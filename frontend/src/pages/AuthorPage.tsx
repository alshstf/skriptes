import { Link, useParams } from '@tanstack/react-router';
import { BarChart3, BookOpen } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import { BookListItem } from '@/components/BookListItem';
import { BackButton } from '@/components/BackButton';
import { FavoriteButton } from '@/components/FavoriteButton';
import { YearHistogram } from '@/components/YearHistogram';
import { ReadingProgress } from '@/components/ReadingProgress';
import { useAuthor, type Author, type SeriesWithCount } from '@/lib/catalog';
import { type BookListItem as BookListItemType } from '@/lib/books';
import { ApiError } from '@/lib/api';

export function AuthorPage() {
  const { id } = useParams({ strict: false }) as { id: string };
  const { data: a, isLoading, error } = useAuthor(id);

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

  return (
    <article className="space-y-6">
      <BackButton />
      <header className="space-y-2">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h1 className="text-2xl font-semibold tracking-tight">{a.full_name}</h1>
          <FavoriteButton target="author" id={a.id} isFavorite={a.is_favorite ?? false} />
        </div>
        <p className="text-sm text-muted-foreground">
          {a.book_count} {pluralBooks(a.book_count)} в каталоге
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
      </header>

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
  // Внутри серии сортируем по ser_no, NULL'ы в конец.
  for (const arr of bySeries.values()) {
    arr.sort((x, y) => {
      const xn = x.ser_no ?? Number.POSITIVE_INFINITY;
      const yn = y.ser_no ?? Number.POSITIVE_INFINITY;
      return xn - yn;
    });
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
          <ul className="space-y-1">
            {books.map((b) => (
              <li key={b.id}>
                <BookListItem book={b} showSeries={false} showSerNo={true} />
              </li>
            ))}
          </ul>
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
        <ul className="space-y-1">
          {books.map((b) => (
            <li key={b.id}>
              <BookListItem book={b} showSeries={false} />
            </li>
          ))}
        </ul>
      </CardContent>
    </Card>
  );
}

// AuthorStats — блок "Статистика" над списком серий.
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
              Добавлено по годам
            </div>
            <YearHistogram data={years} />
          </div>
        ) : null}
      </CardContent>
    </Card>
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
