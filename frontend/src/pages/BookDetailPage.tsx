import { Link, useParams } from '@tanstack/react-router';
import { BookText, BookOpen, BookHeart, Check, ChevronRight, X, Library, Globe } from 'lucide-react';
import { AuthorsEditor } from '@/components/AuthorsEditor';
import { GenresEditor } from '@/components/GenresEditor';
import { SeriesEditor } from '@/components/SeriesEditor';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardTitle } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import { Tooltip, TooltipTrigger, TooltipContent } from '@/components/ui/tooltip';
import { AdaptationsSection } from '@/components/AdaptationsSection';
import { AddToShelfDialog } from '@/components/AddToShelfDialog';
import { BackButton } from '@/components/BackButton';
import { BookCover } from '@/components/BookCover';
import { DownloadMenu } from '@/components/DownloadMenu';
import { EditionRow } from '@/components/EditionRow';
import { ExpandableText } from '@/components/ExpandableText';
import { InlineEditableField } from '@/components/InlineEditableField';
import { useOverrides } from '@/lib/admin';
import { useMe } from '@/lib/auth';
import { RegroupWorkButton } from '@/components/RegroupWorkButton';
import { SplitEditionsDialog } from '@/components/SplitEditionsDialog';
import { MergeIntoWorkDialog } from '@/components/MergeIntoWorkDialog';
import { FavoriteButton } from '@/components/FavoriteButton';
import { RatingControl } from '@/components/RatingControl';
import { useRateBook } from '@/lib/ratings';
import { fmtRating, externalRatingSourceLabel } from '@/lib/ratingDisplay';
import { formatBytes, translationLine } from '@/lib/format';
import { SendToKindleButton } from '@/components/SendToKindleButton';
import { useBookCard, useToggleRead, type Book } from '@/lib/books';
import { useBookCollections } from '@/lib/collections';
import { useLanguageMap, useSrcLanguageMap } from '@/lib/content';
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
  // Список оверрайдов работы (для админ-индикаторов; null/disabled у не-админа).
  const overrides = useOverrides(book?.work_id ?? undefined);
  const workOverridden = overrides.data?.work ?? [];
  const isAdmin = useMe().data?.role === 'admin';

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
  // Обложку грузим по РЕГЕНЕРИРУЮЩЕМУ эндпоинту /api/covers/book/{editionId}
  // (а не by-name /api/covers/{cover_path}, который НЕ восстанавливает файл после
  // очистки/LRU-эвикции кэша → 404). Берём издание, у которого есть обложка (у
  // мульти открытое может быть без неё); иначе — открытое.
  const coverEditionId = editions.find((e) => e.cover_path)?.id ?? book.id;

  return (
    // Кап ширины: на широком десктопе карточка — центрированная читаемая
    // колонка, без огромной пустоты справа от обложки (была до редизайна).
    <article className="mx-auto max-w-4xl space-y-4">
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
        <CardContent className="space-y-5">
          <div className="flex gap-4 md:gap-6 md:items-start">
            <BookCover
              src={`/api/covers/book/${coverEditionId}`}
              title={book.title}
              className="w-24 shrink-0 sm:w-32 md:w-44"
            />

            {/* Идентичность работы рядом с обложкой: заголовок, авторы, строка
                сигналов, серия, жанры. На мобиле обложка слева (не по центру),
                справа от неё — заголовок и сигналы, без пустых полей по бокам. */}
            <div className="min-w-0 flex-1 space-y-2.5">
              <div className="flex flex-wrap items-start justify-between gap-2">
                <div className="min-w-0 space-y-1">
                  <InlineEditableField
                    targetKind="work"
                    targetID={book.work_id ?? 0}
                    field="title"
                    value={book.title}
                    kind="text"
                    label="Название"
                    overridden={workOverridden.includes('title')}
                    layout="heading"
                  >
                    <CardTitle className="text-2xl tracking-tight">{book.title}</CardTitle>
                  </InlineEditableField>
                  <AuthorsEditor
                    workId={book.work_id ?? 0}
                    authors={book.authors}
                    overridden={workOverridden.includes('authors')}
                  />
                </div>
                {/* Действия — кластер справа от заголовка на десктопе; на мобиле
                    отдельным рядом ниже (рядом с обложкой колонка узкая). */}
                <div className="hidden flex-wrap items-center gap-2 md:flex">
                  <ActionButtons book={book} multi={multi} />
                </div>
              </div>

              <CardSignalRow book={book} />

              {/* Строку показываем и без серии — у админа SeriesEditor даёт контрол
                  «добавить в серию». #N (PR3) виден при наличии серии: значение есть
                  ИЛИ админ (тогда «· #—» редактируем — задать номер после добавления). */}
              {book.series || isAdmin ? (
                <p className="text-sm">
                  <SeriesEditor
                    workId={book.work_id ?? 0}
                    authorId={book.authors[0]?.id}
                    series={book.series ?? null}
                    serNo={book.ser_no ?? null}
                    overridden={workOverridden.includes('series')}
                  />
                  {book.series && (book.ser_no || isAdmin) ? (
                    <InlineEditableField
                      targetKind="work"
                      targetID={book.work_id ?? 0}
                      field="ser_no"
                      value={book.ser_no ?? null}
                      kind="int"
                      label="№ в серии"
                      overridden={workOverridden.includes('ser_no')}
                      layout="heading"
                    >
                      <span className="text-muted-foreground"> · #{book.ser_no ?? '—'}</span>
                    </InlineEditableField>
                  ) : null}
                </p>
              ) : null}

              <GenresEditor
                workId={book.work_id ?? 0}
                genres={book.genres}
                overridden={workOverridden.includes('genres')}
              />

              {/* Полка + «Детали файла» — в шапке у обложки (над панелью оценок):
                  заполняют место рядом с обложкой, это метаданные/организация книги.
                  На ДЕСКТОПЕ — здесь (hidden md:block). На мобайле эти блоки уходят
                  вниз (после действий и оценки), чтобы важные действия не оказались
                  под второстепенными — см. ниже md:hidden-блок. */}
              <div className="hidden space-y-2.5 md:block">
                <ShelfSection bookId={book.id} deleted={book.deleted ?? false} />
                {!multi ? <FileDetails book={book} /> : null}
              </div>
            </div>
          </div>

          {/* Действия (мобайл) — отдельный блок под шапкой, по приоритету:
              «Читать» крупной кнопкой с текстом, затем второстепенные (Скачать/
              Kindle/★). На десктопе действия — кластер справа от заголовка. */}
          {!book.deleted ? (
            <div className="space-y-2 md:hidden">
              <MobileActions book={book} multi={multi} />
            </div>
          ) : null}

          {/* «Моё»: ваша оценка + переключатель «прочитано» (снятие сохранено).
              Полноширинная панель — намеренный разделитель «шапка / контент». */}
          {!book.deleted ? (
            <MyBlock book={book} cardKey={[mode === 'work' ? 'work' : 'book', String(id)]} />
          ) : null}

          {/* Полка + «Детали файла» (мобайл) — ниже оценки, как второстепенное.
              На десктопе они в шапке у обложки (hidden md:block выше). */}
          <div className="space-y-3 md:hidden">
            <ShelfSection bookId={book.id} deleted={book.deleted ?? false} />
            {!multi ? <FileDetails book={book} /> : null}
          </div>

          {book.deleted ? (
            <p className="rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive">
              В источнике помечена удалённой (DEL=1).
            </p>
          ) : null}

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

          {/* Админ: присоединить другую книгу (дубль вне общей серии/автора).
              Тихий ряд внизу; сам скрыт у не-админа (тогда обёртка пуста). */}
          <div className="border-t border-border pt-3 empty:hidden">
            <MergeIntoWorkDialog workId={book.work_id ?? 0} workTitle={book.title} />
          </div>
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
  const ov = useOverrides(book.work_id ?? undefined);
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
        {/* Админ: точечный пересбор работы + вынос изданий в отдельную книгу
            (оба сами скрыты у не-админа). */}
        <div className="flex items-center gap-1">
          <RegroupWorkButton workId={book.work_id} />
          <SplitEditionsDialog editions={editions} workTitle={book.title} />
        </div>
      </div>
      <ul className="space-y-2">
        {editions.map((e) => (
          <EditionRow
            key={e.id}
            edition={e}
            workTitle={book.title}
            overridden={ov.data?.book[String(e.id)] ?? []}
          />
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
        <ExpandableText text={annotation} lines={6} className="text-muted-foreground" />
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

// externalRatingDisplay — единый «Внешний рейтинг» с атрибуцией источника.
// Приоритет: LIBRATE (донорская библиотека) → web (Google Books/OpenLibrary).
function externalRatingDisplay(book: Book): { value: string; source: string } | null {
  if (book.rating != null) {
    return { value: fmtRating(book.rating), source: externalRatingSourceLabel('library') };
  }
  if (book.external_rating != null) {
    return {
      value: fmtRating(book.external_rating),
      source: externalRatingSourceLabel(book.external_rating_source),
    };
  }
  return null;
}

/**
 * ActionButtons — ДЕСКТОПНЫЙ кластер действий (справа от заголовка): ★ избранное +
 * (для одного издания) Читать / На Kindle / Скачать. При нескольких изданиях
 * действия — в строках секции «Издания», поэтому здесь только ★. На мобайле своя
 * раскладка по приоритету — см. MobileActions.
 */
function ActionButtons({ book, multi }: { book: Book; multi: boolean }) {
  return (
    <>
      <FavoriteButton target="book" id={book.id} isFavorite={book.is_favorite ?? false} />
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
    </>
  );
}

/**
 * MobileActions — действия на мобайле, по приоритету: «Читать» — крупная primary-
 * кнопка во всю ширину С ТЕКСТОМ (главное действие), ниже ряд второстепенных
 * (Скачать / На Kindle с текстом + ★ иконкой). Так важное действие не оказывается
 * иконкой ниже второстепенных полки/деталей (см. правку раскладки мобайла). При
 * нескольких изданиях Читать/скачивание — на каждое издание в секции «Издания»,
 * поэтому здесь только ★.
 */
function MobileActions({ book, multi }: { book: Book; multi: boolean }) {
  if (multi) {
    return <FavoriteButton target="book" id={book.id} isFavorite={book.is_favorite ?? false} />;
  }
  return (
    <>
      <Button asChild className="w-full gap-1.5">
        <Link
          to="/books/$id/read"
          params={{ id: String(book.id) }}
          aria-label="Открыть книгу в браузерном ридере"
        >
          <BookOpen className="size-4" aria-hidden />
          {readButtonLabel(book.reading_fraction)}
        </Link>
      </Button>
      <div className="flex items-center gap-2">
        <DownloadMenu bookId={book.id} showLabel />
        <SendToKindleButton bookId={book.id} showLabel />
        <FavoriteButton target="book" id={book.id} isFavorite={book.is_favorite ?? false} />
      </div>
    </>
  );
}

/**
 * CardSignalRow — компактная строка сигналов под заголовком: год · 🌐 внешний
 * рейтинг (Tooltip с источником) · 📖 средняя оценка читателей (count) · язык
 * (именем). Под ней — тихая строка «титульного листа»: «Перевод с французского
 * — Гинзбург Ю. А.» (переводчик ОТКРЫТОГО издания + язык оригинала одной
 * естественной фразой; полное имя — в тултипе и «Деталях файла»). Пустые
 * сигналы скрываются; экранизации сюда НЕ выносим — для них на карточке есть
 * отдельная секция AdaptationsSection (на плашке-списка 🎬 нужен, на карточке
 * дублировал бы). Зеркалит идею BookMeta для плашки списка.
 */
function CardSignalRow({ book }: { book: Book }) {
  const langMap = useLanguageMap();
  const srcLangMap = useSrcLanguageMap();
  const ov = useOverrides(book.work_id ?? undefined);
  const ext = externalRatingDisplay(book);
  const avg = book.rating_avg;
  const count = book.rating_count ?? 0;
  const hasReader = avg !== undefined && count > 0;
  const langName = book.lang ? (langMap.get(book.lang) ?? book.lang) : null;
  // Язык оригинала — только когда известен И отличается от языка издания
  // (совпадение = книга в оригинале, фраза была бы шумом).
  const srcLangName =
    book.src_lang && book.src_lang !== book.lang
      ? (srcLangMap.get(book.src_lang) ?? langMap.get(book.src_lang) ?? book.src_lang)
      : null;
  // Переводчик ОТКРЫТОГО издания (консистентно с lang — он тоже открытого).
  // При ≥2 изданиях per-edition переводчики видны и в секции «Издания».
  const opened = (book.editions ?? []).find((e) => e.id === book.id) ?? book.editions?.[0];
  const translator = opened?.translator;
  const translation = translationLine(srcLangName, translator);

  if (!book.written_year && !ext && !hasReader && !langName && !translation) return null;

  return (
    <div className="space-y-1">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-muted-foreground">
      {book.written_year ? (
        <InlineEditableField
          targetKind="work"
          targetID={book.work_id ?? 0}
          field="written_year"
          value={book.written_year}
          kind="int"
          label="Год написания"
          overridden={(ov.data?.work ?? []).includes('written_year')}
          layout="heading"
        >
          <span className="tabular-nums">{book.written_year}</span>
        </InlineEditableField>
      ) : null}
      {ext ? (
        <Tooltip>
          <TooltipTrigger asChild>
            <span className="inline-flex items-center gap-1">
              <Globe className="size-3.5 shrink-0" aria-hidden />
              <span className="tabular-nums text-foreground">{ext.value}</span>
            </span>
          </TooltipTrigger>
          <TooltipContent>Внешний рейтинг · {ext.source}</TooltipContent>
        </Tooltip>
      ) : null}
      {hasReader ? (
        <span className="inline-flex items-center gap-1" title="Оценка читателей">
          <BookHeart className="size-3.5 shrink-0" aria-hidden />
          <span className="tabular-nums text-foreground">{avg!.toFixed(1)}</span>
          <span>({count})</span>
        </span>
      ) : null}
      {langName ? (
        <InlineEditableField
          targetKind="book"
          targetID={book.id}
          field="lang"
          value={book.lang}
          kind="lang"
          label="Язык"
          overridden={(ov.data?.book[String(book.id)] ?? []).includes('lang')}
          layout="heading"
        >
          <span>{langName}</span>
        </InlineEditableField>
      ) : null}
      </div>
      {translation ? (
        // Тихая строка «титульного листа»: полное имя переводчика — в тултипе.
        <p className="text-xs text-muted-foreground" title={translator || undefined}>
          {translation}
        </p>
      ) : null}
    </div>
  );
}

/**
 * MyBlock — блок личного взаимодействия «Моё»: ваша оценка (work-level звёзды) +
 * переключатель «прочитано». Снятие отметки СОХРАНЕНО (та же useToggleRead, что
 * была в ReadStatusField — просто переехала из мета-сетки сюда, в осмысленный
 * блок). cardKey — ключ кэша открытой карточки для оптимистичного обновления.
 */
function MyBlock({ book, cardKey }: { book: Book; cardKey: (string | number)[] }) {
  const rate = useRateBook(book.work_id ?? book.id, cardKey);
  const toggle = useToggleRead();
  const value = book.user_rating ?? 0;
  const isRead = book.is_read ?? false;
  return (
    <div className="flex flex-wrap items-center gap-x-6 gap-y-3 rounded-md border border-border bg-muted/30 px-3 py-2.5">
      <div className="flex items-center gap-2">
        <span className="text-sm text-muted-foreground">Ваша оценка:</span>
        <RatingControl value={value} disabled={rate.isPending} onChange={(n) => rate.mutate(n)} />
      </div>
      {isRead ? (
        // flex-wrap + whitespace-nowrap на юнитах: длинная дата «23 июня 2026 г.»
        // иначе ломалась посреди (на узкой мобильной колонке «г.» уезжала на строку).
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm">
          <span className="inline-flex items-center gap-1 whitespace-nowrap text-green-700 dark:text-green-400">
            <Check className="size-4" aria-hidden />
            Прочитано
          </span>
          {book.read_at ? (
            <span className="whitespace-nowrap text-muted-foreground">· {formatReadDate(book.read_at)}</span>
          ) : null}
          <button
            type="button"
            onClick={() => toggle.mutate({ bookId: book.id, isRead: false })}
            disabled={toggle.isPending}
            className="inline-flex items-center gap-0.5 whitespace-nowrap rounded-md border border-border px-2 py-0.5 text-xs text-muted-foreground transition-colors hover:text-foreground disabled:opacity-50"
            aria-label="Снять отметку «прочитано»"
          >
            <X className="size-3" aria-hidden />
            снять
          </button>
        </div>
      ) : (
        <button
          type="button"
          onClick={() => toggle.mutate({ bookId: book.id, isRead: true })}
          disabled={toggle.isPending}
          className="inline-flex items-center gap-1 rounded-md border border-border px-2.5 py-1 text-sm transition-colors hover:bg-accent disabled:opacity-50"
        >
          <Check className="size-4" aria-hidden />
          Отметить прочитанной
        </button>
      )}
    </div>
  );
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
 * FileDetails — технические поля издания под раскрывашкой «Детали файла»
 * (нативный <details>): Файл / Архив / Размер / Год издания / Добавлена / LIBID.
 * Свёрнуто по умолчанию — это справочные данные, не должны конкурировать с
 * читательской информацией в основном потоке. Только для одного издания; при
 * нескольких поля живут в строках секции «Издания».
 */
function FileDetails({ book }: { book: Book }) {
  // Правки изданий этой работы (для админ-индикаторов; React Query дедупит вызов
  // с EditionsSection по ключу ['overrides', workId]).
  const ov = useOverrides(book.work_id ?? undefined);
  const overridden = ov.data?.book[String(book.id)] ?? [];
  // Издательские атрибуты открытого издания (translator/publisher/isbn живут в
  // EditionRef, не на Book) — паритет с EditionRow: при единственном издании
  // секции «Издания» нет, и раньше их было вообще негде увидеть/править.
  const opened = (book.editions ?? []).find((e) => e.id === book.id) ?? book.editions?.[0];
  return (
    <details className="group text-sm">
      <summary className="flex cursor-pointer list-none items-center gap-1.5 text-muted-foreground transition-colors hover:text-foreground [&::-webkit-details-marker]:hidden">
        <ChevronRight
          className="size-4 shrink-0 transition-transform group-open:rotate-90"
          aria-hidden
        />
        Детали файла
      </summary>
      <dl className="mt-2 grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 pl-[1.375rem] text-xs">
        <Field label="Файл" value={`${book.file_name}.${book.ext}`} mono />
        <Field label="Архив" value={book.archive} mono />
        <Field label="Размер" value={formatBytes(book.size_bytes)} />
        <InlineEditableField
          targetKind="book"
          targetID={book.id}
          field="translator"
          value={opened?.translator}
          kind="text"
          label="Переводчик"
          overridden={overridden.includes('translator')}
          layout="grid"
        />
        <InlineEditableField
          targetKind="book"
          targetID={book.id}
          field="publisher"
          value={opened?.publisher}
          kind="text"
          label="Издатель"
          overridden={overridden.includes('publisher')}
          layout="grid"
        />
        <InlineEditableField
          targetKind="book"
          targetID={book.id}
          field="isbn"
          value={opened?.isbn}
          kind="text"
          label="ISBN"
          overridden={overridden.includes('isbn')}
          layout="grid"
          mono
        />
        {/* Год издания редактируем у админа (кейс Чарушина: edition_year=1000). */}
        <InlineEditableField
          targetKind="book"
          targetID={book.id}
          field="edition_year"
          value={book.edition_year}
          kind="int"
          label="Год издания"
          overridden={overridden.includes('edition_year')}
          layout="grid"
        />
        {book.date_added ? (
          <Field label="Добавлена" value={formatReadDate(book.date_added)} />
        ) : null}
        <Field label="LIBID" value={book.lib_id} mono />
      </dl>
    </details>
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
