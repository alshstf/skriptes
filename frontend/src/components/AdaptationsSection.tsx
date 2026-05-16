import { Film, Tv, ExternalLink } from 'lucide-react';
import { Skeleton } from '@/components/ui/skeleton';
import { Badge } from '@/components/ui/badge';
import { useAdaptations } from '@/lib/adaptations';
import type { Adaptation } from '@/lib/adaptations';

/**
 * AdaptationsSection — секция "По этой книге снято" на странице книги.
 *
 * Три состояния — параллельно AnnotationBlock (книга) и AuthorBio
 * (страница автора): секция всегда рендерится с одинаковым заголовком,
 * меняется только содержимое.
 *
 *  1. items.length > 0 → горизонтальный скролл с карточками. Счётчик
 *     в скобках рядом с заголовком.
 *  2. items пустой, enrichment_status === "pending" → скелетон из
 *     трёх карточек (поллинг useAdaptations догонит до 30s ретраев).
 *  3. items пустой, enrichment_status === "done" → "Экранизаций не
 *     найдено." Это сигнал что Wikidata-обогащение отработало и
 *     ничего по этой книге не нашло; иначе пользователь не отличит
 *     "ещё ищем" от "уже посмотрели".
 */
export function AdaptationsSection({ bookId }: { bookId: number }) {
  const { data, isLoading } = useAdaptations(bookId);

  const items = data?.items ?? [];
  const exhausted = data?.enrichment_status === 'done';
  const showSkeleton = (isLoading || !data || !exhausted) && items.length === 0;

  return (
    <section className="space-y-3" aria-label="Экранизации">
      <h3 className="flex items-center gap-2 text-sm font-medium">
        <Film className="size-4" aria-hidden />
        По этой книге снято
        {items.length > 0 ? (
          <span className="text-muted-foreground font-normal">({items.length})</span>
        ) : null}
      </h3>
      {items.length > 0 ? (
        /*
          Горизонтальный список карточек. На мобильном — свайп; на
          десктопе flex с overflow-x. Гэп подобран чтобы 4-5 постеров
          помещались в типичную ширину карточки книги.
        */
        <ul className="flex gap-3 overflow-x-auto pb-1 -mx-1 px-1">
          {items.map((a) => (
            <li key={a.id} className="shrink-0 w-32 sm:w-36">
              <AdaptationCard a={a} />
            </li>
          ))}
        </ul>
      ) : showSkeleton ? (
        <PendingSkeletonRow />
      ) : (
        <p className="text-sm italic text-muted-foreground">Экранизаций не найдено.</p>
      )}
    </section>
  );
}

function PendingSkeletonRow() {
  return (
    <div
      className="flex gap-3 overflow-hidden pb-1 -mx-1 px-1"
      aria-busy="true"
      aria-label="Экранизации загружаются"
    >
      {[0, 1, 2].map((i) => (
        <div key={i} className="shrink-0 w-32 sm:w-36 space-y-2">
          <Skeleton className="aspect-[2/3] w-full rounded-md" />
          <Skeleton className="h-3 w-full" />
          <Skeleton className="h-3 w-2/3" />
        </div>
      ))}
    </div>
  );
}

function AdaptationCard({ a }: { a: Adaptation }) {
  const KindIcon = a.kind === 'tv_series' || a.kind === 'miniseries' ? Tv : Film;
  // Каждая карточка — ссылка на канонический Wikidata/IMDB URL в новом
  // окне. Без URL — div без интерактива (редкий кейс, мы стараемся
  // всегда возвращать ext_url).
  const Wrapper = (props: { children: React.ReactNode }) =>
    a.ext_url ? (
      <a
        href={a.ext_url}
        target="_blank"
        rel="noopener noreferrer"
        className="block group focus:outline-none focus-visible:ring-2 focus-visible:ring-ring rounded-md"
      >
        {props.children}
      </a>
    ) : (
      <div className="block">{props.children}</div>
    );

  return (
    <Wrapper>
      <div className="space-y-2">
        {a.poster_path ? (
          <img
            src={`/api/covers/${a.poster_path}`}
            alt={`Постер: ${a.title}`}
            className="aspect-[2/3] w-full rounded-md object-cover border border-border bg-muted shadow-sm group-hover:opacity-90 transition-opacity"
            loading="lazy"
          />
        ) : (
          <div
            className="aspect-[2/3] w-full rounded-md border border-border bg-muted flex items-center justify-center text-muted-foreground"
            role="img"
            aria-label={`Постер: ${a.title} (нет)`}
          >
            <KindIcon className="size-8 opacity-40" aria-hidden />
          </div>
        )}
        <div className="space-y-1">
          <p className="text-sm font-medium line-clamp-2 group-hover:underline">
            {a.title}
            {a.ext_url ? (
              <ExternalLink
                className="ml-1 inline size-3 opacity-60 align-baseline"
                aria-hidden
              />
            ) : null}
          </p>
          <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xs text-muted-foreground">
            {a.year ? <span>{a.year}</span> : null}
            {a.kind && a.kind !== 'film' ? (
              <Badge variant="secondary" className="font-normal text-[10px] px-1.5 py-0">
                {kindLabel(a.kind)}
              </Badge>
            ) : null}
          </div>
          {a.director ? (
            <p className="text-xs text-muted-foreground line-clamp-1" title={a.director}>
              реж. {a.director}
            </p>
          ) : null}
        </div>
      </div>
    </Wrapper>
  );
}

function kindLabel(kind: string): string {
  switch (kind) {
    case 'tv_series':
      return 'сериал';
    case 'miniseries':
      return 'мини-сериал';
    case 'anime':
      return 'аниме';
    case 'film':
      return 'фильм';
    default:
      return kind;
  }
}
