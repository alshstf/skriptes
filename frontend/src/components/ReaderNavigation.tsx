import { useEffect, useRef, useState } from 'react';
import { Undo2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet';
import { cn } from '@/lib/utils';

// Навигация по книге в ридере: оглавление (#593) и слайдер прогресса (#592) —
// как в Яндекс Книгах. После прыжка — «Вернуться» на прежнее место, чтобы
// случайный рывок не стоил места чтения.

/** TocItem — глава из оглавления книги (foliate-reader.js, {type:'toc'}). */
export type TocItem = { id: number | null; label: string; href: string; depth: number };

/** Место, куда вернуться после прыжка по оглавлению или слайдеру. */
export type ReturnPlace = { cfi: string; fraction: number | null };

const percentLabel = (f: number | null) => (f === null ? '' : `${Math.round(f * 100)} %`);

/** TocSheet — «Оглавление»: главы книги, текущая отмечена, тап — переход. */
export function TocSheet({
  open,
  onOpenChange,
  items,
  currentId,
  onGo,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  items: TocItem[];
  currentId: number | null;
  onGo: (item: TocItem) => void;
}) {
  const currentRef = useRef<HTMLButtonElement | null>(null);
  // Длинное оглавление открывается на текущей главе.
  useEffect(() => {
    if (!open) return;
    const t = setTimeout(() => currentRef.current?.scrollIntoView({ block: 'center' }), 0);
    return () => clearTimeout(t);
  }, [open]);
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="left" className="w-[90%] gap-0 p-0 sm:max-w-md">
        <SheetHeader className="border-b border-border">
          <SheetTitle>Оглавление</SheetTitle>
          <SheetDescription>Главы книги — тап, чтобы перейти.</SheetDescription>
        </SheetHeader>
        <ul className="flex-1 overflow-y-auto py-1">
          {items.map((it, i) => {
            const current = it.id !== null && it.id === currentId;
            return (
              <li key={`${it.id ?? 'x'}-${i}`}>
                <button
                  ref={current ? currentRef : undefined}
                  type="button"
                  aria-current={current ? 'location' : undefined}
                  onClick={() => onGo(it)}
                  className={cn(
                    'block w-full py-2.5 pr-4 text-left text-sm text-pretty transition-colors hover:bg-accent',
                    current ? 'bg-accent font-medium' : 'text-foreground',
                  )}
                  style={{ paddingLeft: `${1 + Math.min(it.depth, 4) * 1}rem` }}
                >
                  {it.label}
                </button>
              </li>
            );
          })}
        </ul>
      </SheetContent>
    </Sheet>
  );
}

const STEPS = 1000;

/**
 * ProgressBar — нижняя полоса панели ридера: слайдер по доле книги (индикатор
 * и быстрый переход: тянешь — видно, куда, отпустил — переход) и «Вернуться»
 * после прыжка.
 */
export function ProgressBar({
  visible,
  fraction,
  returnTo,
  onSeek,
  onReturn,
}: {
  visible: boolean;
  fraction: number | null;
  returnTo: ReturnPlace | null;
  onSeek: (fraction: number) => void;
  onReturn: () => void;
}) {
  // drag — пока тянут; target — куда отпустили, до прихода нового места.
  const [drag, setDrag] = useState<number | null>(null);
  const [target, setTarget] = useState<number | null>(null);
  const inputRef = useRef<HTMLInputElement | null>(null);
  useEffect(() => setTarget(null), [fraction]);

  // Переход — на отпускание (change), не на каждый шаг ползунка (input).
  const onSeekRef = useRef(onSeek);
  onSeekRef.current = onSeek;
  useEffect(() => {
    const el = inputRef.current;
    if (!el) return;
    const commit = () => {
      const f = Number(el.value) / STEPS;
      setDrag(null);
      setTarget(f);
      onSeekRef.current(f);
    };
    el.addEventListener('change', commit);
    return () => el.removeEventListener('change', commit);
  }, []);

  const shown = drag ?? target ?? fraction;
  return (
    <div
      inert={!visible}
      className={cn(
        'absolute inset-x-0 bottom-0 z-20 border-t border-border bg-background pt-2 pr-[max(0.75rem,env(safe-area-inset-right))] pb-[max(0.5rem,env(safe-area-inset-bottom))] pl-[max(0.75rem,env(safe-area-inset-left))] shadow-[0_-10px_22px_-10px_rgba(0,0,0,0.9)] transition-[translate,opacity] duration-200 ease-out motion-reduce:transition-none',
        visible ? 'translate-y-0 opacity-100' : 'pointer-events-none translate-y-full opacity-0',
      )}
    >
      <div className="mx-auto flex max-w-3xl items-center gap-3">
        {returnTo ? (
          <Button
            variant="outline"
            size="sm"
            className="shrink-0 gap-1 px-2"
            onClick={onReturn}
            aria-label={`Вернуться на прежнее место${returnTo.fraction !== null ? `, ${percentLabel(returnTo.fraction)}` : ''}`}
          >
            <Undo2 className="size-4" aria-hidden />
            <span className="tabular-nums">{returnTo.fraction !== null ? percentLabel(returnTo.fraction) : 'Назад'}</span>
          </Button>
        ) : null}
        <input
          ref={inputRef}
          type="range"
          min={0}
          max={STEPS}
          step={1}
          disabled={fraction === null}
          value={Math.round((shown ?? 0) * STEPS)}
          onChange={(e) => setDrag(Number(e.target.value) / STEPS)}
          aria-label="Прогресс книги"
          aria-valuetext={percentLabel(shown)}
          className="h-8 min-w-0 flex-1 cursor-pointer accent-foreground disabled:cursor-default disabled:opacity-50"
        />
        <span
          className={cn('w-12 shrink-0 text-right text-sm tabular-nums', drag !== null ? 'font-medium' : 'text-muted-foreground')}
        >
          {percentLabel(shown)}
        </span>
      </div>
    </div>
  );
}
