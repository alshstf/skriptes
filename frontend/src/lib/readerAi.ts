/**
 * lib/readerAi.ts — ИИ-функции читалки: «Объяснить», «Кто это», «Что значит»,
 * «Перевести», «Напомнить, что было». Сервер отвечает потоком событий
 * (POST /api/books/{id}/ask, text/event-stream); модели уходит только текст до
 * места чтения.
 */
import { NETWORK_ERROR_MESSAGE } from './api';

export type AiMode = 'explain' | 'who' | 'recap' | 'define' | 'translate';

/** Место в книге, как его знает ридер: раздел, фрагмент, доля раздела до него. */
export type AiPlace = { section: number; excerpt: string; hint: number | null };

export type AiTurn = { role: 'user' | 'assistant'; text: string };

export type AiQuote = { text: string; section: number };

export type AiEvent =
  | { kind: 'stage'; stage: number }
  | { kind: 'delta'; text: string }
  | { kind: 'done'; quotes?: AiQuote[]; truncated?: boolean }
  | { kind: 'error'; text: string };

export type AskInput = {
  mode: AiMode;
  place: AiPlace;
  question?: string;
  thread?: AiTurn[];
  /** Ступень контекста, достигнутая в этом разговоре: продолжение идёт с неё. */
  stage?: number;
  lang?: string;
};

export const AI_GENERIC_ERROR = 'Не удалось получить ответ. Попробуйте ещё раз.';

/**
 * askBook — задать вопрос по книге. События приходят в onEvent по мере ответа;
 * любая ошибка (сеть, отказ сервера до начала потока) — событием error, функция
 * не бросает. Прервать — через signal (тогда событий больше не будет).
 */
export async function askBook(
  bookId: number,
  input: AskInput,
  onEvent: (e: AiEvent) => void,
  signal?: AbortSignal,
): Promise<void> {
  let res: Response;
  try {
    res = await fetch(`/api/books/${bookId}/ask`, {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      signal,
      body: JSON.stringify({
        mode: input.mode,
        section: input.place.section,
        excerpt: input.place.excerpt,
        hint: input.place.hint ?? 0.5,
        question: input.question ?? '',
        thread: input.thread ?? [],
        stage: input.stage ?? 0,
        lang: input.lang ?? 'ru',
      }),
    });
  } catch {
    if (!signal?.aborted) onEvent({ kind: 'error', text: NETWORK_ERROR_MESSAGE });
    return;
  }
  if (!res.ok || !res.body) {
    let message = AI_GENERIC_ERROR;
    try {
      const body: unknown = await res.json();
      if (body && typeof body === 'object' && 'message' in body && typeof body.message === 'string' && body.message) {
        message = body.message;
      }
    } catch {
      /* не JSON */
    }
    onEvent({ kind: 'error', text: message });
    return;
  }
  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buf = '';
  let finished = false;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      buf += decoder.decode(value, { stream: true });
      for (const ev of takeEvents(buf, (rest) => (buf = rest))) {
        if (ev.kind === 'done' || ev.kind === 'error') finished = true;
        onEvent(ev);
      }
    }
  } catch {
    if (signal?.aborted) return;
  }
  // Поток оборвался без done (таймаут, обрыв сети) — читатель должен это увидеть.
  if (!finished && !signal?.aborted) onEvent({ kind: 'error', text: 'Ответ оборвался. Попробуйте ещё раз.' });
}

/** Разбор SSE: целые события из буфера, неполный хвост — обратно через setRest. */
export function takeEvents(buf: string, setRest: (rest: string) => void): AiEvent[] {
  const out: AiEvent[] = [];
  let at: number;
  while ((at = buf.indexOf('\n\n')) >= 0) {
    const block = buf.slice(0, at);
    buf = buf.slice(at + 2);
    for (const line of block.split('\n')) {
      if (!line.startsWith('data: ')) continue;
      try {
        const ev: unknown = JSON.parse(line.slice(6));
        if (ev && typeof ev === 'object' && 'kind' in ev) out.push(ev as AiEvent);
      } catch {
        /* битая строка — пропускаем */
      }
    }
  }
  setRest(buf);
  return out;
}

/** Выделено имя или пара слов — уместны «Кто это» и «Что значит». */
export function isShortSelection(text: string): boolean {
  const t = text.trim();
  return t.length > 0 && t.length <= 60 && t.split(/\s+/).length <= 4;
}
