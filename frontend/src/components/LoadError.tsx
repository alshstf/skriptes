import type { ReactNode } from 'react';
import { Lock } from 'lucide-react';

import { Callout } from '@/components/ui/callout';
import { ApiError } from '@/lib/api';

/**
 * forbiddenMessage — причина отказа 403 от сервера, если он её прислал. Снаружи
 * домашней сети Caddy отвечает на админ-API текстом «администрирование доступно
 * только из домашней сети» (infra/Caddyfile.public, SKRIPTES_LAN_CIDRS) — его и
 * надо показать, а не «Не удалось загрузить».
 */
function forbiddenMessage(error: unknown): string | null {
  if (!(error instanceof ApiError) || error.status !== 403) return null;
  const msg = error.message.trim();
  return msg && !msg.startsWith('HTTP ') ? msg : null;
}

/** lanOnly — отказ «только из домашней сети» (текст Caddyfile.public). */
function lanOnly(msg: string): boolean {
  return /домашней сети/i.test(msg);
}

/**
 * LoadError — ошибка загрузки данных страницы. Отказ с причиной (403) —
 * подсказкой с этой причиной и тем, что делать; остальное — прежним текстом.
 */
export function LoadError({ error, children }: { error: unknown; children: ReactNode }) {
  const msg = forbiddenMessage(error);
  if (msg) {
    return (
      <Callout icon={<Lock className="mt-0.5 size-3.5 shrink-0" aria-hidden />}>
        <p role="alert" className="font-medium text-foreground">
          {msg.charAt(0).toUpperCase() + msg.slice(1)}.
        </p>
        {lanOnly(msg) ? (
          <p className="mt-1">
            Подключитесь к домашнему Wi‑Fi. На iPhone для этой сети выключите «Частный узел iCloud» и «Ограничение
            трекинга IP-адреса» — с ними запросы идут через внешний адрес.
          </p>
        ) : null}
      </Callout>
    );
  }
  return (
    <p role="alert" className="text-sm text-destructive">
      {children}
    </p>
  );
}
