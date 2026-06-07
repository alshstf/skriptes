import { Check } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { BookCover } from '@/components/BookCover';
import { EditionActions } from '@/components/EditionActions';
import { useLanguageMap } from '@/lib/content';
import { type EditionRef } from '@/lib/books';
import { cn } from '@/lib/utils';

/**
 * EditionRow — одно издание (fb2-файл) в секции «Издания».
 *
 * Издания равноправны (никакого «активного»/«открытого»): плоская строка с
 * мини-обложкой, атрибутами и компактными действиями. Прогресс чтения — на
 * строке (per-edition: позиция/CFI привязаны к файлу). Формат не показываем —
 * вся коллекция fb2, формат выбирается в меню скачивания.
 */
export function EditionRow({ edition, workTitle }: { edition: EditionRef; workTitle: string }) {
  const langMap = useLanguageMap();
  const langLabel = edition.lang ? (langMap.get(edition.lang) ?? edition.lang) : null;
  // Собственное название/серия издания, если отличаются от work-level — сигнал
  // «чужого» издания после ошибочного слияния (помогает решиться на split).
  const ownTitle =
    edition.edition_title && edition.edition_title !== workTitle
      ? edition.edition_title
      : edition.title && edition.title !== workTitle
        ? edition.title
        : null;
  const pct =
    edition.reading_fraction && edition.reading_fraction > 0
      ? Math.min(100, Math.max(1, Math.round(edition.reading_fraction * 100)))
      : 0;

  return (
    <li className="rounded-lg border border-border p-3 sm:p-4">
      <div className="flex gap-3">
        <BookCover
          coverPath={edition.cover_path}
          title={workTitle}
          placeholder="mini"
          className="w-12 shrink-0"
        />
        <div className="flex min-w-0 flex-1 flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
          <div className="min-w-0 space-y-1.5">
            <div className="flex flex-wrap items-center gap-1.5">
              {langLabel ? (
                <Badge variant="outline" className="font-normal uppercase">
                  {langLabel}
                </Badge>
              ) : null}
              {ownTitle && edition.series?.title ? (
                <Badge variant="outline" className="font-normal">
                  Серия: {edition.series.title}
                </Badge>
              ) : null}
            </div>

            {ownTitle ? (
              <p className="text-sm font-medium text-pretty">{ownTitle}</p>
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
            </dl>

            {edition.is_read ? (
              <p className="inline-flex items-center gap-1 text-xs text-green-700 dark:text-green-400">
                <Check className="size-3.5" aria-hidden /> Прочитано
              </p>
            ) : pct > 0 ? (
              <div className="flex items-center gap-2" aria-label={`Прочитано ${pct}%`}>
                <div className="h-1 w-28 overflow-hidden rounded-full bg-muted">
                  <div className="h-full bg-foreground/60" style={{ width: `${pct}%` }} />
                </div>
                <span className="text-xs text-muted-foreground tabular-nums">{pct}%</span>
              </div>
            ) : null}
          </div>

          <EditionActions editionId={edition.id} readingFraction={edition.reading_fraction} />
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
