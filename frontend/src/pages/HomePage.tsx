import { useEffect, useRef, useState } from 'react';
import { Link, useNavigate } from '@tanstack/react-router';
import { Bell, BookIcon, LayersIcon, Search, Sparkles, Star, UserIcon } from 'lucide-react';
import { Input } from '@/components/ui/input';
import { Callout } from '@/components/ui/callout';
import { BookCover } from '@/components/BookCover';
import { useDebouncedValue } from '@/lib/useDebouncedValue';
import { useSuggest } from '@/lib/suggest';
import { useHeroSearch } from '@/lib/heroSearch';
import { useInstanceName } from '@/lib/version';
import { useRecSection, workIdOf } from '@/lib/recs';
import type { RecItem, RecSectionKind } from '@/lib/recs';
import { useRatePrompt } from '@/lib/ratings';
import { RatingControl } from '@/components/RatingControl';
import { RatingPromptMenu } from '@/components/RatingPromptMenu';
import { RecDismissMenu } from '@/components/RecDismissMenu';
import { RecReasons } from '@/components/RecReason';
import { CoverCard, SHELF_CARD_WIDTH, Shelf, ShelfSkeleton } from '@/components/Shelf';
import { cn } from '@/lib/utils';
import { pluralBooks } from '@/lib/format';

/**
 * HomePage — новая Главная (`/`).
 *
 * Доминанта — крупный hero-поиск по центру с живым дропдауном результатов
 * (тот же толерантный typeahead, что и в Cmd+K, через useSuggest). Enter или
 * «Показать все результаты» уводят на /books?q=… (там полноценный список).
 *
 * Ниже — лента рекомендаций (lib/recs.ts, один контракт на все секции, у
 * каждой карточки — причина «почему это здесь»): «Продолжить чтение»,
 * «Новинки по подпискам», «Оцените прочитанное». Порядок секций — здесь.
 * Плюс заглушка «Скоро» (персональные подборки — фаза 2 плана recsys).
 *
 * Стиль монохромный (грабля №9): акценты — иконкой и насыщенностью, не цветом.
 */
export function HomePage() {
  return (
    <div className="mx-auto w-full max-w-5xl space-y-10 px-1 py-6 sm:py-10">
      <HeroSearch />
      <ContinueReadingRow />
      <SubscriptionFeedRow />
      <RateablePromptsRow />
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
  const instanceName = useInstanceName();

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
      <h1 className="text-3xl font-semibold tracking-tight">{instanceName}</h1>
      {/* Скрим: пока открыт дропдаун — затемняем страницу, чтобы результаты
          читались как отдельный слой, а не сливались с карточками под ними.
          Инпут и дропдаун выше скрима (wrapper z-40), скрим — z-30. */}
      {showDropdown ? (
        <div
          className="fixed inset-0 z-30 bg-background/70 backdrop-blur-sm"
          aria-hidden
          onMouseDown={() => setOpen(false)}
        />
      ) : null}
      {/* z-40 ТОЛЬКО когда открыт дропдаун: тогда нужно быть выше скрима (z-30) и
          ниже списка (z-50). Без условия `relative z-40` создаёт stacking-контекст
          ВЫШЕ хэдера (z-20) → при скролле hero-инпут просвечивал поверх хэдера, в
          отличие от остального контента, который уезжает под него (грабля №18). */}
      <div
        ref={heroRef}
        className={cn(
          'relative mx-auto w-full max-w-2xl text-left',
          showDropdown && 'z-40',
        )}
      >
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
            className="absolute z-50 mt-2 w-full overflow-hidden rounded-md border border-border bg-popover shadow-xl"
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
                      subtitle={`${a.note ? `${a.note} · ` : ''}${a.book_count} ${pluralBooks(a.book_count)}`}
                      favorite={!!a.is_favorite}
                      favoriteKind="sub"
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
                      favoriteKind="sub"
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
  favoriteKind = 'book',
  onClick,
}: {
  icon: React.ReactNode;
  title: string;
  subtitle?: string;
  favorite?: boolean;
  // Книга — ★ «в избранном»; автор/серия — колокольчик «подписан».
  favoriteKind?: 'book' | 'sub';
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
        favoriteKind === 'sub' ? (
          <Bell className="ml-2 size-3.5 shrink-0 fill-foreground" aria-label="Подписан" />
        ) : (
          <Star className="ml-2 size-3.5 shrink-0 fill-yellow-500 stroke-yellow-500" aria-label="В избранном" />
        )
      ) : null}
    </button>
  );
}

// ── Секции ленты рекомендаций ────────────────────────────────────

// RecShelf — секция ленты: запрос по виду, скелетон, пустое состояние (по
// умолчанию секция без карточек не рендерится), карточки через renderCard.
function RecShelf({
  kind,
  title,
  empty,
  renderCard,
}: {
  kind: RecSectionKind;
  title: string;
  empty?: React.ReactNode;
  renderCard: (item: RecItem) => React.ReactNode;
}) {
  const { data, isLoading } = useRecSection(kind, 12);
  if (isLoading) return <ShelfSkeleton title={title} />;
  if (!data || data.length === 0) return empty ?? null;
  return (
    <Shelf title={title}>
      {data.map((it) => (
        <div key={`${kind}-${it.id}`} className="contents">
          {renderCard(it)}
        </div>
      ))}
    </Shelf>
  );
}

// ── «Продолжить чтение» ──────────────────────────────────────────

function ContinueReadingRow() {
  return (
    <RecShelf
      kind="continue_reading"
      title="Продолжить чтение"
      renderCard={(it) => <ContinueCard item={it} />}
    />
  );
}

function ContinueCard({ item }: { item: RecItem }) {
  const progress = item.reasons.find((r) => r.kind === 'in_progress');
  const pct = Math.round(Math.min(1, Math.max(0, progress?.fraction ?? 0)) * 100);
  return (
    <CoverCard
      to={`/works/${workIdOf(item)}`}
      title={item.title}
      authors={item.authors}
      coverPath={item.cover_path}
      coverEditionId={item.id}
    >
      <div className="mt-1 space-y-0.5">
        <div className="h-1 w-full overflow-hidden rounded-full bg-muted" aria-hidden>
          <div className="h-full bg-foreground/70" style={{ width: `${pct}%` }} />
        </div>
        <RecReasons reasons={item.reasons} />
      </div>
    </CoverCard>
  );
}

// ── «Новинки по подпискам» ─────────────────────────────

function SubscriptionFeedRow() {
  return (
    <RecShelf
      kind="subscriptions"
      title="Новинки по подпискам"
      // Нет подписок ИЛИ у подписанных авторов пока нет книг → аккуратный пустой стейт.
      empty={
        <section className="space-y-3">
          <h2 className="text-lg font-semibold tracking-tight">Новинки по подпискам</h2>
          <Callout icon={<Star className="mt-0.5 size-3.5 shrink-0" aria-hidden />}>
            Добавляйте авторов и серии в избранное, чтобы видеть их новинки. Звезда есть на карточке
            любого автора и любой серии.
          </Callout>
        </section>
      }
      renderCard={(it) => <FeedCard item={it} />}
    />
  );
}

function FeedCard({ item }: { item: RecItem }) {
  return (
    <div className="relative shrink-0">
      {/* Скрыть с причиной: kebab в углу обложки (как у запросов оценки). */}
      <div className="absolute right-1 top-1 z-10">
        <RecDismissMenu workId={workIdOf(item)} />
      </div>
      <CoverCard
        to={`/works/${workIdOf(item)}`}
        title={item.title}
        authors={item.authors}
        coverPath={item.cover_path}
        coverEditionId={item.id}
      >
        {item.series ? (
          <p className="mt-0.5 truncate text-xs text-muted-foreground">{item.series}</p>
        ) : null}
        <RecReasons reasons={item.reasons} className="mt-0.5" />
      </CoverCard>
    </div>
  );
}

// ── «Оцените прочитанное» ────────────────────────────────────────

// RateablePromptsRow — книги, которые юзер вероятно прочитал и ещё не оценил.
// Бэкенд отдаёт пусто, если запросы оценки выключены в профиле → блок скрыт.
function RateablePromptsRow() {
  return (
    <RecShelf
      kind="rate_prompts"
      title="Оцените прочитанное"
      renderCard={(it) => <RateableCard item={it} />}
    />
  );
}

function RateableCard({ item }: { item: RecItem }) {
  const workId = workIdOf(item);
  const rate = useRatePrompt();
  return (
    <div className={cn('group/rate relative flex shrink-0 flex-col gap-1.5', SHELF_CARD_WIDTH)}>
      {/* Kebab поверх обложки: «ещё не прочитал» / «не буду оценивать». */}
      <div className="absolute right-1 top-1 z-10">
        <RatingPromptMenu workId={workId} />
      </div>
      <Link
        to="/works/$id"
        params={{ id: String(workId) }}
        className="group flex flex-col gap-1.5 rounded-md p-1 transition-colors hover:bg-accent/40 focus-visible:outline-2 focus-visible:outline-ring"
      >
        <BookCover
          coverPath={item.cover_path}
          src={`/api/covers/book/${item.id}`}
          title={item.title}
          placeholder="monogram"
          className="w-full"
        />
        <p className="line-clamp-2 text-sm font-medium leading-snug">{item.title}</p>
        {item.authors.length > 0 ? (
          <p className="line-clamp-1 text-xs text-muted-foreground">{item.authors.join(', ')}</p>
        ) : null}
        <RecReasons reasons={item.reasons} />
      </Link>
      {/* Инлайн-оценка: выбрал → книга уходит из ленты (+ авто-«Прочитана»). */}
      <div className="px-1">
        <RatingControl
          value={0}
          disabled={rate.isPending}
          onChange={(n) => {
            if (n) rate.mutate({ workId, rating: n });
          }}
        />
      </div>
    </div>
  );
}

// ── Заглушка «Скоро» ─────────────────────────────────────────────

function ComingSoonRow() {
  return (
    <ComingSoonCard
      icon={<Sparkles className="size-4 shrink-0" aria-hidden />}
      title="Рекомендации"
      text="Скоро здесь появятся персональные подборки на основе ваших оценок и истории."
    />
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
