import { useEffect, useRef, useState, useCallback } from 'react';
import { useParams, useNavigate } from '@tanstack/react-router';
import { ArrowLeft, Check } from 'lucide-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { useReadingPosition, useSavePosition, useToggleRead } from '@/lib/books';

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

  const [ready, setReady] = useState(false);
  const [completed, setCompleted] = useState(false);

  const { data: position, isLoading: posLoading } = useReadingPosition(bookId);
  const save = useSavePosition();
  const toggleRead = useToggleRead();

  // Debounce-таймер для PUT /position. Каждый relocate сдвигает старт.
  const saveTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const lastSavedCfi = useRef<string>('');

  const scheduleSave = useCallback(
    (cfi: string) => {
      if (saveTimer.current) clearTimeout(saveTimer.current);
      saveTimer.current = setTimeout(() => {
        if (cfi && cfi !== lastSavedCfi.current) {
          lastSavedCfi.current = cfi;
          save.mutate({ bookId, pos: cfi });
        }
      }, DEBOUNCE_MS);
    },
    [bookId, save],
  );

  // Шлём финальный flush позиции при размонтировании.
  useEffect(() => {
    return () => {
      if (saveTimer.current) clearTimeout(saveTimer.current);
    };
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
          if (msg.cfi) scheduleSave(msg.cfi);
          break;
        case 'completed':
          // Финальный сейв позиции до отметки прочитанным, чтобы
          // followup-открытие открылось в самом конце.
          if (msg.cfi) {
            lastSavedCfi.current = msg.cfi;
            save.mutate({ bookId, pos: msg.cfi });
          }
          if (!completed) {
            setCompleted(true);
            toggleRead.mutate(
              { bookId, isRead: true },
              {
                onSuccess: () => toast.success('Книга отмечена как прочитанная'),
              },
            );
          }
          break;
        case 'error':
          toast.error(`Ридер: ${msg.reason}${msg.detail ? ` — ${msg.detail}` : ''}`);
          break;
      }
    };
    window.addEventListener('message', handler);
    return () => window.removeEventListener('message', handler);
  }, [bookId, completed, save, scheduleSave, toggleRead]);

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
          onClick={() => navigate({ to: '/books/$id', params: { id: String(bookId) } })}
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
