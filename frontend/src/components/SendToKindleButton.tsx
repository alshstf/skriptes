import { useState } from 'react';
import { Send, ChevronDown } from 'lucide-react';
import { Link } from '@tanstack/react-router';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { useKindleTargets, useSendToKindle, type KindleTarget } from '@/lib/kindle';
import { ApiError } from '@/lib/api';

/**
 * SendToKindleButton — кнопка "Отправить на Kindle" для карточки книги.
 *
 * Поведение по количеству target'ов:
 *   0 → disabled с подсказкой "Настройте в /me".
 *   1 → обычная кнопка, клик сразу отправляет.
 *   2+ → dropdown со списком target'ов, клик выбирает куда отправить.
 *
 * 503 от backend (SMTP не сконфигурирован) — toast с пояснением.
 */
export function SendToKindleButton({ bookId }: { bookId: number }) {
  const targetsQ = useKindleTargets();
  const send = useSendToKindle();
  const [sendingTo, setSendingTo] = useState<number | null>(null);

  const targets = targetsQ.data ?? [];
  const disabled = send.isPending || targetsQ.isLoading;

  async function doSend(target: KindleTarget) {
    setSendingTo(target.id);
    try {
      await send.mutateAsync({ bookId, targetId: target.id });
      toast.success(`Отправлено на «${target.label}» (${target.email})`);
    } catch (err) {
      toast.error(formatError(err));
    } finally {
      setSendingTo(null);
    }
  }

  // 0 target'ов — кнопка-приглашение настроить.
  if (targetsQ.isSuccess && targets.length === 0) {
    return (
      <Button variant="outline" size="sm" asChild>
        <Link to="/me" className="gap-2">
          <Send className="size-4" aria-hidden />
          <span>Настроить Kindle</span>
        </Link>
      </Button>
    );
  }

  // 1 target — простая кнопка, отправляет на единственный.
  if (targets.length === 1) {
    const t = targets[0];
    return (
      <Button
        variant="outline"
        size="sm"
        onClick={() => doSend(t)}
        disabled={disabled}
        className="gap-2"
      >
        <Send className="size-4" aria-hidden />
        {sendingTo === t.id ? 'Отправляется…' : 'На Kindle'}
      </Button>
    );
  }

  // 2+ target'ов — dropdown выбора.
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="sm" disabled={disabled} className="gap-2">
          <Send className="size-4" aria-hidden />
          {send.isPending ? 'Отправляется…' : 'На Kindle'}
          <ChevronDown className="size-3.5 opacity-60" aria-hidden />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-64">
        <DropdownMenuLabel className="text-xs">Куда отправить?</DropdownMenuLabel>
        <DropdownMenuSeparator />
        {targets.map((t) => (
          <DropdownMenuItem
            key={t.id}
            disabled={sendingTo !== null}
            onSelect={(e) => {
              e.preventDefault();
              void doSend(t);
            }}
            className="flex flex-col items-start gap-0"
          >
            <span className="font-medium">{t.label}</span>
            <span className="text-xs text-muted-foreground truncate w-full">{t.email}</span>
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function formatError(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.status === 503) return 'SMTP не настроен на сервере.';
    if (err.status === 404) return 'Адресат или книга не найдены.';
    if (err.status === 502) return 'Не удалось доставить SMTP-серверу.';
    return `Ошибка отправки: ${err.message}`;
  }
  return 'Не удалось отправить.';
}
