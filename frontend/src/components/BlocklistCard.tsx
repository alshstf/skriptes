import { Link } from '@tanstack/react-router';
import { Ban } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import { useBlocklist, useToggleBlock } from '@/lib/blocklist';

/**
 * BlocklistCard — «Не предлагать» в профиле (план recsys/03, 3.2): кого
 * пользователь исключил из рекомендаций, с возвратом. Кнопка исключения — на
 * карточке автора/цикла и в меню карточки ленты.
 */
export function BlocklistCard() {
  const list = useBlocklist();
  const toggle = useToggleBlock();
  const authors = list.data?.authors ?? [];
  const series = list.data?.series ?? [];
  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-base">
          <Ban className="size-4" aria-hidden />
          Не предлагать
        </CardTitle>
        <p className="text-sm text-pretty text-muted-foreground">
          Авторы и циклы, которых вы попросили не предлагать: их нет в рекомендациях на Главной и в
          новинках по подпискам, в каталоге и поиске они остаются. Кнопка — на карточке автора или
          цикла и в меню карточки ленты.
        </p>
      </CardHeader>
      <CardContent className="pt-2">
        {list.isLoading ? (
          <Skeleton className="h-10 w-full" />
        ) : authors.length + series.length === 0 ? (
          <p className="text-sm text-muted-foreground">Пока никого.</p>
        ) : (
          <ul className="divide-y divide-border" data-testid="blocklist">
            {authors.map((a) => (
              <li key={`a-${a.id}`} className="flex items-center gap-3 py-2">
                <Link
                  to="/authors/$id"
                  params={{ id: String(a.id) }}
                  className="min-w-0 flex-1 truncate text-sm underline-offset-2 hover:underline"
                >
                  {a.full_name}
                </Link>
                <span className="shrink-0 text-xs text-muted-foreground">автор</span>
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => toggle.mutate({ target: 'author', id: a.id, next: false })}
                  disabled={toggle.isPending}
                  aria-label={`Снова предлагать автора ${a.full_name}`}
                >
                  Вернуть
                </Button>
              </li>
            ))}
            {series.map((s) => (
              <li key={`s-${s.id}`} className="flex items-center gap-3 py-2">
                <Link
                  to="/series/$id"
                  params={{ id: String(s.id) }}
                  className="min-w-0 flex-1 truncate text-sm underline-offset-2 hover:underline"
                >
                  {s.title}
                  {s.author_name ? <span className="text-muted-foreground"> · {s.author_name}</span> : null}
                </Link>
                <span className="shrink-0 text-xs text-muted-foreground">цикл</span>
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => toggle.mutate({ target: 'series', id: s.id, next: false })}
                  disabled={toggle.isPending}
                  aria-label={`Снова предлагать цикл ${s.title}`}
                >
                  Вернуть
                </Button>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}
