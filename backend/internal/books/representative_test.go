package books_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/books"
	"github.com/skriptes/skriptes/backend/internal/importer"
	"github.com/stretchr/testify/require"
)

// Единый представитель издания (прод-аудит P1 #3/#4): список /books обязан
// показывать язык / обложку / внешний рейтинг ТОГО ЖЕ издания, что откроется
// на карточке (каскад representativeEditions == GetWork). До фикса список брал
// язык как union[0] (алфавитно → «en» у русских переводов, включая скрытый
// пользователем язык) и рейтинг как MAX по изданиям — расходясь с карточкой.
func TestListWorks_RepresentativeEdition(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	pool := startPostgres(t, ctx)
	mgr := startMeilisearch(t, ctx)
	imp := importer.New(importer.Deps{Pool: pool, Meili: mgr})
	abs, err := filepath.Abs(fixtureINPX)
	require.NoError(t, err)
	_, err = imp.Run(ctx, abs)
	require.NoError(t, err)
	svc := books.New(pool, mgr, nil)

	// Русское издание из фикстуры — якорь своей работы (title == works.title).
	var ruID, workID, collID, archID int64
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT b.id, b.work_id, b.collection_id, b.archive_id
		FROM books b JOIN works w ON w.id = b.work_id
		WHERE b.deleted = false AND b.lang = 'ru' AND b.normalized_title = w.normalized_title
		ORDER BY b.id LIMIT 1`).Scan(&ruID, &workID, &collID, &archID))
	// LIBRATE-рейтинг у русского издания.
	_, err = pool.Exec(ctx, `UPDATE books SET rating = 3 WHERE id = $1`, ruID)
	require.NoError(t, err)

	// Английское издание той же работы: алфавитно-первый язык (union[0]-баг),
	// ЕДИНСТВЕННАЯ обложка работы и более высокий внешний рейтинг.
	var enID int64
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title,
		                   lang, work_id, cover_path, external_rating, external_rating_source)
		VALUES ($1, $2, 'EN1', 'en1.fb2', 'fb2', 'The English Edition', 'the english edition',
		        'en', $3, 'en-cover.jpg', 5.0, 'google_books')
		RETURNING id`, collID, archID, workID).Scan(&enID))
	// Колонка works.edition_count НАРОЧНО кривая (99): бейдж обязан считать
	// живые издания сам, а не читать потенциально устаревшую колонку (она не
	// пересчитывается после soft-delete импорта до следующей группировки).
	_, err = pool.Exec(ctx, `UPDATE works SET edition_count = 99 WHERE id = $1`, workID)
	require.NoError(t, err)
	// Meili-док работы получает langs = union (en, ru) — почва для union[0]-бага.
	require.NoError(t, imp.UpsertWorksToIndex(ctx, []int64{workID}))

	findItem := func(res books.ListResponse) *books.ListItem {
		for i := range res.Items {
			if res.Items[i].ID == workID {
				return &res.Items[i]
			}
		}
		return nil
	}

	// ── Без скрытий: представитель = русский якорь (якорь бьёт обложку в
	//    каскаде) → список показывает ЕГО язык и рейтинг, не union[0]/MAX.
	res, err := svc.ListWorks(ctx, books.ListParams{Limit: 50})
	require.NoError(t, err)
	it := findItem(res)
	require.NotNil(t, it, "работа должна быть в выдаче")
	require.Equal(t, "ru", it.Lang, "язык списка = язык представителя (не алфавитный union[0])")
	require.NotNil(t, it.ExternalRating)
	require.EqualValues(t, 3, *it.ExternalRating, "рейтинг списка = рейтинг представителя (не MAX по изданиям)")
	require.NotNil(t, it.ExternalRatingSource)
	require.Equal(t, "library", *it.ExternalRatingSource)
	// Обложки у якоря нет → fallback на единственную обложку работы (как карточка).
	require.Equal(t, "en-cover.jpg", it.CoverPath)
	require.Equal(t, enID, it.CoverEditionID)
	require.Equal(t, 2, it.EditionCount, "живой счёт изданий, не устаревшая колонка works.edition_count")

	// Консистентность с карточкой: GetWork выбирает то же издание.
	card, err := svc.GetWork(ctx, workID, nil, nil)
	require.NoError(t, err)
	require.Equal(t, ruID, card.ID, "карточка открывает то же представительное издание")
	require.Equal(t, it.Lang, card.Lang, "язык списка == языку карточки")

	// ── Скрытие ru: видимый представитель — английское издание; список и
	//    карточка синхронно переключаются на него.
	resEn, err := svc.ListWorks(ctx, books.ListParams{Limit: 50, ExcludeLangs: []string{"ru"}})
	require.NoError(t, err)
	itEn := findItem(resEn)
	require.NotNil(t, itEn, "работа видна — есть издание на видимом языке")
	require.Equal(t, "en", itEn.Lang)
	require.NotNil(t, itEn.ExternalRating)
	require.EqualValues(t, 5, *itEn.ExternalRating)
	require.NotNil(t, itEn.ExternalRatingSource)
	require.Equal(t, "google_books", *itEn.ExternalRatingSource)
	// Бейдж «N изданий» = ВИДИМЫЕ издания (прод-кейс «Разум и чувства»: бейдж
	// обещал «4 изданий», карточка при скрытых языках показывала одно без
	// секции «Издания»). ru скрыт → бейдж 1, консистентно с карточкой.
	require.Equal(t, 1, itEn.EditionCount, "бейдж = видимые издания при скрытом ru")
	cardEn, err := svc.GetWork(ctx, workID, nil, []string{"ru"})
	require.NoError(t, err)
	require.Equal(t, enID, cardEn.ID, "карточка при скрытии ru — английское издание")
	require.Equal(t, itEn.Lang, cardEn.Lang)
	require.Len(t, cardEn.Editions, 1, "карточка показывает столько же изданий, сколько обещал бейдж")
}
