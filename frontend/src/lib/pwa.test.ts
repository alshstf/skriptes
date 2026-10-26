import { afterEach, describe, it, expect, vi } from 'vitest';
import { dropLegacyCaches, isLocalhostHost, LEGACY_CACHES } from './pwa';

/**
 * isLocalhostHost управляет тем, регистрируется ли SW. Регрессия здесь
 * либо ломает PWA на production (false-positive — например подумаем что
 * "books.example.com" это localhost), либо снова даст «no-response»
 * ошибку на dev-стенде (false-negative). Покрываем оба направления.
 */
describe('isLocalhostHost', () => {
  it('true для bare loopback', () => {
    expect(isLocalhostHost('localhost')).toBe(true);
    expect(isLocalhostHost('127.0.0.1')).toBe(true);
    expect(isLocalhostHost('::1')).toBe(true);
  });

  it('true для *.localhost (наш Caddy dev-стенд)', () => {
    expect(isLocalhostHost('skriptes.localhost')).toBe(true);
    expect(isLocalhostHost('app.skriptes.localhost')).toBe(true);
  });

  it('true для зарезервированных под dev TLD: .local и .test', () => {
    expect(isLocalhostHost('skriptes.local')).toBe(true);
    expect(isLocalhostHost('example.test')).toBe(true);
  });

  it('false для production-доменов', () => {
    expect(isLocalhostHost('skriptes.homelab.shustov.pro')).toBe(false);
    expect(isLocalhostHost('books.example.com')).toBe(false);
    expect(isLocalhostHost('skriptes.io')).toBe(false);
  });

  it('false для приватных IP (там может быть валидный LE через DNS-01)', () => {
    // Домен в private subnet всё равно может иметь нормальный cert через
    // DNS-01 challenge; не выключаем SW по факту IP.
    expect(isLocalhostHost('192.168.0.10')).toBe(false);
    expect(isLocalhostHost('10.0.0.1')).toBe(false);
  });

  it('case-insensitive по суффиксу', () => {
    expect(isLocalhostHost('Skriptes.LocalHost')).toBe(true);
    expect(isLocalhostHost('FOO.LOCAL')).toBe(true);
  });

  it('false для "localhost" в середине имени (а не как суффикс)', () => {
    // Маленькая защита от подделок типа "localhost.evil.com".
    expect(isLocalhostHost('localhost.evil.com')).toBe(false);
    expect(isLocalhostHost('mylocal.app')).toBe(false);
  });
});

// Прежний runtime-кэш данных держал устаревшие позиции чтения (#609) —
// новый SW его не читает, но и не удаляет: это делает страница.
describe('dropLegacyCaches', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('удаляет кэши прежних версий, включая skriptes-data', async () => {
    const del = vi.fn(async () => true);
    vi.stubGlobal('caches', { delete: del });
    await dropLegacyCaches();
    expect(LEGACY_CACHES).toContain('skriptes-data');
    expect(del.mock.calls.map((c) => (c as unknown[])[0])).toEqual(LEGACY_CACHES);
  });

  it('без Cache Storage и при ошибке — молча', async () => {
    vi.stubGlobal('caches', undefined);
    await expect(dropLegacyCaches()).resolves.toBeUndefined();
    vi.stubGlobal('caches', { delete: () => Promise.reject(new Error('denied')) });
    await expect(dropLegacyCaches()).resolves.toBeUndefined();
  });
});
