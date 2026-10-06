import { useEffect, useMemo, useRef, useState, useCallback } from 'react';
import { useParams, useNavigate, useSearch } from '@tanstack/react-router';
import { useQueryClient } from '@tanstack/react-query';
import { ArrowLeft, Bookmark, BookmarkCheck, Check, NotebookPen } from 'lucide-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { apiFetch } from '@/lib/api';
import { useReadingPosition, useSavePosition, useToggleRead, type Book } from '@/lib/books';
import {
  useBookAnnotations,
  useDeleteAnnotation,
  useSaveAnnotation,
  useUpdateAnnotationNote,
  type Annotation,
} from '@/lib/annotations';
import { AnnotationsSheet, NoteDialog, SelectionBar } from '@/components/ReaderAnnotations';

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
 *  - отдельный документ: foliate и epub-контент не видят React-стейт.
 *    Это НЕ изоляция по origin — iframe на нашем origin (allow-same-origin),
 *    и страницы книги (blob:-iframe'ы foliate) тоже. От скриптов в книге
 *    (inline, onerror=, <script src>) защищает только CSP сайта
 *    (nginx-security-headers.conf, script-src 'self'): blob:-iframe'ы её
 *    наследуют. Проверяет e2e/reader-csp.spec.ts;
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
  | { type: 'position'; cfi: string; fraction: number | null; label?: string }
  | { type: 'completed'; cfi: string }
  | { type: 'selection'; cfi: string; text: string }
  | { type: 'selection-clear' }
  | { type: 'annotation-click'; cfi: string }
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
  // ?cfi= — открыть сразу на месте заметки («Мои заметки» на карточке).
  const { cfi: cfiParam } = useSearch({ strict: false }) as { cfi?: string };

  // Закладки и выделения (#389): iframe сообщает выделение и клик по подсветке,
  // родитель хранит заметки и присылает iframe список подсветок.
  const iframeRef = useRef<HTMLIFrameElement | null>(null);
  const annotationsQ = useBookAnnotations(bookId);
  const annotations = useMemo(() => annotationsQ.data ?? [], [annotationsQ.data]);
  const saveAnnotation = useSaveAnnotation(bookId);
  const updateNote = useUpdateAnnotationNote();
  const deleteAnnotation = useDeleteAnnotation();
  const [place, setPlace] = useState<{ cfi: string; fraction: number | null; label: string }>({
    cfi: '',
    fraction: null,
    label: '',
  });
  const [selection, setSelection] = useState<{ cfi: string; text: string } | null>(null);
  const [notesOpen, setNotesOpen] = useState(false);
  const [noteFor, setNoteFor] = useState<{ cfi: string; excerpt: string; existing?: Annotation } | null>(null);
  const toReader = useCallback((msg: Record<string, unknown>) => {
    iframeRef.current?.contentWindow?.postMessage(msg, window.location.origin);
  }, []);
  const bookmarkHere = annotations.find((a) => a.kind === 'bookmark' && a.cfi === place.cfi);
  const annotationsRef = useRef<Annotation[]>([]);
  annotationsRef.current = annotations;
  // Подсветки — в iframe, как только ридер готов и при каждом изменении.
  const highlightCfis = useMemo(
    () => annotations.filter((a) => a.kind === 'highlight').map((a) => a.cfi),
    [annotations],
  );
  useEffect(() => {
    if (ready) toReader({ type: 'set-highlights', cfis: highlightCfis });
  }, [ready, highlightCfis, toReader]);

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
          if (msg.cfi) {
            scheduleSave(msg.cfi, msg.fraction);
            setPlace({ cfi: msg.cfi, fraction: msg.fraction, label: msg.label ?? '' });
          }
          break;
        case 'selection':
          setSelection({ cfi: msg.cfi, text: msg.text });
          break;
        case 'selection-clear':
          setSelection(null);
          break;
        case 'annotation-click': {
          const existing = annotationsRef.current.find((a) => a.kind === 'highlight' && a.cfi === msg.cfi);
          if (existing) setNoteFor({ cfi: existing.cfi, excerpt: existing.excerpt ?? '', existing });
          break;
        }
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

  const initialCfi = cfiParam || position?.pos || '';
  const src = `/foliate-reader.html?src=${encodeURIComponent(`/api/books/${bookId}/epub`)}${
    initialCfi ? `&cfi=${encodeURIComponent(initialCfi)}` : ''
  }`;

  return (
    <div className="fixed inset-0 flex flex-col bg-background">
      {/* pt-safe: тулбар ридера — наверху fixed inset-0, иначе на iOS PWA «К
          карточке»/заголовок лезут под статус-бар (грабля №18). Контент iframe
          ниже остаётся immersive. */}
      <header className="flex items-center gap-2 border-b border-border px-3 py-2 pt-[calc(0.5rem+env(safe-area-inset-top))] shrink-0">
        <Button
          variant="ghost"
          size="sm"
          onClick={() => {
            // Навигация на карточку REPLACE'ом текущей reader-записи.
            // window.history.back() здесь ненадёжен: foliate в iframe плодит
            // СВОИ записи в общей session history (перелистывание/секции), и
            // back мог увести в ридер ДРУГОГО издания, а не на карточку (баг,
            // который заметил юзер). replace не добавляет записей и всегда ведёт
            // на карточку, а browser-back с карточки — на то,
            // что было ДО ридера (список/карточка), но не обратно в ридер.
            //
            // Ведём на РАБОТУ (/works/{work_id}) — основной маршрут карточки
            // (находка аудита: раньше вели на /books/{editionId}, URL «съезжал»
            // на издание). work_id берём из кэша карточки (если пришли с неё),
            // иначе один лёгкий fetch; при любой ошибке — фолбэк на /books/{id}.
            void (async () => {
              try {
                const cached = qc.getQueryData<Book>(['book', String(bookId)]);
                const workID =
                  cached?.work_id ??
                  (await apiFetch<Book>(`/api/books/${bookId}`)).work_id;
                if (workID) {
                  await navigate({ to: '/works/$id', params: { id: String(workID) }, replace: true });
                  return;
                }
              } catch {
                // сеть/404 — фолбэк ниже
              }
              await navigate({ to: '/books/$id', params: { id: String(bookId) }, replace: true });
            })();
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
        <Button
          variant="ghost"
          size="icon-sm"
          disabled={!ready || !place.cfi || saveAnnotation.isPending || deleteAnnotation.isPending}
          aria-label={bookmarkHere ? 'Убрать закладку' : 'Закладка на этой странице'}
          aria-pressed={Boolean(bookmarkHere)}
          onClick={() => {
            if (bookmarkHere) {
              deleteAnnotation.mutate(bookmarkHere.id);
              return;
            }
            saveAnnotation.mutate(
              { kind: 'bookmark', cfi: place.cfi, label: place.label, fraction: place.fraction ?? undefined },
              { onSuccess: () => toast.success('Закладка поставлена') },
            );
          }}
        >
          {bookmarkHere ? <BookmarkCheck className="size-4" aria-hidden /> : <Bookmark className="size-4" aria-hidden />}
        </Button>
        <Button
          variant="ghost"
          size="sm"
          className="gap-1"
          onClick={() => setNotesOpen(true)}
          aria-label="Заметки и закладки"
        >
          <NotebookPen className="size-4" aria-hidden />
          <span className="hidden sm:inline">Заметки</span>
          {annotations.length > 0 ? (
            <span className="text-xs tabular-nums text-muted-foreground">{annotations.length}</span>
          ) : null}
        </Button>
      </header>
      {/*
        iframe рендерит /foliate-reader.html, отдаваемый nginx из
        frontend/public/. Sandbox: разрешаем same-origin (нужен для
        fetch'а /api/books/{id}/epub с кукой сессии), allow-scripts
        (foliate-js — это и есть скрипты), allow-popups (для ext-ссылок
        из epub). НЕ даём allow-top-navigation и allow-forms. С
        allow-same-origin + allow-scripts sandbox сам по себе не изолирует —
        скрипты книги режет CSP (см. комментарий в начале файла).
      */}
      <div className="relative flex min-h-0 flex-1">
        <iframe
          ref={iframeRef}
          title="Foliate reader"
          src={src}
          sandbox="allow-same-origin allow-scripts allow-popups allow-popups-to-escape-sandbox"
          className="flex-1 w-full border-0"
        />
        {selection ? (
          <SelectionBar
            text={selection.text}
            busy={saveAnnotation.isPending}
            onCancel={() => {
              setSelection(null);
              toReader({ type: 'clear-selection' });
            }}
            onHighlight={() =>
              saveAnnotation.mutate(
                { kind: 'highlight', cfi: selection.cfi, excerpt: selection.text, label: place.label,
                  fraction: place.fraction ?? undefined },
                {
                  onSuccess: () => {
                    setSelection(null);
                    toReader({ type: 'clear-selection' });
                  },
                },
              )
            }
            onNote={() => setNoteFor({ cfi: selection.cfi, excerpt: selection.text })}
          />
        ) : null}
      </div>
      <AnnotationsSheet
        open={notesOpen}
        onOpenChange={setNotesOpen}
        items={annotations}
        onGo={(a) => {
          toReader({ type: 'goto', cfi: a.cfi });
          setNotesOpen(false);
        }}
        onDelete={(a) => deleteAnnotation.mutate(a.id)}
        onEdit={(a) => setNoteFor({ cfi: a.cfi, excerpt: a.excerpt ?? '', existing: a })}
      />
      <NoteDialog
        open={noteFor != null}
        excerpt={noteFor?.excerpt ?? ''}
        initial={noteFor?.existing?.note ?? ''}
        busy={saveAnnotation.isPending || updateNote.isPending || deleteAnnotation.isPending}
        onClose={() => setNoteFor(null)}
        onDelete={
          noteFor?.existing
            ? () => {
                const id = noteFor.existing!.id;
                deleteAnnotation.mutate(id, { onSuccess: () => setNoteFor(null) });
              }
            : undefined
        }
        onSave={(note) => {
          if (!noteFor) return;
          if (noteFor.existing) {
            updateNote.mutate({ id: noteFor.existing.id, note }, { onSuccess: () => setNoteFor(null) });
            return;
          }
          saveAnnotation.mutate(
            { kind: 'highlight', cfi: noteFor.cfi, excerpt: noteFor.excerpt, note, label: place.label,
              fraction: place.fraction ?? undefined },
            {
              onSuccess: () => {
                setNoteFor(null);
                setSelection(null);
                toReader({ type: 'clear-selection' });
              },
            },
          );
        }}
      />
    </div>
  );
}
