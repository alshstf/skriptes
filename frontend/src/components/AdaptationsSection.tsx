import { Film, Tv, ExternalLink } from 'lucide-react';
import { Skeleton } from '@/components/ui/skeleton';
import { Badge } from '@/components/ui/badge';
import { useAdaptations } from '@/lib/adaptations';
import type { Adaptation } from '@/lib/adaptations';

/**
 * AdaptationsSection — секция "По этой книге снято" на странице книги.
 *
 * Состояния:
 *  1. data === undefined (первый запрос грузится) → скелетон из 2-3 карточек.
 *  2. enrichment_status === "pending" И items пустой → "Ищем экранизации"
 *     со скелетоном (пока поллим), без скрытия секции — пользователю
 *     видна обратная связь что что-то происходит.
 *  3. status === "done" + items пустой → секция вообще не рендерится.
 *     "Экранизаций нет" слишком навязчиво: для большинства книг будет
 *     пустой список, незачем дёргать UI.
 *  4. items.length > 0 → горизонтальный скролл с карточками.
 */
export function AdaptationsSection({ bookId }: { bookId: number }) {
  const { data, isLoading } = useAdaptations(bookId);

  if (isLoading || (!data && !isLoading)) {
    return <PendingSkeleton label="Ищем экранизации" />;
  }
  if (!data) {
    return null;
  }
  if (data.enrichment_status === 'pending' && data.items.length === 0) {
    return <PendingSkeleton label="Ищем экранизации" />;
  }
  if (data.items.length === 0) {
    // done + ничего нет — секцию скрываем. Это самый частый кейс.
    return null;
  }

  return (
    <section className="space-y-3" aria-label="Экранизации">
      <h3 className="flex items-center gap-2 text-sm font-medium">
        <Film className="size-4" aria-hidden />
        По этой книге снято
        <span className="text-muted-foreground font-normal">({data.items.length})</span>
      </h3>
      {/*
        Горизонтальный список карточек. На мобильном — свайп; на десктопе
        обычный flex с overflow-x. Гэп подобран чтобы 4-5 постеров
        помещались в типичную ширину карточки книги.
      */}
      <ul className="flex gap-3 overflow-x-auto pb-1 -mx-1 px-1">
        {data.items.map((a) => (
          <li key={a.id} className="shrink-0 w-32 sm:w-36">
            <AdaptationCard a={a} />
          </li>
        ))}
      </ul>
    </section>
  );
}

function PendingSkeleton({ label }: { label: string }) {
  return (
    <section className="space-y-3" aria-busy="true" aria-label={label}>
      <h3 className="flex items-center gap-2 text-sm font-medium">
        <Film className="size-4" aria-hidden />
        {label}…
      </h3>
      <div className="flex gap-3 overflow-hidden pb-1 -mx-1 px-1">
        {[0, 1, 2].map((i) => (
          <div key={i} className="shrink-0 w-32 sm:w-36 space-y-2">
            <Skeleton className="aspect-[2/3] w-full rounded-md" />
            <Skeleton className="h-3 w-full" />
            <Skeleton className="h-3 w-2/3" />
          </div>
        ))}
      </div>
    </section>
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
