package catalog

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/skriptes/skriptes/backend/internal/books"
)

// ErrNotFound возвращается когда автор / серия с таким id не существует.
var ErrNotFound = errors.New("not found")

// Service — read-only сервис для авторов и серий. Отдельно от books,
// потому что здесь логика агрегаций и SQL-джойнов, а не Meili-поиска.
type Service struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// GetAuthor собирает Author с агрегатами одной серией пайплайна:
//  1. строка автора
//  2. count книг (без deleted)
//  3. топ-5 жанров (по числу книг этого автора в каждом)
//  4. серии (по числу книг автора в каждой)
//  5. до 50 последних книг (deleted скрыты, отсортированы по date_added desc)
//  6. гистограмма по году добавления (year_stats)
//  7. ReadCount — сколько книг этого автора пользователь явно отметил
//     как прочитанные (reads.completed_at IS NOT NULL); заполняется
//     только если userID > 0.
//
// Каждый шаг — отдельный запрос; для 99% карточек это <10 ms total.
// Если когда-то станет горячо — соберём в один CTE.
// bookExclusionClause строит SQL-фрагмент для AND в WHERE по алиасу таблицы
// книг `b`, исключающий книги со скрытым языком ИЛИ несущие хоть один скрытый
// жанр (видимость контента: admin ∪ персональные). startArg — номер следующего
// позиционного аргумента ($N). Пустые срезы → "" + нет доп. аргументов (no-op),
// поэтому хелпер безопасно звать всегда. Возвращает фрагмент и аргументы, которые
// нужно дописать в конец списка args запроса (в том же порядке).
// notCompilationClause — безусловное исключение книг-сборников (works.kind ≠ NULL)
// из АГРЕГАТОВ и СТАТИСТИКИ автора (счётчики, годы, жанры, языки, рейтинг,
// экранизации). Loose coupling: сборник/антология/том собрания — свойство самого
// сборника, не «что написал автор», поэтому не раздувает счётчики и не искажает
// статистику. Алиас книги в подзапросе — `b`. В отличие от opt-in
// hideCompilations (убирает книги из выдачи целиком), применяется ВСЕГДА. НЕ
// применяется к списку книг карточки (сборники видны в своей секции) и к базовой
// видимости автора.
const notCompilationClause = ` AND COALESCE((SELECT wk.kind FROM works wk WHERE wk.id = b.work_id), '') = ''`

func bookExclusionClause(startArg int, excludeGenres, excludeLangs []string, hideCompilations bool) (clause string, args []any) {
	n := startArg
	var b strings.Builder
	if len(excludeLangs) > 0 {
		fmt.Fprintf(&b, " AND (b.lang IS NULL OR NOT (b.lang = ANY($%d::text[])))", n)
		args = append(args, excludeLangs)
		n++
	}
	if len(excludeGenres) > 0 {
		fmt.Fprintf(&b, " AND NOT EXISTS (SELECT 1 FROM book_genres bgx JOIN genres gx ON gx.id = bgx.genre_id"+
			" WHERE bgx.book_id = b.id AND gx.fb2_code = ANY($%d::text[]))", n)
		args = append(args, excludeGenres)
	}
	// «Скрывать сборники» (opt-in профильная настройка): книга скрыта, если её
	// работа помечена kind (сборник/антология/том собрания). NULL work_id →
	// видна (обычная книга).
	if hideCompilations {
		b.WriteString(" AND COALESCE((SELECT wk.kind FROM works wk WHERE wk.id = b.work_id), '') = ''")
	}
	return b.String(), args
}

// GetAuthor — карточка автора. excludeGenres/excludeLangs — скрытые жанры/языки
// (видимость контента), применяются к счётчику книг и списку книг, чтобы на
// карточке не всплывал контент, скрытый глобально/персонально (как в /books).
// SetAuthorService — ручная admin-метка «служебный автор» (в обе стороны).
// is_service_source='manual' защищает решение от эвристики
// metadata.ClassifyServiceAuthors (она не трогает manual-строки).
// ErrNotFound — автора нет.
func (s *Service) SetAuthorService(ctx context.Context, id int64, isService bool) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE authors SET is_service = $2, is_service_source = 'manual' WHERE id = $1
	`, id, isService)
	if err != nil {
		return fmt.Errorf("set author service: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) GetAuthor(ctx context.Context, id, userID int64, excludeGenres, excludeLangs []string, hideCompilations bool) (Author, error) {
	var (
		a         Author
		bio       pgtype.Text
		photoPath pgtype.Text
		fetchedAt pgtype.Timestamptz
	)
	err := s.pool.QueryRow(ctx, `
		SELECT id, last_name, first_name, middle_name, bio, photo_path, metadata_fetched_at, is_service
		FROM authors WHERE id = $1
	`, id).Scan(&a.ID, &a.LastName, &a.FirstName, &a.MiddleName, &bio, &photoPath, &fetchedAt, &a.IsService)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Author{}, ErrNotFound
		}
		return Author{}, fmt.Errorf("query author: %w", err)
	}
	a.FullName = fullName(a.LastName, a.FirstName, a.MiddleName)
	if bio.Valid {
		a.Bio = bio.String
	}
	if photoPath.Valid {
		a.PhotoPath = photoPath.String
	}
	a.EnrichmentFetched = fetchedAt.Valid

	exClause, exArgs := bookExclusionClause(2, excludeGenres, excludeLangs, hideCompilations)
	countArgs := append([]any{id}, exArgs...)
	// Считаем ЛОГИЧЕСКИЕ книги (работы), а не издания — иначе счётчик «N книг»
	// разойдётся со схлопнутым списком. COALESCE(-id) — defensive на случай
	// (невозможного по инварианту) NULL work_id.
	if err := s.pool.QueryRow(ctx, `
		SELECT count(DISTINCT COALESCE(b.work_id, -b.id))
		FROM book_authors ba
		JOIN books b ON b.id = ba.book_id
		WHERE ba.author_id = $1 AND b.deleted = false`+exClause+notCompilationClause,
		countArgs...).Scan(&a.BookCount); err != nil {
		return Author{}, fmt.Errorf("count books: %w", err)
	}
	a.BooksTotal = a.BookCount

	genres, err := s.queryAuthorTopGenres(ctx, id, 5, excludeGenres, excludeLangs, hideCompilations)
	if err != nil {
		return Author{}, err
	}
	a.TopGenres = genres

	series, err := s.queryAuthorSeries(ctx, id, excludeGenres, excludeLangs, hideCompilations)
	if err != nil {
		return Author{}, err
	}
	a.Series = series

	// 500 — потолок для самых плодовитых авторов (Asimov ~500, Stephen
	// King ~80). Группировка по сериям на фронте требует полного списка,
	// поэтому усечение в 50 как раньше уже не работает.
	bookList, refs, err := s.queryAuthorBooks(ctx, id, 500, excludeGenres, excludeLangs, hideCompilations)
	if err != nil {
		return Author{}, err
	}
	a.Books = bookList
	a.BookRefs = refs
	// Books — json-тег без omitempty: nil-срез сериализуется как null и роняет
	// фронт (author.books.length). У автора без видимых книг нормализуем в [].
	if a.Books == nil {
		a.Books = []books.ListItem{}
	}
	// Обогащённая плашка: внешний рейтинг/источник, оценка читателей, экранизации
	// по work_id (как в /books). User-поля (favorite/read) — в api-слое.
	books.HydrateListMeta(ctx, s.pool, a.Books)

	years, err := s.queryAuthorYearStats(ctx, id, excludeGenres, excludeLangs, hideCompilations)
	if err != nil {
		return Author{}, err
	}
	a.YearStats = years

	// Агрегаты-зеркало строки списка (рейтинги/экранизации/годы + языки), чтобы
	// карточка показывала то же, что компактный список авторов.
	if err := s.queryAuthorMeta(ctx, &a, id, excludeGenres, excludeLangs, hideCompilations); err != nil {
		return Author{}, err
	}
	langs, err := s.queryAuthorLanguages(ctx, id, excludeGenres, excludeLangs, hideCompilations)
	if err != nil {
		return Author{}, err
	}
	a.Languages = langs

	if userID > 0 {
		read, err := s.queryAuthorReadCount(ctx, id, userID)
		if err != nil {
			return Author{}, err
		}
		a.ReadCount = read
	}

	return a, nil
}

// GetSeries возвращает серию + список её книг в порядке ser_no (затем title).
// Удалённые книги скрыты — серия "висит" только пока в ней есть живые тома.
//
// Если userID > 0, дополнительно считается ReadCount (сколько книг серии
// уже скачивал текущий пользователь). YearStats считаются всегда.
func (s *Service) GetSeries(ctx context.Context, id, userID int64, excludeGenres, excludeLangs []string, hideCompilations bool) (Series, error) {
	var (
		out      Series
		authorID pgtype.Int8
	)
	err := s.pool.QueryRow(ctx, `
		SELECT s.id, s.title, s.author_id,
		       COALESCE(NULLIF(TRIM(CONCAT_WS(' ', a.last_name, a.first_name, a.middle_name)), ''), '')
		FROM series s
		LEFT JOIN authors a ON a.id = s.author_id
		WHERE s.id = $1
	`, id).Scan(&out.ID, &out.Title, &authorID, &out.AuthorName)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Series{}, ErrNotFound
		}
		return Series{}, fmt.Errorf("query series: %w", err)
	}
	if authorID.Valid {
		v := authorID.Int64
		out.AuthorID = &v
	}

	exClause, exArgs := bookExclusionClause(2, excludeGenres, excludeLangs, hideCompilations)
	bookArgs := append([]any{id}, exArgs...)

	// Все авторы книг серии (по числу работ убыв.) — серия может содержать книги
	// нескольких авторов (со-авторство / ручной перенос), шапка показывает всех.
	if arows, aerr := s.pool.Query(ctx, `
		SELECT a.id,
		       TRIM(CONCAT_WS(' ', a.last_name, a.first_name, a.middle_name)) AS name,
		       count(DISTINCT COALESCE(b.work_id, -b.id)) AS cnt
		FROM books b
		JOIN book_authors ba ON ba.book_id = b.id
		JOIN authors a       ON a.id = ba.author_id
		WHERE b.series_id = $1 AND b.deleted = false`+exClause+`
		GROUP BY a.id, a.last_name, a.first_name, a.middle_name
		ORDER BY cnt DESC, name
	`, bookArgs...); aerr == nil {
		for arows.Next() {
			var ar SeriesAuthorRef
			var cnt int
			if arows.Scan(&ar.ID, &ar.Name, &cnt) == nil && ar.Name != "" {
				out.Authors = append(out.Authors, ar)
			}
		}
		arows.Close()
	}

	rows, err := s.pool.Query(ctx, `
		SELECT b.id, COALESCE((SELECT ww.title FROM works ww WHERE ww.id = b.work_id), b.title), b.lib_id,
		       b.lang, b.date_added, COALESCE((SELECT ww.ser_no FROM works ww WHERE ww.id = b.work_id), b.ser_no),
		       COALESCE(
		           array_agg(DISTINCT TRIM(CONCAT_WS(' ', a.last_name, a.first_name, a.middle_name))) FILTER (WHERE a.id IS NOT NULL),
		           ARRAY[]::text[]
		       ),
		       b.written_year, b.edition_year, b.normalized_title,
		       ar.filename, b.file_name, b.ext, (b.year_local_scanned_at IS NOT NULL),
		       b.work_id
		FROM books b
		JOIN archives ar ON ar.id = b.archive_id
		LEFT JOIN book_authors ba ON ba.book_id = b.id
		LEFT JOIN authors a ON a.id = ba.author_id
		WHERE b.series_id = $1 AND b.deleted = false`+exClause+`
		GROUP BY b.id, ar.filename
		ORDER BY b.ser_no NULLS LAST, b.normalized_title
	`, bookArgs...)
	if err != nil {
		return Series{}, fmt.Errorf("query series books: %w", err)
	}
	defer rows.Close()
	var sortItems []seriesSortItem
	seenWork := map[int64]int{} // work_id → индекс в out.Books (схлопывание изданий)
	for rows.Next() {
		var (
			b         books.ListItem
			lang      pgtype.Text
			dt        pgtype.Date
			serNo     pgtype.Int4
			auth      []string
			wy, ey    pgtype.Int2
			normTitle string
			archiveFn string
			fileName  string
			ext       string
			localScan bool
			workID    pgtype.Int8
		)
		if err := rows.Scan(&b.ID, &b.Title, &b.LibID, &lang, &dt, &serNo, &auth,
			&wy, &ey, &normTitle, &archiveFn, &fileName, &ext, &localScan, &workID); err != nil {
			return Series{}, err
		}
		if workID.Valid && workID.Int64 > 0 {
			if idx, ok := seenWork[workID.Int64]; ok {
				out.Books[idx].EditionCount++
				continue
			}
		}
		if lang.Valid {
			b.Lang = lang.String
		}
		if dt.Valid {
			y := dt.Time.Year()
			b.Year = &y
		}
		if serNo.Valid {
			n := int(serNo.Int32)
			b.SerNo = &n
		}
		b.Authors = auth
		b.Series = out.Title
		if workID.Valid && workID.Int64 > 0 {
			b.WorkID = workID.Int64 // ссылка карточки → /works/{work_id}
		}
		// SeriesID нужен фронту для clickable-имён в потенциальных
		// смешанных списках; в карточке серии очевидно, что все книги
		// принадлежат одной серии, но единообразный тип лучше.
		sid := id
		b.SeriesID = &sid
		si := seriesSortItem{bookID: b.ID, serNo: b.SerNo, title: b.Title, normTitle: normTitle}
		if wy.Valid {
			v := int(wy.Int16)
			si.writtenYear = &v
		}
		if ey.Valid {
			v := int(ey.Int16)
			si.editionYear = &v
		}
		if dt.Valid {
			si.dateAdded = dt.Time
		}
		sortItems = append(sortItems, si)
		out.BookRefs = append(out.BookRefs, BookYearRef{
			BookID: b.ID, Archive: archiveFn, FileName: fileName, Ext: ext,
			HasWrittenYear: wy.Valid, LocalScanned: localScan,
		})
		b.EditionCount = 1
		out.Books = append(out.Books, b)
		if workID.Valid && workID.Int64 > 0 {
			seenWork[workID.Int64] = len(out.Books) - 1
		}
	}
	if err := rows.Err(); err != nil {
		return Series{}, err
	}
	// Каскад порядка внутри серии: проставляем series_order и пересортировываем.
	ranks := assignSeriesOrder(sortItems)
	for i := range out.Books {
		if r, ok := ranks[out.Books[i].ID]; ok {
			rr := r
			out.Books[i].SeriesOrder = &rr
		}
	}
	sort.SliceStable(out.Books, func(i, j int) bool {
		return seriesOrderOf(out.Books[i]) < seriesOrderOf(out.Books[j])
	})
	out.BookCount = len(out.Books)
	// Обогащённая плашка (как в /books): рейтинги/экранизации по work_id.
	books.HydrateListMeta(ctx, s.pool, out.Books)

	years, err := s.querySeriesYearStats(ctx, id, excludeGenres, excludeLangs, hideCompilations)
	if err != nil {
		return Series{}, err
	}
	out.YearStats = years

	if userID > 0 {
		read, err := s.querySeriesReadCount(ctx, id, userID)
		if err != nil {
			return Series{}, err
		}
		out.ReadCount = read
	}

	return out, nil
}

func (s *Service) queryAuthorTopGenres(ctx context.Context, authorID int64, limit int, excludeGenres, excludeLangs []string, hideCompilations bool) ([]GenreCount, error) {
	exClause, exArgs := bookExclusionClause(3, excludeGenres, excludeLangs, hideCompilations)
	args := append([]any{authorID, limit}, exArgs...)
	// count(DISTINCT работа), не count(*) по book_genres: у многоиздательных
	// работ жанр повторяется на каждом издании, и счётчик одного жанра обгонял
	// «N книг» автора (546 > 499, прод-аудит P2 #8). Зеркало счётчика серий ниже.
	rows, err := s.pool.Query(ctx, `
		SELECT g.fb2_code, COALESCE(NULLIF(g.name_ru,''), NULLIF(g.name_en,''), g.fb2_code),
		       count(DISTINCT COALESCE(b.work_id, -b.id)) as cnt
		FROM book_authors ba
		JOIN books b      ON b.id = ba.book_id AND b.deleted = false
		JOIN book_genres bg ON bg.book_id = b.id
		JOIN genres g     ON g.id = bg.genre_id
		WHERE ba.author_id = $1`+exClause+notCompilationClause+`
		GROUP BY g.fb2_code, g.name_ru, g.name_en
		ORDER BY cnt DESC, g.fb2_code
		LIMIT $2
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("query top genres: %w", err)
	}
	defer rows.Close()
	var out []GenreCount
	for rows.Next() {
		var g GenreCount
		if err := rows.Scan(&g.Code, &g.Display, &g.Count); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// ListAuthorSeries — серии автора (для пикера переноса серии: листим серии того же
// автора без поиска, т.к. перенос внутри автора — частый кейс). Админ-контекст, без
// исключений видимости.
func (s *Service) ListAuthorSeries(ctx context.Context, authorID int64) ([]SeriesWithCount, error) {
	return s.queryAuthorSeries(ctx, authorID, nil, nil, false)
}

func (s *Service) queryAuthorSeries(ctx context.Context, authorID int64, excludeGenres, excludeLangs []string, hideCompilations bool) ([]SeriesWithCount, error) {
	// Исключения по контенту режут книги ДО группировки → серия, у которой не
	// осталось видимых книг, просто не попадает в результат (INNER JOIN + WHERE).
	// Так серии-дубли на скрытых языках («Cormoran Strike» при скрытом en) не
	// висят пустыми на карточке автора. Счётчик cnt тоже = число видимых книг.
	exClause, exArgs := bookExclusionClause(2, excludeGenres, excludeLangs, hideCompilations)
	args := append([]any{authorID}, exArgs...)
	// all_comp: серия, ЦЕЛИКОМ состоящая из сборников/антологий/томов собраний
	// (works.kind ≠ NULL у всех работ) — серия-паразит («Шекли. Сборники»,
	// «ПСС в 90 томах»). Фронт выносит такие из списка серий автора в секцию
	// «Сборники и антологии». NULL work_id → false (консервативно — не сборник).
	rows, err := s.pool.Query(ctx, `
		SELECT s.id, s.title, count(DISTINCT COALESCE(b.work_id, -b.id)) as cnt,
		       bool_and((SELECT ww.kind FROM works ww WHERE ww.id = b.work_id) IS NOT NULL) as all_comp
		FROM book_authors ba
		JOIN books b ON b.id = ba.book_id AND b.deleted = false
		JOIN series s ON s.id = b.series_id
		WHERE ba.author_id = $1`+exClause+`
		GROUP BY s.id, s.title
		ORDER BY cnt DESC, s.normalized_title
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("query series: %w", err)
	}
	defer rows.Close()
	var out []SeriesWithCount
	for rows.Next() {
		var sc SeriesWithCount
		if err := rows.Scan(&sc.ID, &sc.Title, &sc.Count, &sc.AllCompilations); err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

func (s *Service) queryAuthorBooks(ctx context.Context, authorID int64, limit int, excludeGenres, excludeLangs []string, hideCompilations bool) ([]books.ListItem, []BookYearRef, error) {
	// Исключения занимают $3.. (после $1 author, $2 limit); LIMIT остаётся $2
	// независимо от позиции в строке — позиционные аргументы по номеру.
	exClause, exArgs := bookExclusionClause(3, excludeGenres, excludeLangs, hideCompilations)
	args := append([]any{authorID, limit}, exArgs...)
	rows, err := s.pool.Query(ctx, `
		SELECT b.id, COALESCE((SELECT ww.title FROM works ww WHERE ww.id = b.work_id), b.title), b.lib_id, b.lang, b.date_added,
		       ser.id, ser.title, COALESCE((SELECT ww.ser_no FROM works ww WHERE ww.id = b.work_id), b.ser_no),
		       COALESCE(
		           array_agg(DISTINCT TRIM(CONCAT_WS(' ', a2.last_name, a2.first_name, a2.middle_name))) FILTER (WHERE a2.id IS NOT NULL),
		           ARRAY[]::text[]
		       ),
		       b.written_year, b.edition_year, b.normalized_title,
		       ar.filename, b.file_name, b.ext, (b.year_local_scanned_at IS NOT NULL),
		       b.work_id,
		       COALESCE((SELECT ww.kind FROM works ww WHERE ww.id = b.work_id), '')
		FROM book_authors ba
		JOIN books b ON b.id = ba.book_id AND b.deleted = false
		JOIN archives ar ON ar.id = b.archive_id
		LEFT JOIN series ser ON ser.id = b.series_id
		LEFT JOIN book_authors ba2 ON ba2.book_id = b.id
		LEFT JOIN authors a2 ON a2.id = ba2.author_id
		WHERE ba.author_id = $1`+exClause+`
		GROUP BY b.id, ser.id, ser.title, ar.filename
		ORDER BY b.date_added DESC NULLS LAST, b.normalized_title
		LIMIT $2
	`, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("query author books: %w", err)
	}
	defer rows.Close()
	var out []books.ListItem
	var refs []BookYearRef
	// sortItems сгруппированы по series_id для покнижного каскада series_order.
	bySeries := map[int64][]seriesSortItem{}
	// Схлопывание изданий в логическую книгу: представитель (первый по ORDER BY) на
	// work_id, остальные издания той же работы только бампают EditionCount.
	seenWork := map[int64]int{} // work_id → индекс в out
	for rows.Next() {
		var (
			b           books.ListItem
			lang        pgtype.Text
			dt          pgtype.Date
			seriesID    pgtype.Int8
			seriesTitle pgtype.Text
			serNo       pgtype.Int4
			auth        []string
			wy, ey      pgtype.Int2
			normTitle   string
			archiveFn   string
			fileName    string
			ext         string
			localScan   bool
			workID      pgtype.Int8
		)
		if err := rows.Scan(&b.ID, &b.Title, &b.LibID, &lang, &dt, &seriesID, &seriesTitle, &serNo, &auth,
			&wy, &ey, &normTitle, &archiveFn, &fileName, &ext, &localScan, &workID, &b.Kind); err != nil {
			return nil, nil, err
		}
		// Дубликат-издание уже виденной работы → только счётчик, в список не добавляем.
		if workID.Valid && workID.Int64 > 0 {
			if idx, ok := seenWork[workID.Int64]; ok {
				out[idx].EditionCount++
				continue
			}
		}
		if lang.Valid {
			b.Lang = lang.String
		}
		if dt.Valid {
			y := dt.Time.Year()
			b.Year = &y
		}
		if seriesTitle.Valid {
			b.Series = seriesTitle.String
		}
		if seriesID.Valid {
			id := seriesID.Int64
			b.SeriesID = &id
		}
		if serNo.Valid {
			n := int(serNo.Int32)
			b.SerNo = &n
		}
		b.Authors = auth
		if workID.Valid && workID.Int64 > 0 {
			b.WorkID = workID.Int64 // ссылка карточки → /works/{work_id}
		}
		if seriesID.Valid {
			si := seriesSortItem{bookID: b.ID, serNo: b.SerNo, title: b.Title, normTitle: normTitle}
			if wy.Valid {
				v := int(wy.Int16)
				si.writtenYear = &v
			}
			if ey.Valid {
				v := int(ey.Int16)
				si.editionYear = &v
			}
			if dt.Valid {
				si.dateAdded = dt.Time
			}
			bySeries[seriesID.Int64] = append(bySeries[seriesID.Int64], si)
		}
		b.EditionCount = 1
		refs = append(refs, BookYearRef{
			BookID: b.ID, Archive: archiveFn, FileName: fileName, Ext: ext,
			HasWrittenYear: wy.Valid, LocalScanned: localScan,
		})
		out = append(out, b)
		if workID.Valid && workID.Int64 > 0 {
			seenWork[workID.Int64] = len(out) - 1
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	// Каскад series_order — на каждую серию отдельно; ранги по bookID.
	ranks := map[int64]int{}
	for _, items := range bySeries {
		for bid, r := range assignSeriesOrder(items) {
			ranks[bid] = r
		}
	}
	for i := range out {
		if r, ok := ranks[out[i].ID]; ok {
			rr := r
			out[i].SeriesOrder = &rr
		}
	}
	return out, refs, nil
}

// yearStatsBooksCap — потолок числа книг, прикладываемых к одному году
// (для тултипа). Count при этом точный; список усекаем, чтобы не раздувать
// payload у плодовитого автора. Реалистично книг в году единицы.
const yearStatsBooksCap = 50

// queryAuthorYearStats — гистограмма по году НАПИСАНИЯ книг автора
// (written_year: fb2 <title-info><date> → внешние источники). Это год
// произведения, а не дата добавления в коллекцию (см. граблю про
// date_added). Книги без written_year отбрасываются — пока год не
// извлечён/недоступен, столбик рисовать не из чего. К каждому году
// прикладываем список книг (id+title) для тултипа на фронте.
func (s *Service) queryAuthorYearStats(ctx context.Context, authorID int64, excludeGenres, excludeLangs []string, hideCompilations bool) ([]YearCount, error) {
	exClause, exArgs := bookExclusionClause(2, excludeGenres, excludeLangs, hideCompilations)
	args := append([]any{authorID}, exArgs...)
	rows, err := s.pool.Query(ctx, `
		SELECT b.written_year::int AS year, b.id, b.title
		FROM book_authors ba
		JOIN books b ON b.id = ba.book_id AND b.deleted = false
		WHERE ba.author_id = $1 AND b.written_year IS NOT NULL`+exClause+notCompilationClause+`
		ORDER BY b.written_year, b.title
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("query author year stats: %w", err)
	}
	return groupYearStats(rows)
}

// queryAuthorMeta — агрегаты-зеркало строки списка для карточки автора:
// внешний рейтинг + источник топ-издания, оценка читателей + число, наличие
// экранизаций, годы активности (по written_year). Языки — отдельно
// (queryAuthorLanguages). Все подзапросы — по видимым книгам (exClause).
func (s *Service) queryAuthorMeta(ctx context.Context, a *Author, id int64, excludeGenres, excludeLangs []string, hideCompilations bool) error {
	exClause, exArgs := bookExclusionClause(2, excludeGenres, excludeLangs, hideCompilations)
	args := append([]any{id}, exArgs...)
	var (
		extRating pgtype.Float8
		extSource pgtype.Text
		readerAvg pgtype.Float8
		readerCnt int
		hasAdapt  bool
		yrFrom    pgtype.Int2
		yrTo      pgtype.Int2
	)
	err := s.pool.QueryRow(ctx, `
		SELECT
		  (SELECT max(COALESCE(b.rating, b.external_rating))::float8 FROM book_authors ba JOIN books b ON b.id = ba.book_id
		     WHERE ba.author_id = $1 AND b.deleted = false AND (b.rating IS NOT NULL OR b.external_rating IS NOT NULL)`+exClause+notCompilationClause+`),
		  (SELECT CASE WHEN b.rating IS NOT NULL THEN 'library' ELSE b.external_rating_source END
		     FROM book_authors ba JOIN books b ON b.id = ba.book_id
		     WHERE ba.author_id = $1 AND b.deleted = false AND (b.rating IS NOT NULL OR b.external_rating IS NOT NULL)`+exClause+notCompilationClause+`
		     ORDER BY COALESCE(b.rating, b.external_rating) DESC NULLS LAST, b.id LIMIT 1),
		  (SELECT avg(br.rating)::float8 FROM book_ratings br WHERE br.work_id IN (
		     SELECT b.work_id FROM book_authors ba JOIN books b ON b.id = ba.book_id
		     WHERE ba.author_id = $1 AND b.deleted = false AND b.work_id IS NOT NULL`+exClause+notCompilationClause+`)),
		  (SELECT count(*)::int FROM book_ratings br WHERE br.work_id IN (
		     SELECT b.work_id FROM book_authors ba JOIN books b ON b.id = ba.book_id
		     WHERE ba.author_id = $1 AND b.deleted = false AND b.work_id IS NOT NULL`+exClause+notCompilationClause+`)),
		  EXISTS (SELECT 1 FROM book_authors ba JOIN books b ON b.id = ba.book_id AND b.deleted = false
		     JOIN book_adaptations ad ON ad.book_id = b.id WHERE ba.author_id = $1`+exClause+notCompilationClause+`),
		  (SELECT min(b.written_year) FROM book_authors ba JOIN books b ON b.id = ba.book_id
		     WHERE ba.author_id = $1 AND b.deleted = false AND b.written_year IS NOT NULL`+exClause+notCompilationClause+`),
		  (SELECT max(b.written_year) FROM book_authors ba JOIN books b ON b.id = ba.book_id
		     WHERE ba.author_id = $1 AND b.deleted = false AND b.written_year IS NOT NULL`+exClause+notCompilationClause+`)
	`, args...).Scan(&extRating, &extSource, &readerAvg, &readerCnt, &hasAdapt, &yrFrom, &yrTo)
	if err != nil {
		return fmt.Errorf("query author meta: %w", err)
	}
	if extRating.Valid {
		v := extRating.Float64
		a.ExternalRating = &v
	}
	if extSource.Valid && extSource.String != "" {
		sv := extSource.String
		a.ExternalRatingSource = &sv
	}
	if readerAvg.Valid {
		v := readerAvg.Float64
		a.ReaderRating = &v
	}
	a.ReaderRatingCount = readerCnt
	a.HasAdaptations = hasAdapt
	if yrFrom.Valid && yrTo.Valid {
		a.YearsActive = &YearsRange{From: int(yrFrom.Int16), To: int(yrTo.Int16)}
	}
	return nil
}

// queryAuthorLanguages — языки изданий автора (lang∪src_lang, нормализованные
// lower+btrim, граблю №14), по убыванию числа книг. Single-author зеркало
// fillAuthorLanguages из списка.
func (s *Service) queryAuthorLanguages(ctx context.Context, id int64, excludeGenres, excludeLangs []string, hideCompilations bool) ([]string, error) {
	exClause, exArgs := bookExclusionClause(2, excludeGenres, excludeLangs, hideCompilations)
	args := append([]any{id}, exArgs...)
	rows, err := s.pool.Query(ctx, `
		SELECT code, count(*) AS cnt FROM (
			SELECT NULLIF(lower(btrim(b.lang)), '') AS code
			FROM book_authors ba JOIN books b ON b.id = ba.book_id AND b.deleted = false
			WHERE ba.author_id = $1`+exClause+notCompilationClause+`
			UNION ALL
			SELECT NULLIF(lower(btrim(b.src_lang)), '') AS code
			FROM book_authors ba JOIN books b ON b.id = ba.book_id AND b.deleted = false
			WHERE ba.author_id = $1`+exClause+notCompilationClause+`
		) t
		WHERE code IS NOT NULL
		GROUP BY code
		ORDER BY cnt DESC, code
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("query author languages: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var (
			code string
			cnt  int
		)
		if err := rows.Scan(&code, &cnt); err != nil {
			return nil, err
		}
		out = append(out, code)
	}
	return out, rows.Err()
}

// groupYearStats сворачивает строки (year, id, title), отсортированные по
// году, в []YearCount: считает книги и собирает их список (с потолком
// yearStatsBooksCap). Закрывает rows.
func groupYearStats(rows pgx.Rows) ([]YearCount, error) {
	defer rows.Close()
	var out []YearCount
	for rows.Next() {
		var (
			year  int
			id    int64
			title string
		)
		if err := rows.Scan(&year, &id, &title); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].Year != year {
			out = append(out, YearCount{Year: year})
		}
		b := &out[len(out)-1]
		b.Count++
		if len(b.Books) < yearStatsBooksCap {
			b.Books = append(b.Books, YearBook{ID: id, Title: title})
		}
	}
	return out, rows.Err()
}

// queryAuthorReadCount — сколько книг автора пользователь явно
// отметил как прочитанные (reads.completed_at IS NOT NULL).
//
// До добавления явной кнопки «Прочитано» считали все строки в reads
// (download = read как heuristic). Теперь, когда у пользователя есть
// явный сигнал — считаем только его. Старые скачанные но не отмеченные
// книги в счётчик не попадают; пользователь может прокликать их
// вручную либо они появятся при дочитывании в браузерном ридере.
func (s *Service) queryAuthorReadCount(ctx context.Context, authorID, userID int64) (int, error) {
	var n int
	// DISTINCT по работе: прочитал любое издание книги → книга прочитана один раз.
	err := s.pool.QueryRow(ctx, `
		SELECT count(DISTINCT COALESCE(b.work_id, -b.id))
		FROM book_authors ba
		JOIN reads r ON r.book_id = ba.book_id AND r.completed_at IS NOT NULL
		JOIN books b ON b.id = ba.book_id AND b.deleted = false
		WHERE ba.author_id = $1 AND r.user_id = $2
	`, authorID, userID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("query author read count: %w", err)
	}
	return n, nil
}

// querySeriesYearStats — то же самое для серии: распределение книг в
// серии по году написания (written_year) + список книг каждого года.
func (s *Service) querySeriesYearStats(ctx context.Context, seriesID int64, excludeGenres, excludeLangs []string, hideCompilations bool) ([]YearCount, error) {
	exClause, exArgs := bookExclusionClause(2, excludeGenres, excludeLangs, hideCompilations)
	args := append([]any{seriesID}, exArgs...)
	rows, err := s.pool.Query(ctx, `
		SELECT b.written_year::int AS year, b.id, b.title
		FROM books b
		WHERE b.series_id = $1 AND b.deleted = false AND b.written_year IS NOT NULL`+exClause+`
		ORDER BY b.written_year, b.title
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("query series year stats: %w", err)
	}
	return groupYearStats(rows)
}

// querySeriesReadCount — сколько книг серии пользователь явно отметил
// как прочитанные. См. doc-comment queryAuthorReadCount для семантики
// completed_at vs. старой «download = read» логики.
func (s *Service) querySeriesReadCount(ctx context.Context, seriesID, userID int64) (int, error) {
	var n int
	// DISTINCT по работе — прочитанное издание считает книгу один раз.
	err := s.pool.QueryRow(ctx, `
		SELECT count(DISTINCT COALESCE(b.work_id, -b.id))
		FROM books b
		JOIN reads r ON r.book_id = b.id AND r.completed_at IS NOT NULL
		WHERE b.series_id = $1 AND b.deleted = false AND r.user_id = $2
	`, seriesID, userID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("query series read count: %w", err)
	}
	return n, nil
}

func fullName(last, first, middle string) string {
	parts := make([]string, 0, 3)
	if last != "" {
		parts = append(parts, last)
	}
	if first != "" {
		parts = append(parts, first)
	}
	if middle != "" {
		parts = append(parts, middle)
	}
	return strings.Join(parts, " ")
}
