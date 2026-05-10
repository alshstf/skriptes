// Тонкий обёртка над fetch для общения с backend.
// Все запросы шлём с credentials:'include' — чтобы cookie skriptes_session
// уезжала на /api/* и возвращалась как Set-Cookie.

export class ApiError extends Error {
  constructor(
    public readonly status: number,
    public readonly body: unknown,
    message: string,
  ) {
    super(message);
    this.name = 'ApiError';
  }

  isUnauthorized(): boolean {
    return this.status === 401;
  }
}

export type RequestOpts = {
  method?: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE';
  body?: unknown;
  signal?: AbortSignal;
};

/**
 * apiFetch — единая точка для всех HTTP-вызовов к backend.
 * - Автоматически шлёт Origin (браузер сам, мы только credentials:'include').
 * - JSON-сериализация тела + Content-Type когда body передан.
 * - Унифицированная обработка не-2xx → ApiError с распарсенным телом.
 */
export async function apiFetch<T>(path: string, opts: RequestOpts = {}): Promise<T> {
  const init: RequestInit = {
    method: opts.method ?? 'GET',
    credentials: 'include',
    signal: opts.signal,
    headers: {},
  };
  if (opts.body !== undefined) {
    init.body = JSON.stringify(opts.body);
    (init.headers as Record<string, string>)['Content-Type'] = 'application/json';
  }
  const res = await fetch(path, init);
  if (res.status === 204) {
    return undefined as T;
  }
  const text = await res.text();
  let parsed: unknown = undefined;
  if (text.length > 0) {
    try {
      parsed = JSON.parse(text);
    } catch {
      parsed = text;
    }
  }
  if (!res.ok) {
    const message =
      (parsed && typeof parsed === 'object' && 'error' in parsed && typeof parsed.error === 'string'
        ? parsed.error
        : `HTTP ${res.status}`);
    throw new ApiError(res.status, parsed, message);
  }
  return parsed as T;
}
