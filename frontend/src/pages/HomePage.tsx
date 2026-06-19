import { useEffect, useRef, useState } from 'react';
import { Link, useNavigate } from '@tanstack/react-router';
import { BookIcon, LayersIcon, Search, Sparkles, Star, UserIcon } from 'lucide-react';
import { Input } from '@/components/ui/input';
import { Callout } from '@/components/ui/callout';
import { BookCover } from '@/components/BookCover';
import { useDebouncedValue } from '@/lib/useDebouncedValue';
import { useSuggest } from '@/lib/suggest';
import { useHeroSearch } from '@/lib/heroSearch';
import { useContinueReading, useSubscriptionFeed } from '@/lib/home';
import type { ContinueItem, FeedItem } from '@/lib/home';
import { cn } from '@/lib/utils';

/**
 * HomePage — новая Главная (`/`).
 *
 * Доминанта — крупный hero-поиск по центру с живым дропдауном результатов
 * (тот же толерантный typeahead, что и в Cmd+K, через useSuggest). Enter или
 * «Показать все результаты» уводят на /books?q=… (там полноценный список).
 *
 * Ниже — динамические блоки: «Продолжить чтение» (книги в процессе) и
 * «Новинки по подпискам». Плюс две заглушки «Скоро» (оценки и
 * рекомендации) — без бэкенда.
 *
 * Стиль монохромный (грабля №9): акценты — иконкой и насыщенностью, не цветом.
 */
export function HomePage() {
  return (
    <div className="mx-auto w-full max-w-5xl space-y-10 px-1 py-6 sm:py-10">
      <HeroSearch />
      <ContinueReadingRow />
      <SubscriptionFeedRow />
      <ComingSoonRow />
    </div>
  );
}

// ── Hero-поиск ───────────────────────────────────────────────────

function HeroSearch() {
  const [query, setQuery] = useState('');
  const debounced = useDebouncedValue(query, 150);
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const blurTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const { setHeroSearchVisible } = useHeroSearch();
  const heroRef = useRef<HTMLDivElement>(null);

  const { data, isFetching } = useSuggest(debounced, 5);

  // Пока hero-инпут на экране — прячем кнопку поиска в хэдере (она дублирует
  // hero). Когда инпут уезжает под sticky-хедер (rootMargin ≈ высота хэдера),
  // observer гасит флаг и кнопка «въезжает». На размонтировании — сброс, чтобы
  // на других страницах кнопка снова была видна.
  useEffect(() => {
    const el = heroRef.current;
    if (!el) return;
    const io = new IntersectionObserver(
      ([entry]) => setHeroSearchVisible(entry.isIntersecting),
      { rootMargin: '-72px 0px 0px 0px' },
    );
    io.observe(el);
    return () => {
      io.disconnect();
      setHeroSearchVisible(false);
    };
  }, [setHeroSearchVisible]);

  const trimmed = debounced.trim();
  const showDropdown = open && trimmed.length >= 2;
  const hasAny =
    (data?.books?.length ?? 0) + (data?.authors?.length ?? 0) + (data?.series?.length ?? 0) > 0;

  // submitSearch — увести на полноценный список книг (страница /books читает ?q).
  function submitSearch(q: string) {
    const v = q.trim();
    if (!v) return;
    setOpen(false);
    void navigate({ to: '/books', search: { q: v } });
  }

  function go(path: string) {
    setOpen(false);
    void navigate({ to: path });
  }

  return (
    <section className="flex min-h-[42vh] flex-col justify-center space-y-5 pb-2 pt-4 text-center sm:block sm:min-h-0 sm:space-y-4 sm:pb-0 sm:pt-8">
      <h1 className="text-3xl font-semibold tracking-tight">Библиотека Skriptes</h1>
      <div ref={heroRef} className="relative mx-auto w-full max-w-2xl text-left">
        <form
          onSubmit={(e) => {
            e.preventDefault();
            submitSearch(query);
          }}
        >
          <Search
            className="pointer-events-none absolute left-3 top-1/2 size-5 -translate-y-1/2 text-muted-foreground"
            aria-hidden
          />
          <Input
            value={query}
            onChange={(e) => {
              setQuery(e.target.value);
              setOpen(true);
            }}
            onFocus={() => setOpen(true)}
            onBlur={() => {
              // Откладываем закрытие, чтобы клик по элементу дропдауна успел
              // отработать (иначе blur снимает дропдаун раньше onClick).
              blurTimer.current = setTimeout(() => setOpen(false), 150);
            }}
            placeholder="Поиск книг, авторов, серий…"
            aria-label="Поиск книг, авторов, серий"
            autoComplete="off"
            autoCorrect="off"
            autoCapitalize="none"
            spellCheck={false}
            inputMode="search"
            enterKeyHint="search"
            className="h-14 pl-11 pr-4 text-base shadow-sm sm:h-12"
          />
        </form>

        {showDropdown ? (
          <div
            className="absolute z-20 mt-2 w-full overflow-hidden rounded-md border border-border bg-popover shadow-lg"
            // Держим фокус: mousedown по дропдауну не должен закрывать его раньше клика.
            onMouseDown={(e) => {
              e.preventDefault();
              if (blurTimer.current) clearTimeout(blurTimer.current);
            }}
            aria-busy={isFetching || undefined}
          >
            <div className="max-h-[60vh] overflow-y-auto py-1">
              {!hasAny ? (
                <p className="px-3 py-6 text-center text-sm text-muted-foreground">
                  {isFetching ? 'Поиск…' : 'Ничего не найдено'}
                </p>
              ) : null}

              {(data?.books?.length ?? 0) > 0 ? (
                <SuggestGroup heading="Книги">
                  {data!.books.map((b) => (
                    <SuggestRow
                      key={`b-${b.id}`}
                      icon={<BookIcon className="size-4 shrink-0 text-muted-foreground" aria-hidden />}
                      title={b.title}
                      subtitle={[
                        b.authors?.join(', ') || '',
                        b.year ? String(b.year) : '',
                        b.series || '',
                      ]
                        .filter(Boolean)
                        .join(' · ')}
                      favorite={!!b.is_favorite}
                      onClick={() => go(`/works/${b.work_id ?? b.id}`)}
                    />
                  ))}
                </SuggestGroup>
              ) : null}

              {(data?.authors?.length ?? 0) > 0 ? (
                <SuggestGroup heading="Авторы">
                  {data!.authors.map((a) => (
                    <SuggestRow
                      key={`a-${a.id}`}
                      icon={<UserIcon className="size-4 shrink-0 text-muted-foreground" aria-hidden />}
                      title={a.full_name}
                      subtitle={`${a.book_count} ${pluralBooks(a.book_count)}`}
                      favorite={!!a.is_favorite}
                      onClick={() => go(`/authors/${a.id}`)}
                    />
                  ))}
                </SuggestGroup>
              ) : null}

              {(data?.series?.length ?? 0) > 0 ? (
                <SuggestGroup heading="Серии">
                  {data!.series.map((s) => (
                    <SuggestRow
                      key={`s-${s.id}`}
                      icon={<LayersIcon className="size-4 shrink-0 text-muted-foreground" aria-hidden />}
                      title={s.title}
                      subtitle={`${s.author_name ? `${s.author_name} · ` : ''}${s.book_count} ${pluralBooks(s.book_count)}`}
                      favorite={!!s.is_favorite}
                      onClick={() => go(`/series/${s.id}`)}
                    />
                  ))}
                </SuggestGroup>
              ) : null}

              {hasAny ? (
                <button
                  type="button"
                  onClick={() => submitSearch(query)}
                  className="mt-1 flex w-full items-center gap-2 border-t border-border px-3 py-2.5 text-left text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
                >
                  <Search className="size-4 shrink-0" aria-hidden />
                  Показать все результаты по «{trimmed}»
                </button>
              ) : null}
            </div>
          </div>
        ) : null}
      </div>
    </section>
  );
}

function SuggestGroup({ heading, children }: { heading: string; children: React.ReactNode }) {
  return (
    <div className="py-1">
      <p className="px-3 py-1 text-xs font-medium text-muted-foreground">{heading}</p>
      {children}
    </div>
  );
}

function SuggestRow({
  icon,
  title,
  subtitle,
  favorite,
  onClick,
}: {
  icon: React.ReactNode;
  title: string;
  subtitle?: string;
  favorite?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="flex w-full items-center gap-2 px-3 py-2 text-left transition-colors hover:bg-accent hover:text-accent-foreground"
    >
      {icon}
      <span className="flex min-w-0 flex-1 flex-col">
        <span className="truncate text-sm">{title}</span>
        {subtitle ? <span className="truncate text-xs text-muted-foreground">{subtitle}</span> : null}
      </span>
      {favorite ? (
        <Star className="ml-2 size-3.5 shrink-0 fill-yellow-500 stroke-yellow-500" aria-label="В избранном" />
      ) : null}
    </button>
  );
}

// ── «Продолжить чтение» ──────────────────────────────────────────

function ContinueReadingRow() {
  const { data, isLoading } = useContinueReading(12);

  // Пустой блок (нечего продолжать) — скрываем целиком, как в задаче.
  if (isLoading) return <ShelfSkeleton title="Продолжить чтение" />;
  if (!data || data.length === 0) return null;

  return (
    <Shelf title="Продолжить чтение">
      {data.map((it) => (
        <ContinueCard key={`cr-${it.id}`} item={it} />
      ))}
    </Shelf>
  );
}

function ContinueCard({ item }: { item: ContinueItem }) {
  const pct = Math.round(Math.min(1, Math.max(0, item.fraction)) * 100);
  return (
    <CoverCard
      to={`/works/${item.work_id ?? item.id}`}
      title={item.title}
      authors={item.authors}
      coverPath={item.cover_path}
      coverEditionId={item.id}
    >
      <div className="mt-1 space-y-0.5">
        <div className="h-1 w-full overflow-hidden rounded-full bg-muted" aria-hidden>
          <div className="h-full bg-foreground/70" style={{ width: `${pct}%` }} />
        </div>
        <p className="text-xs tabular-nums text-muted-foreground">{pct}%</p>
      </div>
    </CoverCard>
  );
}

// ── «Новинки по подпискам» ─────────────────────────────

function SubscriptionFeedRow() {
  const { data, isLoading } = useSubscriptionFeed(12);

  if (isLoading) return <ShelfSkeleton title="Новинки по подпискам" />;

  // Нет подписок ИЛИ у подписанных авторов пока нет книг → аккуратный пустой стейт.
  if (!data || data.length === 0) {
    return (
      <section className="space-y-3">
        <h2 className="text-lg font-semibold tracking-tight">Новинки по подпискам</h2>
        <Callout icon={<Star className="mt-0.5 size-3.5 shrink-0" aria-hidden />}>
          Добавляйте авторов и серии в избранное, чтобы видеть их новинки. Звезда есть на карточке
          любого автора и любой серии.
        </Callout>
      </section>
    );
  }

  return (
    <Shelf title="Новинки по подпискам">
      {data.map((it) => (
        <FeedCard key={`feed-${it.id}`} item={it} />
      ))}
    </Shelf>
  );
}

function FeedCard({ item }: { item: FeedItem }) {
  return (
    <CoverCard
      to={`/works/${item.work_id ?? item.id}`}
      title={item.title}
      authors={item.authors}
      coverPath={item.cover_path}
      coverEditionId={item.id}
    >
      {item.series ? (
        <p className="mt-0.5 truncate text-xs text-muted-foreground">{item.series}</p>
      ) : null}
    </CoverCard>
  );
}

// ── Заглушки «Скоро» ─────────────────────────────────────────────

function ComingSoonRow() {
  return (
    <section className="grid gap-3 sm:grid-cols-2">
      <ComingSoonCard
        icon={<Star className="size-4 shrink-0" aria-hidden />}
        title="Оцените прочитанное"
        text="Скоро здесь можно будет ставить оценки книгам, которые вы уже прочитали."
      />
      <ComingSoonCard
        icon={<Sparkles className="size-4 shrink-0" aria-hidden />}
        title="Рекомендации"
        text="Скоро здесь появятся персональные подборки на основе ваших оценок и истории."
      />
    </section>
  );
}

function ComingSoonCard({
  icon,
  title,
  text,
}: {
  icon: React.ReactNode;
  title: string;
  text: string;
}) {
  return (
    <div className="rounded-xl border border-border bg-card p-4">
      <div className="flex items-center gap-2 text-sm font-medium">
        {icon}
        <span>{title}</span>
        <span className="ml-auto rounded-full border border-border bg-muted px-2 py-0.5 text-xs text-muted-foreground">
          Скоро
        </span>
      </div>
      <p className="mt-2 text-sm text-pretty text-muted-foreground">{text}</p>
    </div>
  );
}

// ── Общие примитивы полки ────────────────────────────────────────

// Shelf — горизонтальный ряд карточек с заголовком. Прокрутка по X, чтобы
// длинная полка не ломала layout.
function Shelf({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="space-y-3">
      <h2 className="text-lg font-semibold tracking-tight">{title}</h2>
      <div className="flex gap-4 overflow-x-auto pb-2">{children}</div>
    </section>
  );
}

// CoverCard — карточка-обложка фиксированной ширины для горизонтальной полки.
// Обложка через общий BookCover (cover_path → /api/covers/{path}; иначе
// on-demand /api/covers/book/{editionId}). Вся карточка — ссылка на карточку книги.
function CoverCard({
  to,
  title,
  authors,
  coverPath,
  coverEditionId,
  children,
}: {
  to: string;
  title: string;
  authors: string[];
  coverPath?: string;
  coverEditionId: number;
  children?: React.ReactNode;
}) {
  return (
    <Link
      to={to}
      className={cn(
        'group flex w-28 shrink-0 flex-col gap-1.5 rounded-md p-1 transition-colors hover:bg-accent/40 sm:w-32',
        'focus-visible:outline-2 focus-visible:outline-ring',
      )}
    >
      <BookCover
        coverPath={coverPath}
        src={coverPath ? undefined : `/api/covers/book/${coverEditionId}`}
        title={title}
        placeholder="monogram"
        className="w-full"
      />
      <p className="line-clamp-2 text-sm font-medium leading-snug">{title}</p>
      {authors.length > 0 ? (
        <p className="line-clamp-1 text-xs text-muted-foreground">{authors.join(', ')}</p>
      ) : null}
      {children}
    </Link>
  );
}

function ShelfSkeleton({ title }: { title: string }) {
  return (
    <section className="space-y-3">
      <h2 className="text-lg font-semibold tracking-tight">{title}</h2>
      <div className="flex gap-4 overflow-hidden pb-2">
        {Array.from({ length: 6 }).map((_, i) => (
          <div key={i} className="flex w-28 shrink-0 flex-col gap-1.5 p-1 sm:w-32">
            <div className="aspect-[2/3] w-full animate-pulse rounded-md bg-accent" />
            <div className="h-4 w-full animate-pulse rounded bg-accent" />
            <div className="h-3 w-2/3 animate-pulse rounded bg-accent" />
          </div>
        ))}
      </div>
    </section>
  );
}

function pluralBooks(n: number): string {
  // Простой русский плюрал: 1 книга / 2-4 книги / 5+ книг (11-14 — исключение).
  const last2 = n % 100;
  const last1 = n % 10;
  if (last2 >= 11 && last2 <= 14) return 'книг';
  if (last1 === 1) return 'книга';
  if (last1 >= 2 && last1 <= 4) return 'книги';
  return 'книг';
}
