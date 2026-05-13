import { Star } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { useToggleFavorite } from '@/lib/favorites';
import { cn } from '@/lib/utils';

/**
 * FavoriteButton — звёздочка для книжной карточки.
 *
 * Контролируемый компонент: текущее состояние приходит через
 * `isFavorite`, useToggleFavorite оптимистически обновляет кэш
 * react-query (см. useBook), так что родителю не нужно
 * передавать onChange.
 */
export function FavoriteButton({
  bookId,
  isFavorite,
}: {
  bookId: number;
  isFavorite: boolean;
}) {
  const toggle = useToggleFavorite();
  const next = !isFavorite;
  return (
    <Button
      variant="ghost"
      size="sm"
      onClick={() => toggle.mutate({ bookId, next })}
      disabled={toggle.isPending}
      aria-pressed={isFavorite}
      aria-label={isFavorite ? 'Убрать из избранного' : 'Добавить в избранное'}
      className="gap-1"
    >
      <Star
        className={cn(
          'size-4',
          isFavorite ? 'fill-yellow-500 stroke-yellow-500' : 'text-muted-foreground',
        )}
        aria-hidden
      />
      <span className="hidden sm:inline">{isFavorite ? 'В избранном' : 'В избранное'}</span>
    </Button>
  );
}
