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

// TestImport_MultiAuthorSeries — серия, под которой книги ≥3 разных авторов
// (издательская «Мини-Шарм»), — одна запись без автора (kind='multi'), а не
// «цикл» у каждого автора. Прежние «циклы» сливаются в неё вместе с подписками,
// работы переезжают; признак липкий; авторский цикл не трогается.
func TestImport_MultiAuthorSeries(t *testing.T) {
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
	seriesOf := func(lib string) int64 {
		t.Helper()
		return q(`SELECT series_id FROM books WHERE lib_id = $1`, lib)
	}
	run := func(books []inpxtest.Book) {
		t.Helper()
		path, err := inpxtest.WriteINPX(dir, "lib.inpx", books)
		require.NoError(t, err)
		_, err = imp.Run(ctx, path)
		require.NoError(t, err)
	}
	king := []inpxtest.Book{
		{LibID: "810001", Title: "Стрелок", Authors: []string{"Кинг,Стивен"}, Series: "Тёмная башня", SerNo: 1},
		{LibID: "810002", Title: "Извлечение троих", Authors: []string{"Кинг,Стивен"}, Series: "Тёмная башня", SerNo: 2},
		// Трое авторов, но у Стаута 80% книг — его цикл, не издательская серия.
		{LibID: "810031", Title: "Фер-де-ланс", Authors: []string{"Стаут,Рекс"}, Series: "Ниро Вульф", SerNo: 1},
		{LibID: "810032", Title: "Лига перепуганных", Authors: []string{"Стаут,Рекс"}, Series: "Ниро Вульф", SerNo: 2},
		{LibID: "810033", Title: "Резиновая лента", Authors: []string{"Стаут,Рекс"}, Series: "Ниро Вульф", SerNo: 3},
		{LibID: "810034", Title: "Красная шкатулка", Authors: []string{"Стаут,Рекс"}, Series: "Ниро Вульф", SerNo: 4},
		{LibID: "810037", Title: "Слишком много поваров", Authors: []string{"Стаут,Рекс"}, Series: "Ниро Вульф", SerNo: 5},
		{LibID: "810038", Title: "Бокал шампанского", Authors: []string{"Стаут,Рекс"}, Series: "Ниро Вульф", SerNo: 6},
		{LibID: "810039", Title: "Лучшие дома", Authors: []string{"Стаут,Рекс"}, Series: "Ниро Вульф", SerNo: 7},
		{LibID: "810040", Title: "Звонок в дверь", Authors: []string{"Стаут,Рекс"}, Series: "Ниро Вульф", SerNo: 8},
		// У одного автора больше половины, но меньше 80% — издательская серия
		// (прод: «Мировая классика» — Нагибин 58 из 78, #298).
		{LibID: "810071", Title: "Зимний дуб", Authors: []string{"Нагибин,Юрий"}, Series: "Мировая классика"},
		{LibID: "810072", Title: "Чистые пруды", Authors: []string{"Нагибин,Юрий"}, Series: "Мировая классика"},
		{LibID: "810073", Title: "Старая черепаха", Authors: []string{"Нагибин,Юрий"}, Series: "Мировая классика"},
		{LibID: "810074", Title: "Анна Каренина", Authors: []string{"Толстой,Лев"}, Series: "Мировая классика"},
		{LibID: "810075", Title: "Госпожа Бовари", Authors: []string{"Флобер,Гюстав"}, Series: "Мировая классика"},
		{LibID: "810035", Title: "Смерть в пиковом положении", Authors: []string{"Голдсборо,Роберт"}, Series: "Ниро Вульф"},
		{LibID: "810036", Title: "Пропавший", Authors: []string{"Другой,Автор"}, Series: "Ниро Вульф"},
		// «Рассказы» у четырёх авторов без доминирующего — жанровое слово, не
		// издательская серия: у каждого своя.
		{LibID: "810041", Title: "Анюта", Authors: []string{"Чехов,Антон,Павлович"}, Series: "Рассказы"},
		{LibID: "810042", Title: "Анна на шее", Authors: []string{"Чехов,Антон,Павлович"}, Series: "Рассказы"},
		{LibID: "810043", Title: "Головастик", Authors: []string{"Белаш,Александр"}, Series: "Рассказы"},
		{LibID: "810044", Title: "Приключения Иля", Authors: []string{"Алексеев,Иван"}, Series: "Рассказы"},
		{LibID: "810045", Title: "Маленькие рассказы", Authors: []string{"Булычев,Кир"}, Series: "Рассказы"},
		// Больше половины книг у служебного автора (журнала) — всё равно
		// издательская серия, не «цикл» журнала (#298).
		{LibID: "810051", Title: "Вокруг света, 1961 №1", Authors: []string{"Журнал «Вокруг света»"}, Series: "Вокруг света (журнал)"},
		{LibID: "810052", Title: "Вокруг света, 1961 №2", Authors: []string{"Журнал «Вокруг света»"}, Series: "Вокруг света (журнал)"},
		{LibID: "810053", Title: "Вокруг света, 1961 №3", Authors: []string{"Журнал «Вокруг света»"}, Series: "Вокруг света (журнал)"},
		{LibID: "810054", Title: "Звёздные корабли", Authors: []string{"Ефремов,Иван"}, Series: "Вокруг света (журнал)"},
		{LibID: "810055", Title: "Остров погибших кораблей", Authors: []string{"Беляев,Александр"}, Series: "Вокруг света (журнал)"},
		// То же, но админ снял с журнала метку служебного — его серия.
		{LibID: "810061", Title: "Юность, 1970 №1", Authors: []string{"Журнал «Юность»"}, Series: "Юность (журнал)"},
		{LibID: "810062", Title: "Юность, 1970 №2", Authors: []string{"Журнал «Юность»"}, Series: "Юность (журнал)"},
		{LibID: "810063", Title: "Юность, 1970 №3", Authors: []string{"Журнал «Юность»"}, Series: "Юность (журнал)"},
		{LibID: "810066", Title: "Юность, 1970 №4", Authors: []string{"Журнал «Юность»"}, Series: "Юность (журнал)"},
		{LibID: "810067", Title: "Юность, 1970 №5", Authors: []string{"Журнал «Юность»"}, Series: "Юность (журнал)"},
		{LibID: "810068", Title: "Юность, 1970 №6", Authors: []string{"Журнал «Юность»"}, Series: "Юность (журнал)"},
		{LibID: "810069", Title: "Юность, 1970 №7", Authors: []string{"Журнал «Юность»"}, Series: "Юность (журнал)"},
		{LibID: "810070", Title: "Юность, 1970 №8", Authors: []string{"Журнал «Юность»"}, Series: "Юность (журнал)"},
		{LibID: "810064", Title: "Звёздный билет", Authors: []string{"Аксёнов,Василий"}, Series: "Юность (журнал)"},
		{LibID: "810065", Title: "Хроника времён", Authors: []string{"Гладилин,Анатолий"}, Series: "Юность (журнал)"},
		// Серия сборников (#448): первый автор один, но в каждой книге по четыре
		// автора и всего их семь — межавторская, а не «цикл» Иванова.
		{LibID: "810081", Title: "Урал улыбается. Выпуск 1", Authors: []string{"Иванов,Иван", "Петров,Пётр", "Сидоров,Сидор", "Кузнецов,Кузьма"}, Series: "Урал улыбается"},
		{LibID: "810082", Title: "Урал улыбается. Выпуск 2", Authors: []string{"Иванов,Иван", "Смирнов,Семён", "Попов,Павел", "Васильев,Василий"}, Series: "Урал улыбается"},
		// Соавторский цикл (двое на книгу) — по-прежнему цикл.
		{LibID: "810091", Title: "Страж", Authors: []string{"Пехов,Алексей", "Бычкова,Елена"}, Series: "Страж", SerNo: 1},
		{LibID: "810092", Title: "Аутодафе", Authors: []string{"Пехов,Алексей", "Бычкова,Елена"}, Series: "Страж", SerNo: 2},
		{LibID: "810093", Title: "Время созидания", Authors: []string{"Пехов,Алексей", "Бычкова,Елена"}, Series: "Страж", SerNo: 3},
	}
	two := append([]inpxtest.Book{
		{LibID: "810011", Title: "Любовь в Париже", Authors: []string{"Иванова,Анна"}, Series: "Мини-Шарм", SerNo: 1},
		{LibID: "810012", Title: "Любовь в Риме", Authors: []string{"Петрова,Мария"}, Series: "Мини-Шарм", SerNo: 2},
	}, king...)

	// Админ заранее снял с журнала «Юность» метку служебного.
	_, err := pool.Exec(ctx, `INSERT INTO authors (last_name, normalized_name, is_service, is_service_source)
		VALUES ('Журнал «Юность»', 'журнал «юность»', false, 'manual')`)
	require.NoError(t, err)

	// Двое авторов — пока «циклы» у каждого.
	run(two)
	ivanovaFrag := seriesOf("810011")
	require.NotEqual(t, ivanovaFrag, seriesOf("810012"))
	user := q(`INSERT INTO users (email, display_name, password_hash, role) VALUES ('u@x','U','h','user') RETURNING id`)
	_, err = pool.Exec(ctx, `INSERT INTO favorite_series (user_id, series_id) VALUES ($1, $2)`, user, ivanovaFrag)
	require.NoError(t, err)
	anth := seriesOf("810081")
	require.Equal(t, anth, seriesOf("810082"))
	require.Equal(t, int64(1), q(`SELECT count(*) FROM series WHERE id = $1 AND author_id IS NULL AND kind = 'multi'`, anth),
		"серия сборников — межавторская")
	guard := seriesOf("810091")
	require.Equal(t, guard, seriesOf("810093"))
	require.Equal(t, int64(1), q(`SELECT count(*) FROM series WHERE id = $1 AND author_id IS NOT NULL AND kind IS NULL`, guard),
		"соавторский цикл — цикл")
	journal := seriesOf("810051")
	for _, lib := range []string{"810052", "810053", "810054", "810055"} {
		require.Equal(t, journal, seriesOf(lib))
	}
	require.Equal(t, int64(1), q(`SELECT count(*) FROM series WHERE id = $1 AND author_id IS NULL AND kind = 'multi'`, journal),
		"служебный автор не делает серию своим циклом")

	// Третий автор — издательская серия: одна запись без автора.
	three := append(append([]inpxtest.Book(nil), two...),
		inpxtest.Book{LibID: "810013", Title: "Любовь в Вене", Authors: []string{"Сидорова,Ольга"}, Series: "Мини-Шарм", SerNo: 3})
	run(three)
	multi := seriesOf("810011")
	require.Equal(t, multi, seriesOf("810012"))
	require.Equal(t, multi, seriesOf("810013"))
	require.Equal(t, int64(1), q(`SELECT count(*) FROM series WHERE id = $1 AND author_id IS NULL AND kind = 'multi'`, multi))
	require.Zero(t, q(`SELECT count(*) FROM series WHERE normalized_title = 'мини-шарм' AND id <> $1`, multi), "«циклы» удалены")
	require.Equal(t, int64(1), q(`SELECT count(*) FROM favorite_series WHERE user_id = $1 AND series_id = $2`, user, multi),
		"подписка перенесена на общую серию")
	require.Equal(t, multi, q(`SELECT w.series_id FROM works w JOIN books b ON b.work_id = w.id WHERE b.lib_id = '810011'`),
		"работа переехала в общую серию")

	// Авторский цикл не затронут.
	tower := seriesOf("810001")
	require.Equal(t, int64(1), q(`SELECT count(*) FROM series WHERE id = $1 AND author_id IS NOT NULL AND kind IS NULL`, tower))
	// Доминирующий автор — его цикл, у продолжателей свои «циклы», как раньше.
	wolfe := seriesOf("810031")
	require.Equal(t, wolfe, seriesOf("810034"))
	require.Equal(t, int64(1), q(`SELECT count(*) FROM series WHERE id = $1 AND author_id IS NOT NULL AND kind IS NULL`, wolfe))
	require.NotEqual(t, wolfe, seriesOf("810035"))
	classics := seriesOf("810071")
	for _, lib := range []string{"810072", "810073", "810074", "810075"} {
		require.Equal(t, classics, seriesOf(lib))
	}
	require.Equal(t, int64(1), q(`SELECT count(*) FROM series WHERE id = $1 AND author_id IS NULL AND kind = 'multi'`, classics),
		"60% у одного автора — издательская серия")
	// Ручное «не служебный» — журнал снова доминирующий: его цикл.
	yunost := seriesOf("810061")
	require.Equal(t, yunost, seriesOf("810063"))
	require.Equal(t, int64(1), q(`SELECT count(*) FROM series WHERE id = $1 AND author_id IS NOT NULL AND kind IS NULL`, yunost))
	require.NotEqual(t, yunost, seriesOf("810064"))
	// Жанровое название — своя серия у каждого автора, общей нет.
	chekhov := seriesOf("810041")
	require.Equal(t, chekhov, seriesOf("810042"))
	require.NotEqual(t, chekhov, seriesOf("810043"))
	require.NotEqual(t, seriesOf("810043"), seriesOf("810044"))
	require.Zero(t, q(`SELECT count(*) FROM series WHERE normalized_title = 'рассказы' AND (kind IS NOT NULL OR author_id IS NULL)`))

	// Следующий выпуск: под названием снова двое — серия остаётся общей.
	run(append(append([]inpxtest.Book(nil), two...),
		inpxtest.Book{LibID: "810014", Title: "Новинка", Authors: []string{"Кинг,Стивен"}}))
	require.Equal(t, multi, seriesOf("810011"))
	require.Equal(t, multi, seriesOf("810012"))
}
