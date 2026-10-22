import { MoreVertical } from 'lucide-react';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { useToggleBlock } from '@/lib/blocklist';
import { useDismissRec } from '@/lib/recs';
import { cn } from '@/lib/utils';

/**
 * RecDismissMenu — kebab (⋮) у карточки ленты: скрыть с причиной. Причина
 * нужна модели близости (только «не интересно» — негатив). Ниже — «не
 * предлагать автора / цикл» (план recsys/03, 3.2), если карточка знает их id
 * (секции правил). Триггер — компактный bordered-чип поверх обложки, как у
 * RatingPromptMenu; stopPropagation — чтобы клик не проваливался в
 * родительскую ссылку-карточку.
 */
export function RecDismissMenu({
  workId,
  authors = [],
  series,
  className,
}: {
  workId: number;
  authors?: { id: number; name: string }[];
  series?: { id: number; title: string };
  className?: string;
}) {
  const dismiss = useDismissRec();
  const block = useToggleBlock();
  const blockable = authors.slice(0, 3);
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        aria-label="Скрыть из ленты"
        onClick={(e) => {
          e.preventDefault();
          e.stopPropagation();
        }}
        className={cn(
          'flex size-7 items-center justify-center rounded-full border border-border bg-background/90 text-muted-foreground shadow-sm backdrop-blur transition hover:bg-accent hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring',
          className,
        )}
      >
        <MoreVertical className="size-4" aria-hidden />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" onClick={(e) => e.stopPropagation()}>
        <DropdownMenuItem onClick={() => dismiss.mutate({ workId, reason: 'not_interested' })}>
          Не интересно
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => dismiss.mutate({ workId, reason: 'already_read' })}>
          Уже читал
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => dismiss.mutate({ workId, reason: 'later' })}>
          Потом
        </DropdownMenuItem>
        {blockable.length > 0 || series ? <DropdownMenuSeparator /> : null}
        {blockable.map((a) => (
          <DropdownMenuItem key={a.id} onClick={() => block.mutate({ target: 'author', id: a.id, next: true })}>
            Не предлагать автора: {a.name}
          </DropdownMenuItem>
        ))}
        {series ? (
          <DropdownMenuItem onClick={() => block.mutate({ target: 'series', id: series.id, next: true })}>
            Не предлагать цикл: {series.title}
          </DropdownMenuItem>
        ) : null}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
