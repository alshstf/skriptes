import { Link, useParams } from '@tanstack/react-router';
import { BookText, BookOpen, BookHeart, Check, X, Library, Globe } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardTitle } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import { AdaptationsSection } from '@/components/AdaptationsSection';
import { AddToShelfDialog } from '@/components/AddToShelfDialog';
import { BackButton } from '@/components/BackButton';
import { BookCover } from '@/components/BookCover';
import { DownloadMenu } from '@/components/DownloadMenu';
import { EditionRow } from '@/components/EditionRow';
import { SplitEditionsDialog } from '@/components/SplitEditionsDialog';
import { MergeIntoWorkDialog } from '@/components/MergeIntoWorkDialog';
import { FavoriteButton } from '@/components/FavoriteButton';
import { RatingControl } from '@/components/RatingControl';
import { useRateBook } from '@/lib/ratings';
import { SendToKindleButton } from '@/components/SendToKindleButton';
import { useBookCard, useToggleRead, type Book } from '@/lib/books';
import { useBookCollections } from '@/lib/collections';
import { ApiError } from '@/lib/api';

/**
 * BookDetailPage — карточка логической книги. Один компонент для двух маршрутов:
 *  - /works/$id (mode='work', основной — ссылки из списков ведут сюда);
 *  - /books/$id (mode='book', обратная совместимость: открывает по id издания,
 *    например возврат из ридера). Оба отдают тот же DTO; top-level — открытое/
 *    представительное издание. book.id (а не id из URL) — цель действий.
 */
export function BookDetailPage({ mode = 'book' }: { mode?: 'book' | 'work' }) {
  const { id } = useParams({ strict: false }) as { id: string };
  const { data: book, isLoading, error, enrichmentExhausted } = useBookCard(id, mode);

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

  const editions = book.editions ?? [];
  // Несколько изданий → действия (Читать/Скачать/Kindle) переезжают в секцию
  // «Издания» (на каждое издание своё). Одно издание → классический вид: всё в
  // шапке, секцию не показываем (избегаем избыточной таблицы из одной строки).
  const multi = editions.length > 1;

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
              {/* Заголовок + авторы + кнопки в один ряд. Действия на мобиле —
                  только иконки (у Kindle/Скачать текст hidden sm:inline), поэтому
                  ряд компактный и помещается без переноса на новую строку. */}
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
                <div className="flex flex-wrap items-center gap-2">
                  <FavoriteButton
                    target="book"
                    id={book.id}
                    isFavorite={book.is_favorite ?? false}
                  />
                  {/* «На полку» переехала вниз, в блок полок под мета (ShelfSection):
                      и сам контрол, и список полок — в одном месте. */}
                  {/* Действия для одного издания — в шапке; при нескольких изданиях
                      они в секции «Издания» (на каждое издание свои). */}
                  {!multi && !book.deleted ? (
                    <>
                      <Button asChild variant="secondary" size="sm" className="gap-1">
                        <Link
                          to="/books/$id/read"
                          params={{ id: String(book.id) }}
                          aria-label="Открыть книгу в браузерном ридере"
                        >
                          <BookOpen className="size-4" aria-hidden />
                          <span className="hidden sm:inline">{readButtonLabel(book.reading_fraction)}</span>
                        </Link>
                      </Button>
                      <SendToKindleButton bookId={book.id} />
                      <DownloadMenu bookId={book.id} />
                    </>
                  ) : null}
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
                {/* Поля уровня файла показываем в шапке только для одного издания;
                    при нескольких они в строках секции «Издания». */}
                {!multi ? (
                  <>
                    <Field label="Файл" value={`${book.file_name}.${book.ext}`} mono />
                    <Field label="Архив" value={book.archive} mono />
                    <Field label="Размер" value={`${(book.size_bytes / 1024).toFixed(1)} KiB`} />
                    {book.lang ? <Field label="Язык" value={book.lang} /> : null}
                  </>
                ) : null}
                {book.written_year ? (
                  <Field label="Год написания" value={String(book.written_year)} />
                ) : null}
                {!multi && book.edition_year ? (
                  <Field label="Год издания" value={String(book.edition_year)} />
                ) : null}
                {book.date_added ? (
                  <Field label="Добавлена" value={formatReadDate(book.date_added)} />
                ) : null}
                <ExternalRatingField book={book} />
                <ReadStatusField
                  bookId={book.id}
                  isRead={book.is_read ?? false}
                  readAt={book.read_at}
                />
                {!multi ? <Field label="LIBID" value={book.lib_id} mono /> : null}
              </dl>

              {!book.deleted ? (
                <RatingsBlock book={book} cardKey={[mode === 'work' ? 'work' : 'book', String(id)]} />
              ) : null}

              <ShelfSection bookId={book.id} deleted={book.deleted ?? false} />

              {book.deleted ? (
                <p className="rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive">
                  В источнике помечена удалённой (DEL=1).
                </p>
              ) : null}
            </div>
          </div>

          {/* Админ: присоединить другую книгу (дубль вне общей серии/автора).
              Сам скрыт у не-админа. */}
          <div className="flex justify-end empty:hidden">
            <MergeIntoWorkDialog workId={book.work_id ?? 0} workTitle={book.title} />
          </div>

          {multi ? <EditionsSection book={book} /> : null}

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
 * EditionsSection — секция «Издания»: одна логическая книга = несколько
 * fb2-файлов (разные переводы / годы издания / языки). Плоский список
 * равноправных изданий — на каждое строка с атрибутами и собственными
 * действиями (Читать/Скачать/Kindle). Рендерится только при ≥2 изданиях
 * (для одного действия остаются в шапке).
 */
function EditionsSection({ book }: { book: Book }) {
  const editions = book.editions ?? [];
  return (
    <section className="space-y-2">
      <div className="flex items-center justify-between gap-2">
        <h3 className="flex items-center gap-2 text-sm font-medium">
          <Library className="size-4" aria-hidden />
          Издания
          <span className="text-xs font-normal text-muted-foreground tabular-nums">
            {editions.length}
          </span>
        </h3>
        {/* Админ: вынести ошибочно слитые издания в отдельную книгу (сам скрыт у не-админа). */}
        <SplitEditionsDialog editions={editions} workTitle={book.title} />
      </div>
      <ul className="space-y-2">
        {editions.map((e) => (
          <EditionRow key={e.id} edition={e} workTitle={book.title} />
        ))}
      </ul>
    </section>
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

// fmtRating — LIBRATE целочисленный, web-рейтинг дробный: целые без хвоста, иначе 1 знак.
function fmtRating(v: number): string {
  return Number.isInteger(v) ? String(v) : v.toFixed(1);
}

// externalSourceLabel — человекочитаемый источник внешнего рейтинга.
function externalSourceLabel(source?: string): string {
  switch (source) {
    case 'google_books':
      return 'Google Books';
    case 'openlibrary':
      return 'OpenLibrary';
    default:
      return 'внешний источник';
  }
}

// externalRatingDisplay — единый «Внешний рейтинг» с атрибуцией источника.
// Приоритет: LIBRATE (донорская библиотека) → web (Google Books/OpenLibrary).
function externalRatingDisplay(book: Book): { value: string; source: string } | null {
  if (book.rating != null) return { value: String(book.rating), source: 'библиотека' };
  if (book.external_rating != null) {
    return { value: fmtRating(book.external_rating), source: externalSourceLabel(book.external_rating_source) };
  }
  return null;
}

/**
 * ExternalRatingField — поле «Внешний рейтинг» в карточке (Globe + значение +
 * источник). Объединяет LIBRATE и web-рейтинг; не показывается, если внешнего
 * рейтинга нет. Оценки читателей (RatingsBlock, BookHeart) — отдельно.
 */
function ExternalRatingField({ book }: { book: Book }) {
  const ext = externalRatingDisplay(book);
  if (!ext) return null;
  return (
    <>
      <dt className="text-muted-foreground">Внешний рейтинг</dt>
      <dd className="flex items-center gap-1.5">
        <Globe className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
        <span>
          {ext.value}
          <span className="text-muted-foreground"> · {ext.source}</span>
        </span>
      </dd>
    </>
  );
}

/**
 * RatingsBlock — «Оценки читателей»: моя оценка (интерактивные звёзды, work-level)
 * + средняя по инстансу. Отдельно от «Внешнего рейтинга» (LIBRATE ∪ web). cardKey —
 * ключ кэша открытой карточки для оптимистичного обновления.
 */
function RatingsBlock({ book, cardKey }: { book: Book; cardKey: (string | number)[] }) {
  const rate = useRateBook(book.work_id ?? book.id, cardKey);
  const value = book.user_rating ?? 0;
  const avg = book.rating_avg;
  const count = book.rating_count ?? 0;
  return (
    <div className="space-y-1.5">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <span className="text-sm text-muted-foreground">Ваша оценка:</span>
        <RatingControl value={value} disabled={rate.isPending} onChange={(n) => rate.mutate(n)} />
      </div>
      <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
        <BookHeart className="size-3.5 shrink-0" aria-hidden />
        {count > 0 && avg !== undefined ? (
          <span>
            Средняя оценка читателей:{' '}
            <span className="tabular-nums text-foreground">{avg.toFixed(1)}</span> · {count}{' '}
            {pluralVotes(count)}
          </span>
        ) : (
          <span>Оценок читателей пока нет</span>
        )}
      </p>
    </div>
  );
}

function pluralVotes(n: number): string {
  const mod10 = n % 10;
  const mod100 = n % 100;
  if (mod10 === 1 && mod100 !== 11) return 'оценка';
  if (mod10 >= 2 && mod10 <= 4 && (mod100 < 10 || mod100 >= 20)) return 'оценки';
  return 'оценок';
}

/**
 * ShelfSection — блок «полки книги» под мета-блоком. И контрол, и список полок
 * в одном месте (раньше кнопка «На полку» висела в ряду действий сверху —
 * перенесли вниз). Малоакцентно: мелкий muted-текст + приглушённые чипы.
 *  - книга ни на одной полке → одна кнопка «На полку» (открывает диалог);
 *  - книга на ≥1 полке → «На полках: чип · чип +N» + компактное «Изменить».
 * Удалённую книгу на полки не кладём (контрол скрыт), но уже имеющееся членство
 * показываем.
 */
function ShelfSection({ bookId, deleted }: { bookId: number; deleted: boolean }) {
  // Служебную «Избранное» исключаем — её передаёт ★ в шапке (без дубля);
  // здесь только пользовательские полки.
  const shelves = (useBookCollections(bookId).data ?? []).filter((s) => s.kind !== 'favorites');
  const onShelves = shelves.length > 0;
  // Удалённая книга без полок — показывать нечего.
  if (!onShelves && deleted) return null;
  const shown = shelves.slice(0, 3);
  const extra = shelves.length - shown.length;

  return (
    <div className="flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
      {onShelves ? (
        <>
          <Library className="size-3.5 shrink-0" aria-hidden />
          <span>На полках:</span>
          {shown.map((s) => (
            <span key={s.id} className="rounded border border-border bg-muted px-1.5 py-0.5">
              {s.name}
            </span>
          ))}
          {extra > 0 ? <span className="tabular-nums">+{extra}</span> : null}
        </>
      ) : null}
      {!deleted ? <AddToShelfDialog bookId={bookId} compact={onShelves} /> : null}
    </div>
  );
}

/**
 * readButtonLabel — текст на кнопке открытия ридера. Без сохранённого
 * прогресса — нейтральное «Читать»; с прогрессом > 0 показываем
 * «Продолжить N%» чтобы пользователь видел что в браузерном ридере
 * уже что-то прочитано и продолжит с того места.
 *
 * 100% мы не показываем как «Продолжить 100%» — это сигнал «уже
 * дочитано», но если хочется перечитать — кнопка такая же «Читать».
 */
function readButtonLabel(fraction?: number): string {
  if (fraction === undefined || fraction <= 0 || fraction >= 1) return 'Читать';
  const pct = Math.max(1, Math.round(fraction * 100));
  return `Продолжить ${pct}%`;
}

/**
 * ReadStatusField — строка «Прочитана» в meta-grid карточки книги.
 *
 * Два состояния:
 *  - is_read=true → «✓ <дата>» + малозаметная кнопка-крестик «Снять»
 *  - is_read=false → «Нет ·» + кнопка-чекмарк «Отметить»
 *
 * Используем тот же useToggleRead с optimistic update — клик
 * мгновенно перерисовывает состояние, при ошибке откатываемся.
 *
 * Возвращает фрагмент с <dt>/<dd> — расчитано на использование
 * внутри родительского <dl class="grid grid-cols-[max-content_1fr]">.
 */
function ReadStatusField({
  bookId,
  isRead,
  readAt,
}: {
  bookId: number;
  isRead: boolean;
  readAt?: string;
}) {
  const toggle = useToggleRead();
  return (
    <>
      <dt className="text-muted-foreground">Прочитана</dt>
      <dd className="flex items-center gap-2">
        {isRead ? (
          <>
            <span className="inline-flex items-center gap-1 text-green-700 dark:text-green-400">
              <Check className="size-3.5" aria-hidden />
              {readAt ? formatReadDate(readAt) : 'Да'}
            </span>
            <button
              type="button"
              onClick={() => toggle.mutate({ bookId, isRead: false })}
              disabled={toggle.isPending}
              className="inline-flex items-center gap-0.5 text-xs text-muted-foreground hover:text-foreground transition-colors disabled:opacity-50"
              aria-label="Снять отметку «прочитано»"
            >
              <X className="size-3" aria-hidden />
              снять
            </button>
          </>
        ) : (
          <>
            <span className="text-muted-foreground">Нет</span>
            <span className="text-muted-foreground">·</span>
            <button
              type="button"
              onClick={() => toggle.mutate({ bookId, isRead: true })}
              disabled={toggle.isPending}
              className="inline-flex items-center gap-1 text-xs text-foreground underline-offset-2 hover:underline disabled:opacity-50"
            >
              <Check className="size-3" aria-hidden />
              Отметить
            </button>
          </>
        )}
      </dd>
    </>
  );
}

/**
 * formatReadDate — «17 мая 2026» (русская локаль, без времени).
 * Принимает ISO-строку с TZ из бэка (RFC 3339), отдаёт человеческое.
 */
function formatReadDate(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleDateString('ru-RU', { day: 'numeric', month: 'long', year: 'numeric' });
}
