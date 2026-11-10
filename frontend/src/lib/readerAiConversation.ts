import { useCallback, useEffect, useRef, useState } from 'react';
import { askBook, type AiMode, type AiPlace, type AiQuote, type AiTurn } from '@/lib/readerAi';

/** Что спросили и что ответили — одна пара разговора. */
export type Exchange = {
  mode: AiMode;
  /** Вопрос, как он показан читателю. */
  label: string;
  /** Вопрос, как он уходит в историю разговора. */
  question: string;
  answer: string;
  stage: number;
  quotes: AiQuote[];
  truncated: boolean;
  status: 'asking' | 'done' | 'error';
  error?: string;
};

const MODE_LABEL: Record<AiMode, string> = {
  explain: 'Что здесь происходит?',
  who: 'Кто это?',
  recap: 'Напомнить, что было',
  define: 'Что это значит?',
  translate: 'Перевести',
};

/**
 * useAiConversation — разговор с моделью об одном месте книги. Ответ приходит
 * потоком; следующий вопрос идёт с той же ступени контекста, что и прошлый
 * ответ (сервер читает тот же текст из кэша). reset — новое место.
 */
export function useAiConversation(bookId: number) {
  const [items, setItems] = useState<Exchange[]>([]);
  const abortRef = useRef<AbortController | null>(null);
  const itemsRef = useRef(items);
  itemsRef.current = items;

  const stop = useCallback(() => {
    abortRef.current?.abort();
    abortRef.current = null;
  }, []);

  const reset = useCallback(() => {
    stop();
    setItems([]);
  }, [stop]);

  useEffect(() => stop, [stop]);

  const ask = useCallback(
    (mode: AiMode, place: AiPlace, question = '') => {
      stop();
      const prev = itemsRef.current.filter((x) => x.status === 'done');
      const thread: AiTurn[] = prev.flatMap((x) => [
        { role: 'user' as const, text: x.question },
        { role: 'assistant' as const, text: x.answer },
      ]);
      const stage = prev.reduce((m, x) => Math.max(m, x.stage), 0);
      const label = question || MODE_LABEL[mode];
      const next: Exchange = {
        mode,
        label,
        question: question || label,
        answer: '',
        stage: 0,
        quotes: [],
        truncated: false,
        status: 'asking',
      };
      setItems([...prev, next]);
      const patch = (fn: (x: Exchange) => Exchange) =>
        setItems((list) => list.map((x, i) => (i === list.length - 1 ? fn(x) : x)));
      const ctrl = new AbortController();
      abortRef.current = ctrl;
      void askBook(
        bookId,
        { mode, place, question, thread, stage },
        (ev) => {
          if (ctrl.signal.aborted) return;
          switch (ev.kind) {
            case 'stage':
              // Новая ступень — текст прежней попытки (если модель что-то успела
              // написать перед тем, как попросить ещё текста) отбрасываем.
              patch((x) => ({ ...x, stage: ev.stage, answer: '' }));
              break;
            case 'delta':
              patch((x) => ({ ...x, answer: x.answer + ev.text }));
              break;
            case 'done':
              patch((x) => ({ ...x, status: 'done', quotes: ev.quotes ?? [], truncated: Boolean(ev.truncated) }));
              break;
            case 'error':
              patch((x) => ({ ...x, status: 'error', error: ev.text }));
              break;
          }
        },
        ctrl.signal,
      );
    },
    [bookId, stop],
  );

  return { items, ask, reset, stop, busy: items.some((x) => x.status === 'asking') };
}
