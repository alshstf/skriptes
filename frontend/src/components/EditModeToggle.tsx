import { Check, Pencil } from 'lucide-react';
import { BackButton } from '@/components/BackButton';
import { Button } from '@/components/ui/button';
import { setEditMode, useEditMode, useIsAdmin } from '@/lib/editMode';

/** EditModeToggle — «Править» / «Готово» для админа (#444); остальным не виден. */
export function EditModeToggle() {
  const admin = useIsAdmin();
  const on = useEditMode();
  if (!admin) return null;
  return (
    <Button
      variant={on ? 'secondary' : 'ghost'}
      size="sm"
      onClick={() => setEditMode(!on)}
      aria-pressed={on}
      className="gap-1.5 text-muted-foreground"
    >
      {on ? <Check className="size-4" aria-hidden /> : <Pencil className="size-4" aria-hidden />}
      {on ? 'Готово' : 'Править'}
    </Button>
  );
}

/** CardToolbar — строка над карточкой: «Назад» и переключатель режима правки. */
export function CardToolbar() {
  return (
    <div className="flex items-center justify-between gap-2">
      <BackButton />
      <EditModeToggle />
    </div>
  );
}
