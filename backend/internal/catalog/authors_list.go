package catalog

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

// authors_list.go — раздел «Авторы» (GET /api/authors): постраничный список
// авторов с фильтрами и агрегатами. В отличие от ListAuthors (enumerate.go,
// лёгкий OPDS-список «id + имя + счётчик»), здесь собираем богатую карточку
// строки: избранное, языки, годы активности, экранизации, рейтинг, топ-жанры.
//
// Опираемся ТОЛЬКО на уже имеющиеся данные (новых внешних обогащений раздел НЕ
// добавляет). Meili-индекс авторов НЕ заводим — авторов на порядки меньше книг
// (462k книг ↔ десятки тысяч авторов), PG-агрегаты с подзапросами достаточно
// быстры и не плодят N+1.

// AuthorListItem — строка в списке авторов с агрегатами.
type AuthorListItem struct {
	ID        int64  `json:"id"`
	FullName  string `json:"full_name"`
	PhotoPath string `json:"photo_path,omitempty"`
	// BookCount — число ЛОГИЧЕСКИХ книг (работ) автора (видимых; скрытый
	// контент исключён). DISTINCT по work_id, чтобы издания не двоили счёт.
	BookCount int `json:"book_count"`
	// IsFavorite — текущий юзер подписан на автора (favorite_authors).
	IsFavorite bool `json:"is_favorite"`
	// FavoritedBooksCount — сколько книг этого автора у юзера в избранном
	// (favorites → book_authors). DISTINCT по работе.
	FavoritedBooksCount int `json:"favorited_books_count"`
	// TopGenres — топ-жанров автора по числу книг (как в карточке автора).
	TopGenres []GenreCount `json:"top_genres,omitempty"`
	// Languages — языки изданий автора (lang + src_lang), нормализованные
	// lower+trim (см. граблю №14), по убыванию числа книг.
	Languages []string `json:"languages,omitempty"`
	// YearsActive — мин/макс год НАПИСАНИЯ (written_year) среди книг автора.
	// written_year ≠ date_added (граблю №3). nil, если год нигде не извлечён.
	YearsActive *YearsRange `json:"years_active,omitempty"`
	// HasAdaptations — есть ли экранизация хоть у одной книги автора.
	HasAdaptations bool `json:"has_adaptations"`
	// LibraryRating — БИБЛИОТЕЧНЫЙ рейтинг (LIBRATE из INPX, books.rating), а
	// НЕ пользовательский. Берём максимум по книгам автора. nil, если рейтинга
	// нет ни у одной книги.
	LibraryRating *int `json:"library_rating,omitempty"`
}

// YearsRange — диапазон лет активности автора (по written_year).
type YearsRange struct {
	From int `json:"from"`
	To   int `json:"to"`
}

// AuthorListParams — параметры фильтрации/пагинации списка авторов.
type AuthorListParams struct {
	// UserID — текущий пользователь (для is_favorite / favorited_books_count);
	// 0 = аноним (поля остаются пустыми).
	UserID int64

	Query          string   // ILIKE по authors.normalized_name (префикс)
	Genres         []string // авторы, писавшие хотя бы в одном из этих жанров (fb2_code)
	Langs          []string // авторы с хотя бы одной книгой на этих языках (lang/src_lang)
	YearFrom       int      // пересечение [year_from, year_to] с диапазоном лет активности
	YearTo         int
	HasAdaptations bool // только авторы, у книг которых есть экранизации
	MinRating      int  // минимальный библиотечный рейтинг (max по книгам автора ≥ этого)
	FavoritesOnly  bool // только авторы из favorite_authors текущего юзера

	Sort   string // "name" (дефолт) | "book_count" | "rating"
	Limit  int    // ≤ 500, дефолт 50
	Offset int

	// Исключения видимости контента (admin ∪ персональные): авторы остаются
	// в списке, но агрегаты (счётчики/жанры/языки/годы) считаются по ВИДИМЫМ
	// книгам — как на карточке автора/серии (граблю №14).
	ExcludeGenres []string
	ExcludeLangs  []string
}

// AuthorListResult — страница списка авторов + общее число (для пагинации).
type AuthorListResult struct {
	Items []AuthorListItem `json:"items"`
	Total int              `json:"total"`
}

// ListAuthorsFiltered — постраничный список авторов с фильтрами и агрегатами.
//
// Стратегия: один запрос с агрегирующими подзапросами на автора (без N+1) даёт
// строки списка; топ-жанры добираются ОДНИМ батч-запросом по найденным id
// (window-функция, ≤5 жанров на автора). Фильтры режут множество авторов через
// EXISTS-подзапросы по видимым книгам.
func (s *Service) ListAuthorsFiltered(ctx context.Context, p AuthorListParams) (AuthorListResult, error) {
	p.Limit, p.Offset = sanitizePaging(p.Limit, p.Offset)

	// Аргументы накапливаем по мере построения запроса; каждый addArg отдаёт
	// номер плейсхолдера и кладёт значение в args. КАЖДЫЙ переданный аргумент
	// обязан быть упомянут в тексте запроса (PG не выводит тип неупомянутого
	// $N) — поэтому исключения видимости рендерим фрагментом со СВЕЖИМИ
	// плейсхолдерами на каждом месте использования (renderExclusion), а не
	// общим $1 (иначе в COUNT-запросе без контент-фильтров $1 повис бы
	// неупомянутым). Дублирование slice-аргумента исключений по местам
	// дёшево (массив кодов мал).
	args := make([]any, 0, 24)
	nextArg := 1
	addArg := func(v any) int {
		args = append(args, v)
		n := nextArg
		nextArg++
		return n
	}
	// renderExclusion — фрагмент " AND (lang…) AND NOT EXISTS(genre…)" по алиасу
	// `b`, с собственными плейсхолдерами (аргументы доклеиваются в args). Пусто,
	// если ни язык, ни жанр не скрыты (no-op, безопасно звать всегда).
	renderExclusion := func() string {
		var sb strings.Builder
		if len(p.ExcludeLangs) > 0 {
			n := addArg(p.ExcludeLangs)
			fmt.Fprintf(&sb, " AND (b.lang IS NULL OR NOT (b.lang = ANY($%d::text[])))", n)
		}
		if len(p.ExcludeGenres) > 0 {
			n := addArg(p.ExcludeGenres)
			fmt.Fprintf(&sb, " AND NOT EXISTS (SELECT 1 FROM book_genres bgx JOIN genres gx ON gx.id = bgx.genre_id"+
				" WHERE bgx.book_id = b.id AND gx.fb2_code = ANY($%d::text[]))", n)
		}
		return sb.String()
	}

	// where — условия-фильтры для авторов (склеиваются через AND). Каждый
	// предикат — EXISTS по видимым книгам автора (либо строка автора). Эти
	// предикаты ОБЩИЕ для COUNT и главного запроса (одни плейсхолдеры).
	var where []string

	if q := strings.TrimSpace(p.Query); q != "" {
		// Префиксный ILIKE по normalized_name (как в SuggestAuthors): GIN
		// trigram index ускоряет на длинных запросах.
		n := addArg(q)
		where = append(where, fmt.Sprintf("a.normalized_name::text ILIKE $%d || '%%'", n))
	}

	if p.FavoritesOnly && p.UserID > 0 {
		n := addArg(p.UserID)
		where = append(where, fmt.Sprintf(
			"EXISTS (SELECT 1 FROM favorite_authors fa WHERE fa.author_id = a.id AND fa.user_id = $%d)", n))
	}

	if len(p.Genres) > 0 {
		n := addArg(p.Genres)
		where = append(where, fmt.Sprintf(
			"EXISTS (SELECT 1 FROM book_authors ba JOIN books b ON b.id = ba.book_id AND b.deleted = false"+
				" JOIN book_genres bg ON bg.book_id = b.id JOIN genres g ON g.id = bg.genre_id"+
				" WHERE ba.author_id = a.id AND g.fb2_code = ANY($%d::text[])"+renderExclusion()+")", n))
	}

	if len(p.Langs) > 0 {
		n := addArg(p.Langs)
		// Язык оригинала или язык издания: книга совпадает, если её lang ИЛИ
		// src_lang (нормализованные) попали в выбранные. Нормализуем на лету
		// (lower+btrim) — defensive, хотя импорт уже нормализует lang.
		where = append(where, fmt.Sprintf(
			"EXISTS (SELECT 1 FROM book_authors ba JOIN books b ON b.id = ba.book_id AND b.deleted = false"+
				" WHERE ba.author_id = a.id"+
				" AND (lower(btrim(b.lang)) = ANY($%d::text[]) OR lower(btrim(b.src_lang)) = ANY($%d::text[]))"+
				renderExclusion()+")", n, n))
	}

	if p.YearFrom > 0 || p.YearTo > 0 {
		// Пересечение диапазона активности автора [min,max] с [from,to]:
		// существует видимая книга автора с written_year в [from,to].
		lo, hi := p.YearFrom, p.YearTo
		yearCond := "b.written_year IS NOT NULL"
		if lo > 0 {
			n := addArg(lo)
			yearCond += fmt.Sprintf(" AND b.written_year >= $%d", n)
		}
		if hi > 0 {
			n := addArg(hi)
			yearCond += fmt.Sprintf(" AND b.written_year <= $%d", n)
		}
		where = append(where, "EXISTS (SELECT 1 FROM book_authors ba JOIN books b ON b.id = ba.book_id AND b.deleted = false"+
			" WHERE ba.author_id = a.id AND "+yearCond+renderExclusion()+")")
	}

	if p.HasAdaptations {
		where = append(where,
			"EXISTS (SELECT 1 FROM book_authors ba JOIN books b ON b.id = ba.book_id AND b.deleted = false"+
				" JOIN book_adaptations ad ON ad.book_id = b.id WHERE ba.author_id = a.id"+renderExclusion()+")")
	}

	if p.MinRating > 0 {
		n := addArg(p.MinRating)
		where = append(where, fmt.Sprintf(
			"EXISTS (SELECT 1 FROM book_authors ba JOIN books b ON b.id = ba.book_id AND b.deleted = false"+
				" WHERE ba.author_id = a.id AND b.rating >= $%d"+renderExclusion()+")", n))
	}

	whereSQL := ""
	if len(where) > 0 {
		whereSQL = " WHERE " + strings.Join(where, " AND ")
	}

	// total — число авторов, прошедших фильтры (для пагинации). Использует ровно
	// те аргументы, что упомянуты в whereSQL: на этом этапе args содержит только
	// фильтр-аргументы (limit/offset/user/агрегатные исключения добавятся ниже).
	nFilterArgs := len(args)
	var total int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM authors a`+whereSQL, args[:nFilterArgs]...,
	).Scan(&total); err != nil {
		return AuthorListResult{}, fmt.Errorf("count authors: %w", err)
	}

	// ORDER BY — по выбранной сортировке. book_count/rating считаются в
	// SELECT-агрегатах ниже; сортируем по тем же выражениям (алиасы).
	orderSQL := "ORDER BY a.last_name, a.first_name, a.middle_name, a.id"
	switch p.Sort {
	case "book_count":
		orderSQL = "ORDER BY book_count DESC, a.last_name, a.id"
	case "rating":
		orderSQL = "ORDER BY library_rating DESC NULLS LAST, book_count DESC, a.id"
	}

	// Главный запрос: на каждого автора — агрегаты подзапросами. userID для
	// is_favorite/favorited_books_count — отдельный аргумент (0 у анонима →
	// подзапрос даст false/0). Исключения в каждом агрегате рендерим свежими
	// плейсхолдерами (renderExclusion).
	userN := addArg(p.UserID)
	exBookCount := renderExclusion()
	exFavBooks := renderExclusion()
	exYrFrom := renderExclusion()
	exYrTo := renderExclusion()
	exAdapt := renderExclusion()
	exRating := renderExclusion()
	limitN := addArg(p.Limit)
	offsetN := addArg(p.Offset)

	query := fmt.Sprintf(`
		SELECT a.id, a.last_name, a.first_name, a.middle_name, a.photo_path,
		       (SELECT count(DISTINCT COALESCE(b.work_id, -b.id))
		          FROM book_authors ba JOIN books b ON b.id = ba.book_id
		          WHERE ba.author_id = a.id AND b.deleted = false%[2]s)::int AS book_count,
		       EXISTS (SELECT 1 FROM favorite_authors fa
		               WHERE fa.author_id = a.id AND fa.user_id = $%[1]d) AS is_favorite,
		       (SELECT count(DISTINCT COALESCE(b.work_id, -b.id))
		          FROM favorites f JOIN book_authors ba ON ba.book_id = f.book_id
		          JOIN books b ON b.id = f.book_id
		          WHERE ba.author_id = a.id AND b.deleted = false AND f.user_id = $%[1]d%[3]s)::int AS fav_books,
		       (SELECT min(b.written_year) FROM book_authors ba JOIN books b ON b.id = ba.book_id
		          WHERE ba.author_id = a.id AND b.deleted = false AND b.written_year IS NOT NULL%[4]s) AS yr_from,
		       (SELECT max(b.written_year) FROM book_authors ba JOIN books b ON b.id = ba.book_id
		          WHERE ba.author_id = a.id AND b.deleted = false AND b.written_year IS NOT NULL%[5]s) AS yr_to,
		       EXISTS (SELECT 1 FROM book_authors ba JOIN books b ON b.id = ba.book_id AND b.deleted = false
		               JOIN book_adaptations ad ON ad.book_id = b.id WHERE ba.author_id = a.id%[6]s) AS has_adapt,
		       (SELECT max(b.rating)::int FROM book_authors ba JOIN books b ON b.id = ba.book_id
		          WHERE ba.author_id = a.id AND b.deleted = false AND b.rating IS NOT NULL%[7]s) AS library_rating
		FROM authors a%[8]s
		%[9]s
		LIMIT $%[10]d OFFSET $%[11]d
	`, userN, exBookCount, exFavBooks, exYrFrom, exYrTo, exAdapt, exRating, whereSQL, orderSQL, limitN, offsetN)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return AuthorListResult{}, fmt.Errorf("list authors: %w", err)
	}
	defer rows.Close()

	out := make([]AuthorListItem, 0, p.Limit)
	ids := make([]int64, 0, p.Limit)
	for rows.Next() {
		var (
			it                  AuthorListItem
			last, first, middle string
			photo               pgtype.Text
			yrFrom, yrTo        pgtype.Int2
			rating              pgtype.Int4
		)
		if err := rows.Scan(&it.ID, &last, &first, &middle, &photo,
			&it.BookCount, &it.IsFavorite, &it.FavoritedBooksCount,
			&yrFrom, &yrTo, &it.HasAdaptations, &rating); err != nil {
			return AuthorListResult{}, fmt.Errorf("scan author: %w", err)
		}
		it.FullName = fullName(last, first, middle)
		if photo.Valid {
			it.PhotoPath = photo.String
		}
		if yrFrom.Valid && yrTo.Valid {
			it.YearsActive = &YearsRange{From: int(yrFrom.Int16), To: int(yrTo.Int16)}
		}
		if rating.Valid {
			v := int(rating.Int32)
			it.LibraryRating = &v
		}
		out = append(out, it)
		ids = append(ids, it.ID)
	}
	if err := rows.Err(); err != nil {
		return AuthorListResult{}, err
	}

	// Топ-жанры и языки — батч-запросами по найденным id (без N+1).
	if len(ids) > 0 {
		idx := make(map[int64]int, len(ids))
		for i, id := range ids {
			idx[id] = i
		}
		if err := s.fillAuthorTopGenres(ctx, ids, idx, out, p.ExcludeGenres, p.ExcludeLangs); err != nil {
			return AuthorListResult{}, err
		}
		if err := s.fillAuthorLanguages(ctx, ids, idx, out, p.ExcludeGenres, p.ExcludeLangs); err != nil {
			return AuthorListResult{}, err
		}
	}

	return AuthorListResult{Items: out, Total: total}, nil
}

// fillAuthorTopGenres — батч-добор топ-5 жанров на каждого автора из ids одним
// запросом (window ROW_NUMBER по убыванию числа книг). Результат раскладывается
// по out через idx (author_id → индекс).
func (s *Service) fillAuthorTopGenres(
	ctx context.Context, ids []int64, idx map[int64]int, out []AuthorListItem,
	excludeGenres, excludeLangs []string,
) error {
	// $1 — массив author_id; исключения — $2.. (bookExclusionClause со startArg=2).
	exClause, exArgs := bookExclusionClause(2, excludeGenres, excludeLangs)
	args := append([]any{ids}, exArgs...)
	rows, err := s.pool.Query(ctx, `
		SELECT author_id, fb2_code, display, cnt FROM (
			SELECT ba.author_id,
			       g.fb2_code,
			       COALESCE(NULLIF(g.name_ru,''), NULLIF(g.name_en,''), g.fb2_code) AS display,
			       count(*) AS cnt,
			       ROW_NUMBER() OVER (PARTITION BY ba.author_id
			                          ORDER BY count(*) DESC, g.fb2_code) AS rn
			FROM book_authors ba
			JOIN books b ON b.id = ba.book_id AND b.deleted = false
			JOIN book_genres bg ON bg.book_id = b.id
			JOIN genres g ON g.id = bg.genre_id
			WHERE ba.author_id = ANY($1::bigint[])`+exClause+`
			GROUP BY ba.author_id, g.fb2_code, g.name_ru, g.name_en
		) t
		WHERE rn <= 5
		ORDER BY author_id, cnt DESC, fb2_code
	`, args...)
	if err != nil {
		return fmt.Errorf("query author top genres: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			authorID int64
			g        GenreCount
		)
		if err := rows.Scan(&authorID, &g.Code, &g.Display, &g.Count); err != nil {
			return err
		}
		if i, ok := idx[authorID]; ok {
			out[i].TopGenres = append(out[i].TopGenres, g)
		}
	}
	return rows.Err()
}

// fillAuthorLanguages — батч-добор языков (lang + src_lang) на каждого автора.
// Нормализуем lower+btrim (граблю №14) и группируем; порядок — по убыванию
// числа книг (популярный язык автора первым).
func (s *Service) fillAuthorLanguages(
	ctx context.Context, ids []int64, idx map[int64]int, out []AuthorListItem,
	excludeGenres, excludeLangs []string,
) error {
	exClause, exArgs := bookExclusionClause(2, excludeGenres, excludeLangs)
	args := append([]any{ids}, exArgs...)
	// UNION ALL по lang и src_lang: считаем каждый язык-источник как сигнал
	// «автор есть на этом языке». NULLIF('') отсекает пустые после btrim.
	rows, err := s.pool.Query(ctx, `
		SELECT author_id, code, count(*) AS cnt FROM (
			SELECT ba.author_id, NULLIF(lower(btrim(b.lang)), '') AS code
			FROM book_authors ba
			JOIN books b ON b.id = ba.book_id AND b.deleted = false
			WHERE ba.author_id = ANY($1::bigint[])`+exClause+`
			UNION ALL
			SELECT ba.author_id, NULLIF(lower(btrim(b.src_lang)), '') AS code
			FROM book_authors ba
			JOIN books b ON b.id = ba.book_id AND b.deleted = false
			WHERE ba.author_id = ANY($1::bigint[])`+exClause+`
		) t
		WHERE code IS NOT NULL
		GROUP BY author_id, code
		ORDER BY author_id, cnt DESC, code
	`, args...)
	if err != nil {
		return fmt.Errorf("query author languages: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			authorID int64
			code     string
			cnt      int
		)
		if err := rows.Scan(&authorID, &code, &cnt); err != nil {
			return err
		}
		if i, ok := idx[authorID]; ok {
			out[i].Languages = append(out[i].Languages, code)
		}
	}
	return rows.Err()
}
