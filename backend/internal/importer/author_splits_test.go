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

// TestImport_AuthorNamesakesSplit — выпуск INPX начал различать тёзок уточнением
// в скобках (librusec 2026-09). Прежняя запись достаётся наследнику (больше книг /
// активность при ничьей / вариант без уточнения), остальные тёзки — новые авторы;
// подписки остаются, обогащение разделившихся сбрасывается, основной автор работ и
// опустевшие серии чинятся; повторный и «старый» импорт ничего не ломают.
func TestImport_AuthorNamesakesSplit(t *testing.T) {
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
	exec := func(sql string, args ...any) {
		t.Helper()
		_, err := pool.Exec(ctx, sql, args...)
		require.NoError(t, err)
	}
	authorOf := func(lib string) int64 {
		t.Helper()
		return q(`SELECT ba.author_id FROM book_authors ba JOIN books b ON b.id = ba.book_id
			WHERE b.lib_id = $1 ORDER BY ba.position LIMIT 1`, lib)
	}
	noteOf := func(id int64) string {
		t.Helper()
		var n *string
		require.NoError(t, pool.QueryRow(ctx, `SELECT name_note FROM authors WHERE id = $1`, id).Scan(&n))
		if n == nil {
			return ""
		}
		return *n
	}

	// ── июльский выпуск: тёзки склеены ──
	july := []inpxtest.Book{
		{LibID: "800001", Title: "Беседа 1", Authors: []string{"Антоний"}, Series: "Беседы", SerNo: 1},
		{LibID: "800002", Title: "Беседа 2", Authors: []string{"Антоний"}, Series: "Беседы", SerNo: 2},
		{LibID: "800003", Title: "Беседа 3", Authors: []string{"Антоний"}},
		{LibID: "800004", Title: "Беседа 4", Authors: []string{"Антоний"}},
		{LibID: "800005", Title: "Проповедь", Authors: []string{"Антоний"}, Series: "Проповеди", SerNo: 1},
		{LibID: "800006", Title: "Нейромант", Authors: []string{"Гибсон,Уильям"}},
		{LibID: "800009", Title: "Стихи", Authors: []string{"Петров,Пётр"}},
		{LibID: "800010", Title: "Химия", Authors: []string{"Петров,Пётр"}},
		{LibID: "800011", Title: "Роман 1", Authors: []string{"Сидоров,Сидор"}},
		{LibID: "800012", Title: "Роман 2", Authors: []string{"Сидоров,Сидор"}},
		{LibID: "800013", Title: "Репортаж", Authors: []string{"Сидоров,Сидор"}},
	}
	path, err := inpxtest.WriteINPX(dir, "librusec.inpx", july)
	require.NoError(t, err)
	_, err = imp.Run(ctx, path)
	require.NoError(t, err)

	antony, gibson, petrov, sidorov := authorOf("800001"), authorOf("800006"), authorOf("800009"), authorOf("800011")
	require.Equal(t, antony, authorOf("800005"), "в июле — одна запись")
	user := q(`INSERT INTO users (email, display_name, password_hash, role) VALUES ('u@x','U','h','user') RETURNING id`)
	exec(`INSERT INTO favorite_authors (user_id, author_id) VALUES ($1, $2)`, user, antony)
	exec(`UPDATE authors SET bio = 'чья-то биография', photo_path = 'p.jpg', metadata_fetched_at = now()
		WHERE id = ANY($1)`, []int64{antony, gibson, sidorov})
	// Ничья у Петрова (по книге на тёзку): читают «Химию» — её автор и наследует запись.
	exec(`INSERT INTO reads (user_id, book_id, fraction) SELECT $1, id, 0.3 FROM books WHERE lib_id = '800010'`, user)
	oldProповеди := q(`SELECT series_id FROM books WHERE lib_id = '800005'`)

	// ── сентябрьский выпуск: тёзки различены ──
	sept := []inpxtest.Book{
		{LibID: "800001", Title: "Беседа 1", Authors: []string{"Антоний [Блум]"}, Series: "Беседы", SerNo: 1},
		{LibID: "800002", Title: "Беседа 2", Authors: []string{"Антоний [Блум]"}, Series: "Беседы", SerNo: 2},
		{LibID: "800003", Title: "Беседа 3", Authors: []string{"Антоний [Блум]"}},
		{LibID: "800004", Title: "Беседа 4", Authors: []string{"Антоний [Блум]"}},
		{LibID: "800005", Title: "Проповедь", Authors: []string{"Антоний [Храповицкий]"}, Series: "Проповеди", SerNo: 1},
		{LibID: "800008", Title: "Житие", Authors: []string{"Антоний [Бочков]"}},
		{LibID: "800006", Title: "Нейромант", Authors: []string{"Гибсон [фантаст],Уильям"}},
		{LibID: "800009", Title: "Стихи", Authors: []string{"Петров [поэт],Пётр"}},
		{LibID: "800010", Title: "Химия", Authors: []string{"Петров [химик],Пётр"}},
		{LibID: "800011", Title: "Роман 1", Authors: []string{"Сидоров,Сидор"}},
		{LibID: "800012", Title: "Роман 2", Authors: []string{"Сидоров,Сидор"}},
		{LibID: "800013", Title: "Репортаж", Authors: []string{"Сидоров [журналист],Сидор"}},
	}
	_, err = inpxtest.WriteINPX(dir, "librusec.inpx", sept)
	require.NoError(t, err)
	_, err = imp.Run(ctx, path)
	require.NoError(t, err)

	// Антоний: Блум — 4 книги из 5 → наследник, прежний id и подписка.
	require.Equal(t, antony, authorOf("800001"))
	require.Equal(t, "Блум", noteOf(antony))
	hrap, bochkov := authorOf("800005"), authorOf("800008")
	require.NotEqual(t, antony, hrap)
	require.NotEqual(t, antony, bochkov)
	require.Equal(t, "Храповицкий", noteOf(hrap))
	require.Equal(t, "Бочков", noteOf(bochkov))
	require.Equal(t, int64(1), q(`SELECT count(*) FROM favorite_authors WHERE author_id = $1`, antony))
	var bio, photo *string
	var fetched *time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT bio, photo_path, metadata_fetched_at FROM authors WHERE id = $1`, antony).
		Scan(&bio, &photo, &fetched))
	require.Nil(t, bio, "разделился — обогащение сброшено")
	require.Nil(t, photo)
	require.Nil(t, fetched)
	require.Equal(t, hrap, q(`SELECT w.primary_author_id FROM works w JOIN books b ON b.work_id = w.id WHERE b.lib_id = '800005'`),
		"основной автор работы ушёл вместе с книгой")
	require.Zero(t, q(`SELECT count(*) FROM series WHERE id = $1`, oldProповеди), "опустевшая серия прежней записи удалена")

	// Гибсон: просто получил уточнение — не разделение, обогащение на месте.
	require.Equal(t, gibson, authorOf("800006"))
	require.Equal(t, "фантаст", noteOf(gibson))
	require.Equal(t, int64(1), q(`SELECT count(*) FROM authors WHERE id = $1 AND bio IS NOT NULL`, gibson))

	// Петров: ничья 1:1 → наследник тот, чью книгу читают.
	require.Equal(t, petrov, authorOf("800010"))
	require.Equal(t, "химик", noteOf(petrov))
	require.NotEqual(t, petrov, authorOf("800009"))

	// Сидоров: вариант без уточнения — большинство, остаётся прежней записью.
	require.Equal(t, sidorov, authorOf("800011"))
	require.Equal(t, "", noteOf(sidorov))
	require.NotEqual(t, sidorov, authorOf("800013"))
	require.Equal(t, "журналист", noteOf(authorOf("800013")))

	reasons := map[string]string{}
	rows, err := pool.Query(ctx, `SELECT base_name || '|' || note, reason FROM author_splits WHERE is_heir`)
	require.NoError(t, err)
	for rows.Next() {
		var k, r string
		require.NoError(t, rows.Scan(&k, &r))
		reasons[k] = r
	}
	require.NoError(t, rows.Err())
	rows.Close()
	require.Equal(t, map[string]string{
		"антоний|Блум": "majority", "гибсон уильям|фантаст": "majority",
		"петров пётр|химик": "tie:activity", "сидоров сидор|": "majority",
	}, reasons)
	splits := q(`SELECT count(*) FROM author_splits`)

	// Повторный импорт изменённого файла: имена уже разделены — журнал не растёт.
	sept = append(sept, inpxtest.Book{LibID: "800014", Title: "Новая беседа", Authors: []string{"Антоний [Блум]"}})
	_, err = inpxtest.WriteINPX(dir, "librusec.inpx", sept)
	require.NoError(t, err)
	_, err = imp.Run(ctx, path)
	require.NoError(t, err)
	require.Equal(t, splits, q(`SELECT count(*) FROM author_splits`))
	require.Equal(t, antony, authorOf("800014"))

	// Файл старого выпуска (без уточнений): «Антоний» ложится на наследника,
	// «Сидоров» — на прежнюю запись, лишних авторов не появляется.
	authors := q(`SELECT count(*) FROM authors`)
	legacy, err := inpxtest.WriteINPX(dir, "legacy.inpx", []inpxtest.Book{
		{LibID: "800020", Title: "Старая книга", Authors: []string{"Антоний"}},
		{LibID: "800021", Title: "Старый роман", Authors: []string{"Сидоров,Сидор"}},
	})
	require.NoError(t, err)
	_, err = imp.Run(ctx, legacy)
	require.NoError(t, err)
	require.Equal(t, antony, authorOf("800020"))
	require.Equal(t, sidorov, authorOf("800021"))
	require.Equal(t, authors, q(`SELECT count(*) FROM authors`))
}
