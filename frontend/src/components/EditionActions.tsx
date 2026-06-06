import { useState } from 'react';
import { Link } from '@tanstack/react-router';
import { BookOpen, MoreHorizontal, Download, Send } from 'lucide-react';
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
import { downloadFormats } from '@/lib/formats';
import { useKindleTargets, useSendToKindle, type KindleTarget } from '@/lib/kindle';
import { ApiError } from '@/lib/api';

/**
 * EditionActions — компактные действия одного издания в секции «Издания».
 *
 * Чтобы строки не рябили (изданий может быть много), всё кроме основного
 * «Читать» спрятано в одно меню «⋯»: скачивание (все форматы) + отправка на
 * Kindle. Итого две контролы на строку вместо трёх россыпью.
 */
export function EditionActions({
  editionId,
  readingFraction,
}: {
  editionId: number;
  readingFraction?: number;
}) {
  const targetsQ = useKindleTargets();
  const send = useSendToKindle();
  const [sending, setSending] = useState(false);
  const targets = targetsQ.data ?? [];

  async function doSend(t: KindleTarget) {
    setSending(true);
    try {
      await send.mutateAsync({ bookId: editionId, targetId: t.id });
      toast.success(`Отправлено на «${t.label}» (${t.email})`);
    } catch (err) {
      toast.error(kindleError(err));
    } finally {
      setSending(false);
    }
  }

  return (
    <div className="flex items-center gap-1.5 shrink-0">
      <Button asChild variant="secondary" size="sm" className="gap-1">
        <Link
          to="/books/$id/read"
          params={{ id: String(editionId) }}
          aria-label="Открыть издание в браузерном ридере"
        >
          <BookOpen className="size-4" aria-hidden />
          <span className="hidden sm:inline">{readLabel(readingFraction)}</span>
        </Link>
      </Button>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="ghost" size="icon-sm" aria-label="Скачать или отправить на Kindle">
            <MoreHorizontal className="size-4" aria-hidden />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-72">
          <DropdownMenuLabel className="flex items-center gap-2 text-xs">
            <Download className="size-3.5" aria-hidden /> Скачать
          </DropdownMenuLabel>
          {downloadFormats.map((f) => (
            <DropdownMenuItem key={f.id} asChild>
              <a
                href={`/api/books/${editionId}/download?format=${f.id}`}
                download
                className="flex flex-col items-start gap-0.5 cursor-pointer"
              >
                <span className="font-medium whitespace-nowrap">{f.label}</span>
                <span className="text-xs text-muted-foreground whitespace-normal break-words">
                  {f.sub}
                </span>
              </a>
            </DropdownMenuItem>
          ))}
          <DropdownMenuSeparator />
          <DropdownMenuLabel className="flex items-center gap-2 text-xs">
            <Send className="size-3.5" aria-hidden /> На Kindle
          </DropdownMenuLabel>
          {targets.length === 0 ? (
            <DropdownMenuItem asChild>
              <Link to="/me" className="cursor-pointer">
                Настроить Kindle…
              </Link>
            </DropdownMenuItem>
          ) : (
            targets.map((t) => (
              <DropdownMenuItem
                key={t.id}
                disabled={sending}
                onSelect={(e) => {
                  e.preventDefault();
                  void doSend(t);
                }}
                className="flex flex-col items-start gap-0"
              >
                <span className="font-medium">{t.label}</span>
                <span className="text-xs text-muted-foreground truncate w-full">{t.email}</span>
              </DropdownMenuItem>
            ))
          )}
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  );
}

// readLabel — «Читать» без прогресса, иначе «Продолжить N%» (1..99%).
function readLabel(fraction?: number): string {
  if (fraction === undefined || fraction <= 0 || fraction >= 1) return 'Читать';
  return `Продолжить ${Math.max(1, Math.round(fraction * 100))}%`;
}

function kindleError(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.status === 503) return 'SMTP не настроен на сервере.';
    if (err.status === 404) return 'Адресат или книга не найдены.';
    if (err.status === 502) return 'Не удалось доставить SMTP-серверу.';
    return `Ошибка отправки: ${err.message}`;
  }
  return 'Не удалось отправить.';
}
