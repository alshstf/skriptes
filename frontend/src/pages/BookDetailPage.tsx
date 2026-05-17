import { Link, useParams } from '@tanstack/react-router';
import { BookText, BookOpen } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardTitle } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import { AdaptationsSection } from '@/components/AdaptationsSection';
import { BackButton } from '@/components/BackButton';
import { BookCover } from '@/components/BookCover';
import { DownloadMenu } from '@/components/DownloadMenu';
import { FavoriteButton } from '@/components/FavoriteButton';
import { ReadToggle } from '@/components/ReadToggle';
import { SendToKindleButton } from '@/components/SendToKindleButton';
import { useBook } from '@/lib/books';
import { ApiError } from '@/lib/api';

export function BookDetailPage() {
  const { id } = useParams({ strict: false }) as { id: string };
  const { data: book, isLoading, error, enrichmentExhausted } = useBook(id);

  if (isLoading) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-7 w-2/3" />
        <Skeleton className="h-5 w-1/3" />
        <Skeleton className="h-4 w-1/4" />
      </div>
    );
  }

  if (error) {
    const isNotFound = error instanceof ApiError && error.status === 404;
    return (
      <div className="space-y-3">
        <BackButton />
        <p className="text-sm text-destructive" role="alert">
          {isNotFound ? 'Книга не найдена.' : `Не удалось загрузить карточку: ${error.message}`}
        </p>
      </div>
    );
  }

  if (!book) return null;

  return (
    <article className="space-y-4">
      <BackButton />
      <Card>
        {/*
          Двухуровневая структура:
            1. flex-row: обложка слева + meta справа (на md+).
               На мобильном — обложка сверху, meta под ней.
            2. Аннотация на ПОЛНУЮ ширину карточки, под flex-row.
          Так аннотация начинается с левого края (под обложкой), а не
          плавает только в правой половине под высотой meta-блока.
        */}
        <CardContent className="space-y-6">
          <div className="flex flex-col gap-6 md:flex-row md:items-start">
            <BookCover
              coverPath={book.cover_path}
              title={book.title}
              className="w-32 sm:w-44 md:w-56 mx-auto md:mx-0"
            />

            <div className="flex flex-col gap-4 flex-1 min-w-0">
              {/* Заголовок + авторы + кнопки */}
              <div className="flex flex-wrap items-start justify-between gap-2">
                <div className="space-y-1 flex-1 min-w-0">
                  <CardTitle className="text-2xl tracking-tight">{book.title}</CardTitle>
                  {book.authors.length > 0 ? (
                    <p className="text-base text-muted-foreground">
                      {book.authors.map((a, i) => (
                        <span key={a.id}>
                          {i > 0 ? ', ' : ''}
                          <Link
                            to="/authors/$id"
                            params={{ id: String(a.id) }}
                            className="hover:underline"
                          >
                            {a.full_name}
                          </Link>
                        </span>
                      ))}
                    </p>
                  ) : null}
                </div>
                <div className="flex flex-wrap items-center gap-2 shrink-0">
                  <ReadToggle bookId={book.id} isRead={book.is_read ?? false} />
                  <FavoriteButton
                    target="book"
                    id={book.id}
                    isFavorite={book.is_favorite ?? false}
                  />
                  {!book.deleted ? (
                    <Button asChild variant="secondary" size="sm" className="gap-1">
                      <Link
                        to="/books/$id/read"
                        params={{ id: String(book.id) }}
                        aria-label="Открыть книгу в браузерном ридере"
                      >
                        <BookOpen className="size-4" aria-hidden />
                        <span className="hidden sm:inline">Читать</span>
                      </Link>
                    </Button>
                  ) : null}
                  {!book.deleted ? <SendToKindleButton bookId={book.id} /> : null}
                  {!book.deleted ? <DownloadMenu bookId={book.id} /> : null}
                </div>
              </div>

              {/* Серия / жанры / meta */}
              {book.series ? (
                <div className="text-sm">
                  <span className="text-muted-foreground">Серия:</span>{' '}
                  <Link
                    to="/series/$id"
                    params={{ id: String(book.series.id) }}
                    className="hover:underline"
                  >
                    {book.series.title}
                  </Link>
                  {book.ser_no ? (
                    <span className="text-muted-foreground"> · #{book.ser_no}</span>
                  ) : null}
                </div>
              ) : null}

              {book.genres.length > 0 ? (
                <div className="flex flex-wrap gap-1">
                  {book.genres.map((g) => (
                    <Badge key={g.id} variant="secondary" className="font-normal">
                      {g.display}
                    </Badge>
                  ))}
                </div>
              ) : null}

              <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 text-sm">
                <Field label="Файл" value={`${book.file_name}.${book.ext}`} mono />
                <Field label="Архив" value={book.archive} mono />
                <Field label="Размер" value={`${(book.size_bytes / 1024).toFixed(1)} KiB`} />
                {book.lang ? <Field label="Язык" value={book.lang} /> : null}
                {book.date_added ? <Field label="Добавлена" value={book.date_added} /> : null}
                {book.rating !== undefined ? (
                  <Field label="Рейтинг" value={String(book.rating)} />
                ) : null}
                <Field label="LIBID" value={book.lib_id} mono />
              </dl>

              {book.deleted ? (
                <p className="rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive">
                  В источнике помечена удалённой (DEL=1).
                </p>
              ) : null}
            </div>
          </div>

          <AnnotationBlock
            annotation={book.annotation}
            enrichmentExhausted={enrichmentExhausted}
          />

          {/*
            Экранизации — отдельная секция под аннотацией. Не рендерится
            пока enrichment'у нечего показать (см. AdaptationsSection):
            для большинства книг экранизаций просто нет, навязывать
            "Экранизаций не найдено" — лишний шум.
          */}
          {!book.deleted ? <AdaptationsSection bookId={book.id} /> : null}
        </CardContent>
      </Card>
    </article>
  );
}

/**
 * AnnotationBlock — секция "Аннотация" на странице книги.
 *
 * Три состояния:
 *  1. annotation есть → plain-text с whitespace-pre-line (параграфы из
 *     fb2 разделены \n\n, pre-line их сохраняет).
 *  2. annotation нет, polling в useBook ещё активен → скелетон с
 *     aria-busy. Высота примерно как у текста чтобы layout не прыгал.
 *  3. annotation нет И enrichmentExhausted (polling сдался после ~10
 *     ретраев) → "Описание отсутствует" вместо вечного скелетона. Это
 *     случай книги без <annotation> в fb2 и без записи в OL/GB —
 *     например короткое неоднозначное название не находится в источниках.
 */
function AnnotationBlock({
  annotation,
  enrichmentExhausted,
}: {
  annotation?: string;
  enrichmentExhausted: boolean;
}) {
  return (
    <section className="space-y-2">
      <h3 className="flex items-center gap-2 text-sm font-medium">
        <BookText className="size-4" aria-hidden />
        Аннотация
      </h3>
      {annotation ? (
        <p className="whitespace-pre-line text-sm text-muted-foreground">{annotation}</p>
      ) : enrichmentExhausted ? (
        <p className="text-sm italic text-muted-foreground">Описание отсутствует.</p>
      ) : (
        <div className="space-y-2" aria-busy="true" aria-label="Аннотация загружается">
          <Skeleton className="h-3 w-full" />
          <Skeleton className="h-3 w-[95%]" />
          <Skeleton className="h-3 w-[88%]" />
          <Skeleton className="h-3 w-3/4" />
        </div>
      )}
    </section>
  );
}

function Field({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className={mono ? 'font-mono text-xs break-all' : ''}>{value}</dd>
    </>
  );
}
