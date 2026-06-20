import { MoreVertical } from 'lucide-react';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { useDismissRatingPrompt, useSnoozeRatingPrompt } from '@/lib/ratings';
import { cn } from '@/lib/utils';

/**
 * RatingPromptMenu — kebab (⋮) у запроса оценки. Два действия:
 *   - «Ещё не прочитал» → отложить (спросим позже);
 *   - «Не буду оценивать» → скрыть, пока не появится явный сигнал прочтения.
 *
 * Используется и в блоке «Оцените прочитанное» на Главной (поверх обложки), и
 * на карточке книги (когда работа ещё не оценена). Триггер — компактный
 * bordered-чип, читаемый и поверх обложки. stopPropagation — чтобы клик не
 * проваливался в родительскую ссылку-карточку.
 */
export function RatingPromptMenu({ workId, className }: { workId: number; className?: string }) {
  const dismiss = useDismissRatingPrompt();
  const snooze = useSnoozeRatingPrompt();
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        aria-label="Действия с запросом оценки"
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
        <DropdownMenuItem onClick={() => snooze.mutate(workId)}>Ещё не прочитал</DropdownMenuItem>
        <DropdownMenuItem onClick={() => dismiss.mutate(workId)}>Не буду оценивать</DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
