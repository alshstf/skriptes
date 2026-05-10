import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { apiFetch, ApiError } from './api';

describe('apiFetch', () => {
  beforeEach(() => vi.useFakeTimers({ shouldAdvanceTime: true }));
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  it('parses JSON 200 and sends credentials', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () =>
      new Response(JSON.stringify({ user: { id: 1, email: 'a@b.c' } }), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      }),
    );
    vi.stubGlobal('fetch', fetchMock);
    const res = await apiFetch<{ user: { id: number } }>('/api/auth/me');
    expect(res.user.id).toBe(1);
    const init = fetchMock.mock.calls[0]![1]!;
    expect(init.credentials).toBe('include');
    expect(init.method).toBe('GET');
  });

  it('serializes JSON body and sets Content-Type on POST', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () =>
      new Response('null', { status: 200, headers: { 'content-type': 'application/json' } }),
    );
    vi.stubGlobal('fetch', fetchMock);
    await apiFetch('/api/auth/login', { method: 'POST', body: { email: 'a', password: 'b' } });
    const init = fetchMock.mock.calls[0]![1]!;
    expect(init.method).toBe('POST');
    expect(init.body).toBe('{"email":"a","password":"b"}');
    expect((init.headers as Record<string, string>)['Content-Type']).toBe('application/json');
  });

  it('throws ApiError on 401 with parsed body', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        new Response(JSON.stringify({ error: 'invalid email or password' }), {
          status: 401,
          headers: { 'content-type': 'application/json' },
        }),
      ),
    );
    let caught: unknown;
    try {
      await apiFetch('/api/auth/me');
    } catch (e) {
      caught = e;
    }
    expect(caught).toBeInstanceOf(ApiError);
    const err = caught as ApiError;
    expect(err.status).toBe(401);
    expect(err.message).toBe('invalid email or password');
    expect(err.isUnauthorized()).toBe(true);
  });

  it('returns undefined on 204 without consuming body', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(null, { status: 204 })));
    const res = await apiFetch<void>('/api/auth/logout', { method: 'POST' });
    expect(res).toBeUndefined();
  });
});
