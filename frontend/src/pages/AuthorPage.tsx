import { Link, useParams } from '@tanstack/react-router';
import { BarChart3, BookHeart, BookOpen, Film, Globe, User as UserIcon } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import { BookListItem } from '@/components/BookListItem';
import { BackButton } from '@/components/BackButton';
import { MergeSuggestions } from '@/components/MergeSuggestions';
import { MergeWorksDialog } from '@/components/MergeWorksDialog';
import { ExpandableText } from '@/components/ExpandableText';
import { FavoriteButton } from '@/components/FavoriteButton';
import { YearHistogram } from '@/components/YearHistogram';
import { ReadingProgress } from '@/components/ReadingProgress';
import { useAuthor, type Author, type SeriesWithCount } from '@/lib/catalog';
import { useLanguageMap } from '@/lib/content';
import { fmtRating, externalRatingSourceLabel } from '@/lib/ratingDisplay';
import { bySeriesOrder, type BookListItem as BookListItemType } from '@/lib/books';
import { ApiError } from '@/lib/api';
import { cn } from '@/lib/utils';

export function AuthorPage() {
  const { id } = useParams({ strict: false }) as { id: string };
  const { data: a, isLoading, error, enrichmentExhausted } = useAuthor(id);
  const langMap = useLanguageMap();

  if (isLoading) return <AuthorSkeleton />;

  if (error) {
    const isNotFound = error instanceof ApiError && error.status === 404;
    return (
      <div className="space-y-3">
        <BackButton />
        <p className="text-sm text-destructive" role="alert">
          {isNotFound ? 'Автор не найден.' : `Не удалось загрузить: ${error.message}`}
        </p>
      </div>
    );
  }

  if (!a) return null;

  // Сводка-зеркало строки списка авторов: годы активности, языки изданий.
  const years = a.years_active
    ? a.years_active.from === a.years_active.to
      ? String(a.years_active.from)
      : `${a.years_active.from}–${a.years_active.to}`
    : null;
  const langNames = (a.languages ?? []).map((c) => langMap.get(c) ?? c);

  return (
    <article className="space-y-6">
      <BackButton />

      {/* Шапка с двухуровневой структурой как у BookDetailPage:
            1. flex-row с фото слева + meta (имя/счётчик/жанры/кнопка) справа.
            2. Био ниже на полную ширину.
          Био на полную ширину специально — bio из Wikipedia часто длинный,
          в узкой правой колонке выглядит неудобно. */}
      <Card>
        <CardContent className="space-y-6">
          <div className="flex flex-col gap-6 md:flex-row md:items-start">
            <AuthorPhoto
              photoPath={a.photo_path}
              fullName={a.full_name}
              className="w-32 sm:w-40 mx-auto md:mx-0"
            />
            <div className="flex flex-col gap-2 flex-1 min-w-0">
              <div className="flex flex-wrap items-start justify-between gap-2">
                <div className="flex items-center gap-1.5 min-w-0">
                  <h1 className="text-2xl font-semibold tracking-tight">{a.full_name}</h1>
                  {a.has_adaptations ? (
                    <Film className="size-4 shrink-0 text-muted-foreground" aria-label="Есть экранизации" />
                  ) : null}
                </div>
                <FavoriteButton target="author" id={a.id} isFavorite={a.is_favorite ?? false} />
              </div>
              {/* Сводка-зеркало строки списка: книги · годы · внешний рейтинг
                  (Globe, источник в тултипе) · оценка читателей (BookHeart). */}
              <p className="flex flex-wrap items-center gap-x-1 text-sm text-muted-foreground tabular-nums">
                <span>
                  {a.book_count} {pluralBooks(a.book_count)} в каталоге
                </span>
                {years ? <span>· {years}</span> : null}
                {a.external_rating != null ? (
                  <span
                    className="inline-flex items-center gap-0.5"
                    aria-label={`Внешний рейтинг ${fmtRating(a.external_rating)} · ${externalRatingSourceLabel(a.external_rating_source)}`}
                    title={`Внешний рейтинг ${fmtRating(a.external_rating)} · ${externalRatingSourceLabel(a.external_rating_source)}`}
                  >
                    · <Globe className="size-3.5 text-muted-foreground" aria-hidden /> {fmtRating(a.external_rating)}
                  </span>
                ) : null}
                {a.reader_rating != null ? (
                  <span
                    className="inline-flex items-center gap-0.5"
                    aria-label={`Оценка читателей ${a.reader_rating.toFixed(1)} (${a.reader_rating_count ?? 0})`}
                  >
                    · <BookHeart className="size-3.5 text-muted-foreground" aria-hidden /> {a.reader_rating.toFixed(1)}
                    {a.reader_rating_count ? (
                      <span className="text-muted-foreground/70"> ({a.reader_rating_count})</span>
                    ) : null}
                  </span>
                ) : null}
              </p>
              {a.top_genres && a.top_genres.length > 0 ? (
                <div className="flex flex-wrap gap-1 pt-1">
                  {a.top_genres.map((g) => (
                    <Badge key={g.code} variant="secondary" className="font-normal">
                      {g.display} · {g.count}
                    </Badge>
                  ))}
                </div>
              ) : null}
              {langNames.length > 0 ? (
                <p className="text-xs text-muted-foreground">{langNames.join(', ')}</p>
              ) : null}
            </div>
          </div>

          <AuthorBio bio={a.bio} enrichmentExhausted={enrichmentExhausted} />
        </CardContent>
      </Card>

      <AuthorStats author={a} />

      <AuthorBooks author={a} />
    </article>
  );
}

/**
 * AuthorBooks — основной блок книг автора с группировкой по сериям.
 *
 * Структура:
 *  - если у автора есть серии — рисуем по карточке на каждую серию,
 *    внутри карточки книги в порядке ser_no;
 *  - книги вне серий идут отдельной карточкой "Вне серий" в самом
 *    низу, но только если они вообще есть И если у автора есть хоть
 *    одна серия (иначе обычным плоским списком — карточка
 *    "Вне серий" у такого автора смотрелась бы абсурдно);
 *  - если у автора нет серий — плоский список без всякой группировки.
 *
 * Порядок секций: серии в порядке, который пришёл с backend (он сейчас
 * по убыванию числа книг), потом "Вне серий".
 */
function AuthorBooks({ author }: { author: Author }) {
  if (author.books.length === 0) {
    return (
      <section>
        <p className="text-sm text-muted-foreground">Книг нет.</p>
      </section>
    );
  }

  const hasSeries = (author.series ?? []).length > 0;

  // Без серий — плоский список как раньше.
  if (!hasSeries) {
    return (
      <section className="space-y-2">
        <h2 className="flex items-center gap-2 text-base font-medium">
          <BookOpen className="size-4" aria-hidden /> Книги
          {author.books_total > author.books.length ? (
            <span className="text-sm font-normal text-muted-foreground">
              (показаны последние {author.books.length} из {author.books_total})
            </span>
          ) : null}
        </h2>
        <ul className="space-y-1">
          {author.books.map((b) => (
            <li key={b.id}>
              <BookListItem book={b} showSeries={false} />
            </li>
          ))}
        </ul>
      </section>
    );
  }

  // С сериями — группируем по series_id.
  const bySeries = new Map<number, BookListItemType[]>();
  const standalone: BookListItemType[] = [];
  for (const b of author.books) {
    if (b.series_id != null) {
      const arr = bySeries.get(b.series_id) ?? [];
      arr.push(b);
      bySeries.set(b.series_id, arr);
    } else {
      standalone.push(b);
    }
  }
  // Внутри серии — по series_order (backend-каскад: ser_no → год → эвристика →
  // date_added). Бэкенд уже отдаёт сортированно, клиентская сортировка устойчива.
  for (const arr of bySeries.values()) {
    arr.sort(bySeriesOrder);
  }

  return (
    <div className="space-y-4">
      {(author.series ?? []).map((s) => (
        <SeriesSection
          key={s.id}
          series={s}
          books={bySeries.get(s.id) ?? []}
        />
      ))}
      {standalone.length > 0 ? <StandaloneSection books={standalone} /> : null}
    </div>
  );
}

function SeriesSection({
  series,
  books,
}: {
  series: SeriesWithCount;
  books: BookListItemType[];
}) {
  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="flex flex-wrap items-center gap-2 text-base">
          {/* Badge "Серия" — явный визуальный маркер типа секции,
              понятнее иконки. Outline + uppercase + tiny font = aside-tag
              стиль, не конкурирует с названием. */}
          <Badge
            variant="outline"
            className="px-1.5 py-0 text-[10px] font-medium uppercase tracking-wider"
          >
            Серия
          </Badge>
          <Link
            to="/series/$id"
            params={{ id: String(series.id) }}
            className="hover:underline"
          >
            {series.title}
          </Link>
          <span className="text-sm font-normal text-muted-foreground tabular-nums">
            {series.count} {pluralBooks(series.count)}
          </span>
        </CardTitle>
      </CardHeader>
      <CardContent className="pt-0">
        {books.length === 0 ? (
          <p className="text-sm text-muted-foreground">Книги не загрузились.</p>
        ) : (
          <div className="space-y-2">
            <MergeSuggestions books={books} />
            <div className="flex justify-end empty:hidden">
              <MergeWorksDialog books={books} />
            </div>
            <ul className="space-y-1">
              {books.map((b) => (
                <li key={b.id}>
                  <BookListItem book={b} showSeries={false} showSerNo={true} />
                </li>
              ))}
            </ul>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function StandaloneSection({ books }: { books: BookListItemType[] }) {
  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-base">
          <BookOpen className="size-4" aria-hidden /> Вне серий
          <span className="text-sm font-normal text-muted-foreground tabular-nums">
            {books.length} {pluralBooks(books.length)}
          </span>
        </CardTitle>
      </CardHeader>
      <CardContent className="pt-0">
        <div className="space-y-2">
          <div className="flex justify-end empty:hidden">
            <MergeWorksDialog books={books} />
          </div>
          <ul className="space-y-1">
            {books.map((b) => (
              <li key={b.id}>
                <BookListItem book={b} showSeries={false} />
              </li>
            ))}
          </ul>
        </div>
      </CardContent>
    </Card>
  );
}

// AuthorStats — блок "Статистика" над списком книг.
// Прячется если ничего показать: нет year_stats и нет downloads.
// Гистограмма скрывается отдельно если в распределении < 2 точек:
// одинокий столбик ничего не сообщает.
function AuthorStats({ author }: { author: import('@/lib/catalog').Author }) {
  const years = author.year_stats ?? [];
  const showHistogram = years.length >= 2;
  const showProgress = (author.read_count ?? 0) > 0 && author.book_count > 0;
  if (!showHistogram && !showProgress) return null;
  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-base">
          <BarChart3 className="size-4" aria-hidden /> Статистика
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-4 pt-0">
        {showProgress ? (
          <ReadingProgress read={author.read_count ?? 0} total={author.book_count} />
        ) : null}
        {showHistogram ? (
          <div className="space-y-1">
            <div className="text-xs font-medium text-muted-foreground uppercase">
              Книги по годам написания
            </div>
            <YearHistogram data={years} />
          </div>
        ) : null}
      </CardContent>
    </Card>
  );
}

/**
 * AuthorPhoto — портрет автора с плейсхолдером.
 *
 * Симметрично BookCover: одинаковая высота/ширина у плейсхолдера и
 * у настоящей картинки, чтобы подмена через polling не сдвигала layout.
 * aspect-[3/4] — типичная пропорция портрета (вертикальный прямоугольник).
 */
function AuthorPhoto({
  photoPath,
  fullName,
  className,
}: {
  photoPath?: string;
  fullName: string;
  className?: string;
}) {
  const base = cn(
    'aspect-[3/4] rounded-md border border-border bg-muted shadow-sm overflow-hidden shrink-0 self-start',
    className,
  );
  if (photoPath) {
    return (
      <img
        src={`/api/covers/${photoPath}`}
        alt={`Фото: ${fullName}`}
        className={cn(base, 'object-cover')}
        loading="lazy"
      />
    );
  }
  return (
    <div
      className={cn(base, 'flex flex-col items-center justify-center gap-2 p-3 text-muted-foreground')}
      role="img"
      aria-label={`Фото: ${fullName} (загружается)`}
    >
      <UserIcon className="size-10 opacity-40" aria-hidden />
      <span className="text-xs line-clamp-3 text-center">{fullName}</span>
    </div>
  );
}

/**
 * AuthorBio — био-блок с теми же тремя состояниями что и AnnotationBlock
 * для книги: текст / скелетон / fallback "Информация отсутствует".
 */
function AuthorBio({
  bio,
  enrichmentExhausted,
}: {
  bio?: string;
  enrichmentExhausted: boolean;
}) {
  return (
    <section className="space-y-2">
      <h3 className="text-sm font-medium text-muted-foreground uppercase tracking-wider">
        Биография
      </h3>
      {bio ? (
        // Wikipedia intro обычно 500-2000 символов — клампим до 5
        // строк, дальше пользователь жмёт "Развернуть".
        <ExpandableText text={bio} lines={5} />
      ) : enrichmentExhausted ? (
        <p className="text-sm italic text-muted-foreground">Информация отсутствует.</p>
      ) : (
        <div className="space-y-2" aria-busy="true" aria-label="Биография загружается">
          <Skeleton className="h-3 w-full" />
          <Skeleton className="h-3 w-[97%]" />
          <Skeleton className="h-3 w-[90%]" />
          <Skeleton className="h-3 w-3/4" />
        </div>
      )}
    </section>
  );
}

function AuthorSkeleton() {
  return (
    <div className="space-y-4">
      <Skeleton className="h-7 w-1/2" />
      <Skeleton className="h-4 w-1/4" />
      <Skeleton className="h-32 w-full" />
    </div>
  );
}

function pluralBooks(n: number): string {
  const mod10 = n % 10;
  const mod100 = n % 100;
  if (mod100 >= 11 && mod100 <= 14) return 'книг';
  if (mod10 === 1) return 'книга';
  if (mod10 >= 2 && mod10 <= 4) return 'книги';
  return 'книг';
}
