/**
 * lib/readerPosition.ts — копия места чтения на устройстве (#591).
 *
 * Сервер узнаёт позицию с дебаунсом и при уходе со страницы, но на телефоне
 * фоновую вкладку замораживают или выгружают раньше, чем запрос уходит, — и
 * книга открывалась там, где позиция успела сохраниться в прошлый раз. Поэтому
 * каждое место пишем ещё и в localStorage (синхронно, доживает до выгрузки).
 *
 * Какую позицию брать при открытии — без сравнения часов устройства и сервера:
 * `synced` — последнее место, про которое мы знаем, что оно на сервере. Если
 * сервер отдаёт именно его, значит, после нас позицию никто не менял, и более
 * свежая локальная копия — наше неотправленное чтение. Если сервер отдаёт
 * другое — читали на другом устройстве, прав сервер.
 */

const KEY_PREFIX = 'skriptes.reader.pos.';

export type LocalPosition = {
  cfi: string;
  fraction: number | null;
  /** Последнее место, подтверждённое сервером (или отданное им при открытии). */
  synced: string;
};

function key(bookId: number): string {
  return KEY_PREFIX + bookId;
}

export function loadLocalPosition(bookId: number): LocalPosition | null {
  try {
    const raw = window.localStorage.getItem(key(bookId));
    if (!raw) return null;
    const v = JSON.parse(raw) as Partial<LocalPosition>;
    if (typeof v?.cfi !== 'string' || typeof v.synced !== 'string') return null;
    return { cfi: v.cfi, fraction: typeof v.fraction === 'number' ? v.fraction : null, synced: v.synced };
  } catch {
    return null;
  }
}

function store(bookId: number, p: LocalPosition) {
  try {
    window.localStorage.setItem(key(bookId), JSON.stringify(p));
  } catch {
    // приватный режим / переполнение — останется только серверная позиция
  }
}

/** rememberLocalPosition — ридер сейчас здесь (ещё не обязательно на сервере). */
export function rememberLocalPosition(bookId: number, cfi: string, fraction: number | null) {
  const prev = loadLocalPosition(bookId);
  store(bookId, { cfi, fraction, synced: prev?.synced ?? '' });
}

/** markPositionSynced — сервер принял (или отдал при открытии) это место. */
export function markPositionSynced(bookId: number, cfi: string) {
  const prev = loadLocalPosition(bookId);
  store(bookId, { cfi: prev?.cfi ?? cfi, fraction: prev?.fraction ?? null, synced: cfi });
}

/** adoptServerPosition — открыли по серверу: локальная копия = его место. */
export function adoptServerPosition(bookId: number, cfi: string) {
  store(bookId, { cfi, fraction: null, synced: cfi });
}

export function clearLocalPosition(bookId: number) {
  try {
    window.localStorage.removeItem(key(bookId));
  } catch {
    // нечего чистить
  }
}

/**
 * startPosition — откуда открыть книгу: неотправленная локальная копия, если
 * сервер с тех пор не менялся, иначе — серверная позиция.
 */
export function startPosition(bookId: number, serverPos: string): { cfi: string; fromLocal: boolean } {
  const local = loadLocalPosition(bookId);
  if (local && local.cfi && local.cfi !== serverPos && local.synced === serverPos) {
    return { cfi: local.cfi, fromLocal: true };
  }
  return { cfi: serverPos, fromLocal: false };
}
