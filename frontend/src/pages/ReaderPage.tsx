import { useEffect, useRef, useState, useCallback } from 'react';
import { useParams, useNavigate } from '@tanstack/react-router';
import { useQueryClient } from '@tanstack/react-query';
import { ArrowLeft, Check } from 'lucide-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { apiFetch } from '@/lib/api';
import { useReadingPosition, useSavePosition, useToggleRead, type Book } from '@/lib/books';

/**
 * ReaderPage — full-screen ридер на foliate-js через iframe.
 *
 * Архитектура:
 *  - iframe → `/foliate-reader.html?src=/api/books/{id}/epub&cfi=<pos>`
 *  - foliate-js внутри iframe рендерит epub, эмитит relocate-события
 *  - postMessage от iframe → React handler → debounce'ed PUT в /position
 *  - {type:'completed'} от iframe → toggle.mutate({isRead:true})
 *
 * Зачем iframe (а не прямой импорт foliate-js модулей в React):
 *  - изоляция: epub-контент часто содержит inline-JS / CSS, не хотим
 *    давать ему доступ к нашему React-стейту и cookies (iframe = другой
 *    origin контекст для CSP);
 *  - простота интеграции: foliate-view — vanilla custom element, в
 *    React его обернуть несложно, но iframe рендерится атомарно и
 *    стабильнее по жизненному циклу;
 *  - foliate-js идёт ES-модулями со своими dynamic-import'ами, vite
 *    bundle'ить его болезненно; static-asset в /public/ + iframe не
 *    требует ни bundle'инга, ни build-step'ов в нашем pipeline.
 *
 * Auto-mark на дочитывании: iframe шлёт `{type:'completed'}` когда
 * пользователь прокручивает в last-5%-zone. Это срабатывает один раз
 * за сессию (флаг в iframe). При повторном открытии книги отметка уже
 * стоит — не дублируем.
 */

type ReaderMessage =
  | { type: 'ready' }
  | { type: 'position'; cfi: string; fraction: number | null }
  | { type: 'completed'; cfi: string }
  | { type: 'error'; reason: string; detail?: string };

const DEBOUNCE_MS = 3000;

export function ReaderPage() {
  const params = useParams({ strict: false }) as { id: string };
  const bookId = Number(params.id);
  const navigate = useNavigate();
  const qc = useQueryClient();

  const [ready, setReady] = useState(false);
  const [completed, setCompleted] = useState(false);

  const { data: position, isLoading: posLoading } = useReadingPosition(bookId);
  const save = useSavePosition();
  const toggleRead = useToggleRead();

  // Debounce-таймер для PUT /position. Каждый relocate сдвигает старт.
  // pendingPos хранит последнюю позицию пришедшую из foliate — на
  // момент срабатывания таймера в ней актуальный cfi+fraction.
  // wasCompletedRef — флаг что в этой сессии срабатывал 'completed'
  // event (юзер достиг конца книги). Используется в unmount-cleanup
  // чтобы сбросить сохранённую позицию — при следующем открытии
  // ридер начнёт с начала, а не телепортирует в конец.
  const saveTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const lastSavedCfi = useRef<string>('');
  const pendingPos = useRef<{ cfi: string; fraction: number | null } | null>(null);
  const wasCompletedRef = useRef(false);

  const scheduleSave = useCallback(
    (cfi: string, fraction: number | null) => {
      pendingPos.current = { cfi, fraction };
      if (saveTimer.current) clearTimeout(saveTimer.current);
      saveTimer.current = setTimeout(() => {
        const p = pendingPos.current;
        if (!p || !p.cfi || p.cfi === lastSavedCfi.current) return;
        lastSavedCfi.current = p.cfi;
        save.mutate({
          bookId,
          pos: p.cfi,
          fraction: p.fraction ?? undefined,
        });
      }, DEBOUNCE_MS);
    },
    [bookId, save],
  );

  // Cleanup при размонтировании ridera:
  //  1. Отменяем pending-debounce timer.
  //  2. ЕСЛИ юзер дочитал до конца (wasCompletedRef) — сбрасываем
  //     позицию: pos='', fraction=1.0. При следующем открытии ридер
  //     начнёт с начала книги, а не телепортирует в самый конец
  //     (где бы fraction>=0.99 мгновенно дёрнул бы повторный auto-mark
  //     + тост). Reset делается ИМЕННО на unmount, не в 'completed'-
  //     handler — пока юзер в ридере, он может листать назад и
  //     перечитывать концовку, незачем мешать.
  //  3. Иначе — финальный flush последней позиции если она ещё не
  //     успела уехать в БД (юзер вышел в течение debounce-окна).
  //  4. Инвалидируем book-кэш чтобы карточка после возвращения показала
  //     актуальные read_at / reading_fraction вместо stale-данных.
  useEffect(() => {
    return () => {
      if (saveTimer.current) clearTimeout(saveTimer.current);

      const finalBody: Record<string, unknown> | null = (() => {
        if (wasCompletedRef.current) {
          return { pos: '', fraction: 1.0 };
        }
        const p = pendingPos.current;
        if (p && p.cfi && p.cfi !== lastSavedCfi.current) {
          const body: Record<string, unknown> = { pos: p.cfi };
          if (p.fraction !== null) body.fraction = p.fraction;
          return body;
        }
        return null;
      })();

      if (finalBody) {
        // Fire-and-forget — не ждём ответа, ошибки логируем но не
        // показываем (юзер уже на другой странице).
        void apiFetch(`/api/books/${bookId}/position`, {
          method: 'PUT',
          body: finalBody,
        }).catch((err) => {
          console.warn('reader: final position write failed', err);
        });
      }

      void qc.invalidateQueries({ queryKey: ['book', String(bookId)] });
    };
    // bookId / qc — стабильные на протяжении жизни компонента; cleanup
    // должен выполниться РОВНО при unmount, не на каждом re-render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    const handler = (e: MessageEvent<ReaderMessage>) => {
      // Принимаем только сообщения с того же origin — защита от
      // постороннего window.opener / window.open кросс-origin.
      if (e.origin !== window.location.origin) return;
      const msg = e.data;
      if (!msg || typeof msg !== 'object' || !('type' in msg)) return;

      switch (msg.type) {
        case 'ready':
          setReady(true);
          break;
        case 'position':
          if (msg.cfi) scheduleSave(msg.cfi, msg.fraction);
          break;
        case 'completed':
          // Юзер дочитал книгу до конца. Поднимаем флаг — на unmount
          // cleanup сбросит сохранённую позицию, чтобы следующее
          // открытие ридера началось с начала. Прямо сейчас НЕ
          // трогаем pos — юзер всё ещё в ридере и может листать
          // назад / перечитывать концовку.
          //
          // Сохраняем cfi последней страницы как обычно (через
          // pendingPos) — пока сессия идёт, актуальная позиция нужна
          // на случай рефреша браузера, например.
          wasCompletedRef.current = true;
          if (msg.cfi) scheduleSave(msg.cfi, 1.0);

          if (!completed) {
            setCompleted(true);
            // Тост только если книга ещё НЕ была отмечена прочитанной.
            // Иначе при повторном дочитывании (или открытии в конце)
            // юзер видит «Отмечено как прочитанное» хотя факт давно
            // известен — раздражает и сбивает с толку.
            const prevBook = qc.getQueryData<Book>(['book', String(bookId)]);
            if (!prevBook?.is_read) {
              toggleRead.mutate(
                { bookId, isRead: true },
                {
                  onSuccess: () => toast.success('Книга отмечена как прочитанная'),
                },
              );
            }
          }
          break;
        case 'error':
          toast.error(`Ридер: ${msg.reason}${msg.detail ? ` — ${msg.detail}` : ''}`);
          break;
      }
    };
    window.addEventListener('message', handler);
    return () => window.removeEventListener('message', handler);
  }, [bookId, completed, save, scheduleSave, toggleRead, qc]);

  // URL ридера-iframe: src = /api/books/{id}/epub, cfi = последняя
  // сохранённая позиция (если есть). Ждём position-запрос чтобы не
  // открыть iframe дважды (без cfi → с cfi).
  if (posLoading) {
    return (
      <div className="fixed inset-0 flex items-center justify-center bg-background text-muted-foreground">
        Загружаем позицию…
      </div>
    );
  }

  const initialCfi = position?.pos ?? '';
  const src = `/foliate-reader.html?src=${encodeURIComponent(`/api/books/${bookId}/epub`)}${
    initialCfi ? `&cfi=${encodeURIComponent(initialCfi)}` : ''
  }`;

  return (
    <div className="fixed inset-0 flex flex-col bg-background">
      <header className="flex items-center gap-2 border-b border-border px-3 py-2 shrink-0">
        <Button
          variant="ghost"
          size="sm"
          onClick={() => {
            // Browser back уважает реальную историю навигации:
            //   List → Detail → Reader → back → Detail → back → List
            // Использовать navigate({to:'/books/$id'}) нельзя — оно
            // pushает НОВУЮ entry, и тогда стек становится
            //   [List, Detail, Reader, Detail]
            // → browser back из Detail возвращает в Reader (bug, который
            // юзер заметил). Fallback на прямой navigate — если ридер
            // открыт по deeplink'у и в истории ничего нет, чтобы
            // кнопка не выкидывала из приложения.
            if (window.history.length > 1) {
              window.history.back();
            } else {
              void navigate({ to: '/books/$id', params: { id: String(bookId) } });
            }
          }}
          aria-label="Вернуться к карточке книги"
        >
          <ArrowLeft className="size-4 mr-1" aria-hidden />
          К карточке
        </Button>
        <div className="text-sm text-muted-foreground flex-1 truncate">
          {ready ? 'Чтение' : 'Подготовка…'}
        </div>
        {completed ? (
          <span className="inline-flex items-center gap-1 text-sm text-green-600 dark:text-green-400">
            <Check className="size-4" aria-hidden />
            Прочитано
          </span>
        ) : null}
      </header>
      {/*
        iframe рендерит /foliate-reader.html, отдаваемый nginx из
        frontend/public/. Sandbox: разрешаем same-origin (нужен для
        fetch'а /api/books/{id}/epub с кукой сессии), allow-scripts
        (foliate-js — это и есть скрипты), allow-popups (для ext-ссылок
        из epub). НЕ даём allow-top-navigation и allow-forms.
      */}
      <iframe
        title="Foliate reader"
        src={src}
        sandbox="allow-same-origin allow-scripts allow-popups allow-popups-to-escape-sandbox"
        className="flex-1 w-full border-0"
      />
    </div>
  );
}
