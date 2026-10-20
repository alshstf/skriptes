import { MoreVertical } from 'lucide-react';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { useDismissRec } from '@/lib/recs';
import { cn } from '@/lib/utils';

/**
 * RecDismissMenu — kebab (⋮) у карточки ленты: скрыть с причиной. Причина
 * нужна модели близости (только «не интересно» — негатив). Триггер — компактный
 * bordered-чип поверх обложки, как у RatingPromptMenu; stopPropagation — чтобы
 * клик не проваливался в родительскую ссылку-карточку.
 */
export function RecDismissMenu({ workId, className }: { workId: number; className?: string }) {
  const dismiss = useDismissRec();
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
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
