package importer_test

import (
	"context"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/importer"
	"github.com/skriptes/skriptes/backend/internal/inpx/inpxtest"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestImport_WorkPicksUpNewSeries — #275: выпуск INPX впервые проставил серию уже
// импортированной книге — серия должна дойти до работы (раньше чинилась только
// исчезнувшая серия, новая оставалась у одного издания). Авторский цикл важнее
// межавторской серии; ручная правка серии не трогается.
func TestImport_WorkPicksUpNewSeries(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool, _ := testpg.Start(t, ctx)
	mgr := startMeilisearch(t, ctx)
	imp := importer.New(importer.Deps{Pool: pool, Meili: mgr})
	dir := t.TempDir()

	q := func(sql string, args ...any) int64 {
		t.Helper()
		var v int64
		require.NoError(t, pool.QueryRow(ctx, sql, args...).Scan(&v))
		return v
	}
	run := func(books []inpxtest.Book) {
		t.Helper()
		path, err := inpxtest.WriteINPX(dir, "lib.inpx", books)
		require.NoError(t, err)
		_, err = imp.Run(ctx, path)
		require.NoError(t, err)
	}
	workSeries := func(lib string) (int64, int64) {
		t.Helper()
		var sid, sno *int64
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT w.series_id, w.ser_no FROM works w JOIN books b ON b.work_id = w.id WHERE b.lib_id = $1`, lib).Scan(&sid, &sno))
		var s, n int64
		if sid != nil {
			s = *sid
		}
		if sno != nil {
			n = *sno
		}
		return s, n
	}

	base := []inpxtest.Book{
		{LibID: "820001", Title: "Стрелок", Authors: []string{"Кинг,Стивен"}},
		{LibID: "820002", Title: "Извлечение троих", Authors: []string{"Кинг,Стивен"}, Series: "Тёмная башня", SerNo: 2},
	}
	run(base)
	s, _ := workSeries("820001")
	require.Zero(t, s, "в первом выпуске серии нет")

	// Следующий выпуск проставил серию первой книге.
	next := []inpxtest.Book{
		{LibID: "820001", Title: "Стрелок", Authors: []string{"Кинг,Стивен"}, Series: "Тёмная башня", SerNo: 1},
		base[1],
	}
	run(next)
	bookSeries := q(`SELECT series_id FROM books WHERE lib_id = '820001'`)
	s, n := workSeries("820001")
	require.Equal(t, bookSeries, s, "работа подхватила серию, впервые проставленную выпуском")
	require.Equal(t, int64(1), n)

	// Работа из двух изданий: одно в межавторской серии, другое — в авторском
	// цикле. Представитель — цикл, даже с большим номером.
	cycle := q(`SELECT series_id FROM books WHERE lib_id = '820002'`)
	multi := q(`INSERT INTO series (title, normalized_title, kind) VALUES ('Мини-Шарм', 'мини-шарм', 'multi') RETURNING id`)
	w1 := q(`SELECT work_id FROM books WHERE lib_id = '820001'`)
	_, err := pool.Exec(ctx, `UPDATE books SET series_id = $1, ser_no = 1 WHERE lib_id = '820001'`, multi)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE books SET work_id = $1 WHERE lib_id = '820002'`, w1)
	require.NoError(t, err)
	ids, err := imp.SyncWorkSeries(ctx)
	require.NoError(t, err)
	require.Contains(t, ids, w1)
	s, n = workSeries("820001")
	require.Equal(t, cycle, s, "авторский цикл важнее межавторской серии")
	require.Equal(t, int64(2), n)

	// Повторный проход ничего не меняет.
	ids, err = imp.SyncWorkSeries(ctx)
	require.NoError(t, err)
	require.Empty(t, ids)

	// #480: из двух циклов — цикл издания на языке названия работы, даже с
	// большим номером: английское издание в «Dark Tower» с №1 не побеждает
	// русское «Тёмная башня» с №2.
	en := q(`INSERT INTO series (title, normalized_title) VALUES ('Dark Tower', 'dark tower') RETURNING id`)
	_, err = pool.Exec(ctx, `UPDATE books SET series_id = $1, ser_no = 1, lang = 'en', title = 'The Gunslinger',
		normalized_title = 'the gunslinger' WHERE lib_id = '820001'`, en)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE books SET lang = 'ru' WHERE lib_id = '820002'`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE works SET title = 'Извлечение троих', normalized_title = 'извлечение троих' WHERE id = $1`, w1)
	require.NoError(t, err)
	_, err = imp.SyncWorkSeries(ctx)
	require.NoError(t, err)
	s, n = workSeries("820001")
	require.Equal(t, cycle, s, "цикл на языке названия работы важнее меньшего номера")
	require.Equal(t, int64(2), n)

	// Ручная правка серии работы (оверрайд) не перетирается.
	_, err = pool.Exec(ctx, `INSERT INTO metadata_overrides (target_kind, target_id, field, override_value, original_value)
		VALUES ('work', $1, 'series', '{}'::jsonb, '{}'::jsonb)`, w1)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE works SET series_id = NULL, ser_no = NULL WHERE id = $1`, w1)
	require.NoError(t, err)
	ids, err = imp.SyncWorkSeries(ctx)
	require.NoError(t, err)
	require.NotContains(t, ids, w1)
	s, _ = workSeries("820001")
	require.Zero(t, s)
}
