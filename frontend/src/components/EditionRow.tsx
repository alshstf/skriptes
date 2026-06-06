import { Link } from '@tanstack/react-router';
import { BookOpen, Check } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { DownloadMenu } from '@/components/DownloadMenu';
import { SendToKindleButton } from '@/components/SendToKindleButton';
import { useLanguageMap } from '@/lib/content';
import { type EditionRef } from '@/lib/books';
import { cn } from '@/lib/utils';

/**
 * EditionRow — одно издание (fb2-файл) в секции «Издания» на странице книги.
 *
 * Монохром: открытое издание выделяется не цветом, а контрастной рамкой +
 * приглушённым фоном + меткой «Открыто». Атрибуты издания (переводчик/издатель/
 * год/ISBN/размер/формат) — компактным inline-списком; действия (Читать/
 * Скачать/Kindle) — справа, по id ИМЕННО этого издания.
 */
export function EditionRow({ edition, isCurrent }: { edition: EditionRef; isCurrent: boolean }) {
  const langMap = useLanguageMap();
  const langLabel = edition.lang ? (langMap.get(edition.lang) ?? edition.lang) : null;

  return (
    <li
      className={cn(
        'rounded-lg border p-3 sm:p-4 transition-colors',
        isCurrent ? 'border-foreground/30 bg-muted/50' : 'border-border',
      )}
    >
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0 space-y-1.5">
          <div className="flex flex-wrap items-center gap-2">
            {langLabel ? (
              <Badge variant="outline" className="font-normal uppercase">
                {langLabel}
              </Badge>
            ) : null}
            {isCurrent ? (
              <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
                <Check className="size-3" aria-hidden /> Открыто
              </span>
            ) : null}
          </div>

          {edition.edition_title ? (
            <p className="text-sm font-medium text-pretty">{edition.edition_title}</p>
          ) : null}

          <dl className="flex flex-wrap gap-x-4 gap-y-0.5 text-xs text-muted-foreground tabular-nums">
            <Meta label="Переводчик" value={edition.translator} />
            <Meta
              label="Год издания"
              value={edition.edition_year ? String(edition.edition_year) : undefined}
            />
            <Meta label="Издатель" value={edition.publisher} />
            <Meta label="ISBN" value={edition.isbn} mono />
            <Meta label="Размер" value={`${(edition.size_bytes / 1024).toFixed(1)} KiB`} />
            <Meta label="Формат" value={edition.ext.toUpperCase()} />
          </dl>
        </div>

        <div className="flex shrink-0 flex-wrap items-center gap-2">
          <Button asChild variant="secondary" size="sm" className="gap-1">
            <Link
              to="/books/$id/read"
              params={{ id: String(edition.id) }}
              aria-label="Открыть издание в браузерном ридере"
            >
              <BookOpen className="size-4" aria-hidden />
              <span className="hidden sm:inline">Читать</span>
            </Link>
          </Button>
          <SendToKindleButton bookId={edition.id} />
          <DownloadMenu bookId={edition.id} />
        </div>
      </div>
    </li>
  );
}

// Meta — одна пара «метка: значение», скрывается если значения нет.
function Meta({ label, value, mono = false }: { label: string; value?: string; mono?: boolean }) {
  if (!value) return null;
  return (
    <span>
      <span className="opacity-70">{label}:</span>{' '}
      <span className={cn('text-foreground/80', mono && 'font-mono break-all')}>{value}</span>
    </span>
  );
}
