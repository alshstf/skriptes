import { Bell, Star } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { useToggleFavorite, type FavoriteTarget } from '@/lib/favorites';
import { cn } from '@/lib/utils';

/**
 * FavoriteButton — звёздочка для карточки книги, автора или серии.
 *
 * Тип сущности определяет:
 *  - URL мутации (см. useToggleFavorite/PATH);
 *  - какой react-query ключ инвалидировать (book/author/series);
 *  - текст в aria-label и подпись.
 *
 * Сам флаг приходит сверху из родителя — оптимистическое обновление
 * useToggleFavorite пишет в кэш конкретного ресурса, поэтому при
 * следующем рендере value уже обновится без onChange-колбэка.
 */
export function FavoriteButton({
  target,
  id,
  isFavorite,
  labelHidden,
}: {
  target: FavoriteTarget;
  id: number;
  isFavorite: boolean;
  /** Если true — текст рядом со звёздочкой никогда не показывается. */
  labelHidden?: boolean;
}) {
  const toggle = useToggleFavorite();
  const next = !isFavorite;
  const text = labelText(target, isFavorite);
  // Книга — ★ «избранное» (жёлтая, исключение из монохрома). Автор/серия —
  // «подписка»: колокольчик (заполненный foreground'ом, когда подписан).
  const Icon = target === 'book' ? Star : Bell;
  return (
    <Button
      variant="ghost"
      size="sm"
      onClick={() => toggle.mutate({ target, id, next })}
      disabled={toggle.isPending}
      aria-pressed={isFavorite}
      aria-label={ariaLabel(target, isFavorite)}
      className="gap-1"
    >
      <Icon
        className={cn(
          'size-4',
          isFavorite
            ? target === 'book'
              ? 'fill-yellow-500 stroke-yellow-500'
              : 'fill-foreground'
            : 'text-muted-foreground',
        )}
        aria-hidden
      />
      {labelHidden ? null : <span className="hidden sm:inline">{text}</span>}
    </Button>
  );
}

function ariaLabel(target: FavoriteTarget, isFav: boolean): string {
  if (target === 'book') {
    return isFav ? 'Убрать книгу из избранного' : 'Добавить книгу в избранное';
  }
  if (target === 'author') {
    return isFav ? 'Отписаться от автора' : 'Подписаться на автора';
  }
  return isFav ? 'Отписаться от серии' : 'Подписаться на серию';
}

function labelText(target: FavoriteTarget, isFav: boolean): string {
  if (target === 'book') return isFav ? 'В избранном' : 'В избранное';
  if (target === 'author') return isFav ? 'Подписаны' : 'Подписаться';
  return isFav ? 'Подписаны' : 'Подписаться';
}
