import type { UseQueryResult } from '@tanstack/react-query';
import { Search } from 'lucide-react';
import { Link } from '@tanstack/react-router';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Callout } from '@/components/ui/callout';
import { Skeleton } from '@/components/ui/skeleton';
import { LoadError } from '@/components/LoadError';
import type { SearchMissesResponse } from '@/lib/admin';
import { pluralRu } from '@/lib/format';

/**
 * SearchMissesCard — «Что ищут и не находят» в админке: агрегат ненайденных
 * запросов за период без привязки к пользователям — подсказка, чем пополнить
 * библиотеку. Клик по запросу — повторить поиск.
 */
export function SearchMissesCard({ query }: { query: UseQueryResult<SearchMissesResponse> }) {
  return (
    <Card className="max-w-2xl">
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-base">
          <Search className="size-4" aria-hidden />
          Ищут и не находят
        </CardTitle>
        <p className="text-sm text-pretty text-muted-foreground">
          Запросы без результатов за {query.data?.days ?? 90} дней — что стоит добавить в библиотеку.
          Кто искал — не показывается.
        </p>
      </CardHeader>
      <CardContent className="pt-2">
        {query.isLoading ? (
          <Skeleton className="h-16 w-full" />
        ) : query.error ? (
          <LoadError error={query.error}>Не удалось загрузить.</LoadError>
        ) : !query.data?.enabled ? (
          <Callout>Лог поиска выключен (`SKRIPTES_SEARCH_LOG_DAYS=0`).</Callout>
        ) : query.data.items.length === 0 ? (
          <Callout>За этот период всё находилось.</Callout>
        ) : (
          <ul className="divide-y divide-border text-sm" data-testid="search-misses">
            {query.data.items.map((m) => (
              <li key={m.q} className="flex items-center gap-3 py-1.5">
                <Link to="/books" search={{ q: m.q }} className="min-w-0 flex-1 truncate underline-offset-2 hover:underline">
                  {m.q}
                </Link>
                <span className="shrink-0 tabular-nums text-muted-foreground">
                  {m.count} {pluralRu(m.count, ['раз', 'раза', 'раз'])}
                  {m.users > 1 ? ` · ${m.users} ${pluralRu(m.users, ['человек', 'человека', 'человек'])}` : ''}
                </span>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}
