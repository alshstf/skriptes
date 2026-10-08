package awards

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

func TestNormalization(t *testing.T) {
	require.Equal(t, "р значит ракета", normTitle("Р — значит ракета"))
	require.Equal(t, "451 по фаренгеиту", softName("451° по Фаренгейту"))
	require.Equal(t, "звездныи десант", softName("Звёздный десант"))
	require.Equal(t, softName("Сэмюэль Беккет"), softName("Сэмюэл Беккет"))
	require.Equal(t, softName("Чайна Мьевилль"), softName("Чайна Мьевиль"))
	require.Equal(t, surnameStem("Фрэнк Херберт"), surnameStem("Фрэнк Герберт"))
	require.Equal(t, "стругацк", surnameStem("Аркадий и Борис Стругацкие"))
	require.Equal(t, "лем", surnameStem("Станислав Лем"), "короткая фамилия — целиком")

	w, ok := newAuthorWant(1, "Дж. М. Кутзее")
	require.True(t, ok)
	require.Equal(t, authorWant{id: 1, first: "дж", mid: "м", rest: "кутзе", last: "кутзе", whole: "дж м кутзе"}, w)
	w, _ = newAuthorWant(3, "Василий И. Аксёнов")
	require.Equal(t, "и", w.mid, "инициал отчества")
	require.Equal(t, "аксенов", w.rest)
	w, _ = newAuthorWant(4, "Борис Иванович Иванов")
	require.Equal(t, "и", w.mid, "отчество")
	require.Equal(t, "иванов", w.rest)
	w, _ = newAuthorWant(5, "Юджин О'Нил")
	require.Empty(t, w.mid, "«О'» — не инициал")
	require.Equal(t, "о нил", w.rest)
	require.Equal(t, "кто бы мог подумать", mainTitle("Кто бы мог подумать! Как мозг заставляет нас делать глупости"))
	w, ok = newAuthorWant(2, "Сюлли-Прюдом")
	require.True(t, ok)
	require.Equal(t, "сюли прюдом", w.whole)

	require.Equal(t, "...And Call Me Conrad", quoted(`Roger Zelazny "...And Call Me Conrad"`))
	require.Empty(t, quoted("Иван Бунин"))
}

func TestAllows(t *testing.T) {
	a := Award{MaxYear: 2021, Nominations: []int{0, 101}}
	require.True(t, a.allows(2021, 101))
	require.True(t, a.allows(1999, 0))
	require.False(t, a.allows(2022, 101), "после MaxYear не учитываем")
	require.False(t, a.allows(2010, 105), "номинация вне белого списка")
	require.True(t, Award{}.allows(2026, 7))
	for _, a := range Catalog {
		require.NotEmpty(t, a.Key)
		require.True(t, a.FantlabID > 0 || len(a.Wikidata) > 0 || a.Manual, "у премии есть источник: %s", a.Key)
		got, ok := ByKey(a.Key)
		require.True(t, ok)
		require.Equal(t, a.Name, got.Name)
	}
}

// fantlabAward — ответ /award/{id}?include_contests=1 в форме Фантлаба.
const fantlabAward = `{"contests": [
 {"nameyear": 2020, "contest_works": [
  {"contest_work_id": 1, "cw_winner": 1, "cw_link_type": "work", "cw_link_id": 10, "nomination_id": 101,
   "nomination_number": 1, "nomination_rusname": "Роман", "autor_rusname": "Фрэнк Герберт",
   "work_rusname": "Дюна", "cw_name": "Frank Herbert \"Dune\""},
  {"contest_work_id": 2, "cw_winner": 0, "cw_link_type": "work", "cw_link_id": 11, "nomination_id": 101,
   "nomination_rusname": "Роман", "autor_rusname": "Кто-то", "work_rusname": "Номинант"},
  {"contest_work_id": 3, "cw_winner": 1, "cw_link_type": "work", "cw_link_id": 12, "nomination_id": 999,
   "nomination_rusname": "Журнал", "autor_rusname": "Редакция", "work_rusname": "Журнал"},
  {"contest_work_id": 4, "cw_winner": 1, "cw_link_type": "autor", "cw_link_id": 5, "nomination_id": 101,
   "nomination_rusname": "Роман", "autor_rusname": "Иван Бунин"},
  {"contest_work_id": 5, "cw_winner": 1, "cw_link_type": "work", "cw_link_id": 13, "nomination_id": null,
   "nomination_number": 0, "autor_rusname": "Станислав Лем", "work_rusname": "Солярис", "cw_name": "Станислав Лем \"Солярис\""}
 ]},
 {"nameyear": 2023, "contest_works": [
  {"contest_work_id": 6, "cw_winner": 1, "cw_link_type": "work", "cw_link_id": 14, "nomination_id": 101,
   "nomination_rusname": "Роман", "autor_rusname": "Поздний Автор", "work_rusname": "После войны"}
 ]}
]}`

func TestFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/award/7", r.URL.Path)
		require.Equal(t, "1", r.URL.Query().Get("include_contests"))
		_, _ = w.Write([]byte(fantlabAward))
	}))
	defer srv.Close()
	s := NewSyncer(nil, nil).WithEndpoint(srv.URL, srv.Client())

	wins, err := s.fetch(context.Background(), Award{Key: "x", FantlabID: 7, MaxYear: 2021, Nominations: []int{0, 101}})
	require.NoError(t, err)
	require.Equal(t, []win{
		{year: 2020, nomination: "Роман", nomOrder: 1, kind: "work", title: "Дюна", origTitle: "Dune", author: "Фрэнк Герберт",
			ref: "1", link: "work10"},
		{year: 2020, kind: "work", title: "Солярис", author: "Станислав Лем", ref: "5", link: "work13"},
	}, wins, "только лауреаты, номинации белого списка, до MaxYear; премия автору — только у AuthorLevel")

	wins, err = s.fetch(context.Background(), Award{Key: "x", FantlabID: 7, AuthorLevel: true, Nominations: []int{101}})
	require.NoError(t, err)
	require.Len(t, wins, 3)
	require.Equal(t, win{year: 2020, nomination: "Роман", kind: "author", author: "Иван Бунин", ref: "4", link: "autor5"}, wins[1])
}

// catalogFixture — книги и авторы каталога для сопоставления.
type catalogFixture struct {
	t              *testing.T
	ctx            context.Context
	pool           *pgxpool.Pool
	collID, archID int64
	n              int
	authors        map[string]int64
}

func newCatalog(t *testing.T, ctx context.Context, pool *pgxpool.Pool) *catalogFixture {
	f := &catalogFixture{t: t, ctx: ctx, pool: pool, authors: map[string]int64{}}
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&f.collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, f.collID).Scan(&f.archID))
	return f
}

// author — автор «Фамилия|Имя|Отчество» (повторный вызов — тот же id).
func (f *catalogFixture) author(name string, renown int64) int64 {
	if id, ok := f.authors[name]; ok {
		return id
	}
	p := strings.Split(name+"||", "|")
	var id int64
	require.NoError(f.t, f.pool.QueryRow(f.ctx, `
		INSERT INTO authors (last_name, first_name, middle_name, normalized_name, renown) VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		p[0], p[1], p[2], strings.ToLower(name), renown).Scan(&id))
	f.authors[name] = id
	return id
}

// work — работа с изданиями (название, оригинальное название, язык) одного автора.
func (f *catalogFixture) work(author string, editions ...[3]string) int64 {
	var workID int64
	require.NoError(f.t, f.pool.QueryRow(f.ctx,
		`INSERT INTO works (title, normalized_title, edition_count) VALUES ($1, lower($1), $2) RETURNING id`,
		editions[0][0], len(editions)).Scan(&workID))
	aid := f.author(author, 0)
	for _, e := range editions {
		f.n++
		var bookID int64
		require.NoError(f.t, f.pool.QueryRow(f.ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, src_title, lang, work_id)
			VALUES ($1,$2,$3,'f','fb2',$4,lower($4),NULLIF($5,''),$6,$7) RETURNING id`,
			f.collID, f.archID, fmt.Sprint(f.n), e[0], e[1], e[2], workID).Scan(&bookID))
		_, err := f.pool.Exec(f.ctx, `INSERT INTO book_authors (book_id, author_id) VALUES ($1,$2)`, bookID, aid)
		require.NoError(f.t, err)
	}
	return workID
}

func TestSyncAndMatch(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	f := newCatalog(t, ctx, pool)

	dune := f.work("Херберт|Фрэнк", [3]string{"Дюна", "Dune", "ru"})
	f.work("Херберт|Брайан", [3]string{"Дюна: Дом Атрейдесов", "", "ru"})
	solaris := f.work("Лем|Станислав", [3]string{"Солярис", "", "ru"}, [3]string{"Солярис", "", "ru"})
	solarisDup := f.work("Лем|Станислав", [3]string{"Солярис", "", "ru"}) // дубль работы с одним изданием — уступает
	f.work("Тарковский|Андрей", [3]string{"Солярис", "", "ru"})
	// Только иноязычное издание с оригинальным названием и русское под другим названием.
	disgraceEn := f.work("Кутзее|Джон|Максвелл", [3]string{"Disgrace", "Disgrace", "en"})
	disgraceRu := f.work("Кутзее|Джон|Максвелл", [3]string{"Бесчестье", "", "ru"})
	f.author("Бунин|Иван|Алексеевич", 10)
	f.author("Бунин|Иван|Иванович", 0) // тёзка, менее известный
	kutzee := f.authors["Кутзее|Джон|Максвелл"]
	// «Просветитель» (manual.json): у премии название с подзаголовком, в каталоге — без.
	kazantseva := f.work("Казанцева|Ася", [3]string{"Кто бы мог подумать", "", "ru"})
	// Премия Андрея Белого 1985 — «Василий И. Аксёнов», не автор «Острова Крым».
	f.author("Аксёнов|Василий|Павлович", 100)
	// Работа, известная по id Фантлаба (#412): название у лауреата другое.
	fsk := f.work("Стругацкий|Аркадий", [3]string{"Пикник на обочине", "", "ru"})
	_, err0 := pool.Exec(ctx, `UPDATE works SET ext_ids = ext_ids || '{"fl_id": 4242}' WHERE id = $1`, fsk)
	require.NoError(t, err0)
	// Пулитцеровская премия и «Оскар» экранизации (Wikidata): книга и фильм по ней.
	gone := f.work("Митчелл|Маргарет", [3]string{"Унесённые ветром", "Gone with the Wind", "ru"})
	_, err := pool.Exec(ctx, `INSERT INTO book_adaptations (book_id, provider, ext_id, title, year)
		SELECT id, 'wikidata', 'Q2875', 'Унесённые ветром', 1939 FROM books WHERE work_id = $1`, gone)
	require.NoError(t, err)

	var second atomic.Bool // вторая синхронизация: Букер недоступен, у Нобелевской один лауреат
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case second.Load() && r.URL.Path == "/award/74":
			_, _ = w.Write([]byte(`{"contests":[{"nameyear":2003,"contest_works":[{"contest_work_id":201,"cw_winner":1,
				"cw_link_type":"autor","cw_link_id":8,"autor_rusname":"Дж. М. Кутзее"}]}]}`))
		case second.Load():
			http.Error(w, "nope", http.StatusInternalServerError)
		case r.URL.Path == "/sparql":
			q := r.URL.Query().Get("query")
			switch {
			case strings.Contains(q, "wd:Q833633 "): // Пулитцеровская
				_, _ = w.Write([]byte(sparqlRows(
					map[string]string{"item": "Q1", "human": "false", "t": "1937-05-03T00:00:00Z", "iru": "Унесённые ветром",
						"ien": "Gone with the Wind", "auth": "Q2", "aru": "Маргарет Митчелл"},
					map[string]string{"item": "Q2", "human": "true", "t": "1937-05-03T00:00:00Z", "iru": "Маргарет Митчелл"},
					map[string]string{"item": "Q3", "human": "true", "t": "1938-05-03T00:00:00Z", "iru": "Джон Марканд"})))
			case strings.Contains(q, "wd:Q102427 "): // «Оскар», лучший фильм: фильм, продюсер «за работу», чужой фильм
				_, _ = w.Write([]byte(sparqlRows(
					map[string]string{"item": "Q2875", "human": "false", "t": "1940-02-29T00:00:00Z", "iru": "Унесённые ветром"},
					map[string]string{"item": "Q5", "human": "true", "t": "1940-02-29T00:00:00Z", "forw": "Q2875",
						"fru": "Унесённые ветром"},
					map[string]string{"item": "Q999", "human": "false", "t": "1998-03-23T00:00:00Z", "iru": "Титаник"})))
			default:
				_, _ = w.Write([]byte(sparqlRows()))
			}
		case r.URL.Path == "/award/36":
			_, _ = w.Write([]byte(`{"contests":[{"nameyear":2000,"contest_works":[
				{"contest_work_id":100,"cw_winner":1,"cw_link_type":"work","cw_link_id":1,"nomination_id":261,
				 "nomination_number":1,"nomination_rusname":"Русский Букер","autor_rusname":"Фрэнк Герберт","work_rusname":"Дюна"},
				{"contest_work_id":101,"cw_winner":1,"cw_link_type":"work","cw_link_id":2,"nomination_id":261,
				 "nomination_number":1,"nomination_rusname":"Русский Букер","autor_rusname":"Станислав Лем","work_rusname":"Солярис"},
				{"contest_work_id":102,"cw_winner":1,"cw_link_type":"work","cw_link_id":3,"nomination_id":261,
				 "nomination_number":1,"nomination_rusname":"Русский Букер","autor_rusname":"Дж. М. Кутзее",
				 "work_rusname":"Бесчестье","cw_name":"J. M. Coetzee \"Disgrace\""},
				{"contest_work_id":103,"cw_winner":1,"cw_link_type":"work","cw_link_id":4,"nomination_id":261,
				 "nomination_number":1,"nomination_rusname":"Русский Букер","autor_rusname":"Иван Петров","work_rusname":"Солярис"},
				{"contest_work_id":104,"cw_winner":1,"cw_link_type":"work","cw_link_id":5,"nomination_id":261,
				 "nomination_number":1,"nomination_rusname":"Русский Букер","autor_rusname":"Нет Такого","work_rusname":"Нет в каталоге"},
				{"contest_work_id":105,"cw_winner":1,"cw_link_type":"work","cw_link_id":4242,"nomination_id":261,
				 "nomination_number":1,"nomination_rusname":"Русский Букер","autor_rusname":"Аркадий и Борис Стругацкие","work_rusname":"Пикник на обочине (другая редакция)"}
			]}]}`))
		case r.URL.Path == "/award/74":
			_, _ = w.Write([]byte(`{"contests":[
				{"nameyear":1933,"contest_works":[{"contest_work_id":200,"cw_winner":1,"cw_link_type":"autor","cw_link_id":9,
				 "autor_rusname":"Иван Бунин"}]},
				{"nameyear":2003,"contest_works":[{"contest_work_id":201,"cw_winner":1,"cw_link_type":"autor","cw_link_id":8,
				 "autor_rusname":"Дж. М. Кутзее"}]}]}`))
		default:
			http.Error(w, "nope", http.StatusInternalServerError) // остальные премии — сбой источника
		}
	}))
	defer srv.Close()
	reindexed := map[int64]bool{}
	s := NewSyncer(pool, nil).WithEndpoint(srv.URL, srv.Client()).WithWorksChanged(func(_ context.Context, ids []int64) error {
		for _, id := range ids {
			reindexed[id] = true
		}
		return nil
	})

	manual := manualCounts(t)
	total, matched, err := s.SyncAll(ctx)
	require.NoError(t, err)
	require.Equal(t, 8+manual[""]+2+2, total, "Фантлаб, ручной список, Пулитцер (книга и автор), «Оскар» (два фильма)")
	require.Equal(t, 6+1+1+1, matched, "Фантлаб, «Просветитель», Пулитцер, «Оскар»")
	for _, id := range []int64{dune, solaris, disgraceRu, kazantseva, gone} {
		require.True(t, reindexed[id], "работа с новыми премиями уходит на переиндексацию: %d", id)
	}

	workOf := func(ref string) *int64 {
		var id *int64
		require.NoError(t, pool.QueryRow(ctx, `SELECT work_id FROM award_wins WHERE source_ref = $1`, ref).Scan(&id))
		return id
	}
	require.Equal(t, fsk, *workOf("105"), "по id Фантлаба — несмотря на другое название")
	require.Equal(t, dune, *workOf("100"), "Герберт = Херберт; «Дюна: Дом Атрейдесов» Брайана — другое название")
	require.Equal(t, solaris, *workOf("101"), "из дублей — с большим числом изданий, фильм Тарковского — другой автор")
	require.Equal(t, disgraceRu, *workOf("102"), "русское издание важнее найденного по оригинальному названию")
	require.NotEqual(t, disgraceEn, *workOf("102"))
	require.Nil(t, workOf("103"), "то же название, другой автор — не связываем")
	require.Nil(t, workOf("104"))

	authorOf := func(ref string) *int64 {
		var id *int64
		require.NoError(t, pool.QueryRow(ctx, `SELECT author_id FROM award_wins WHERE source_ref = $1`, ref).Scan(&id))
		return id
	}
	require.Equal(t, f.authors["Бунин|Иван|Алексеевич"], *authorOf("200"), "из тёзок — самый известный")
	require.Equal(t, kutzee, *authorOf("201"), "инициалы «Дж. М.» → Джон Максвелл")

	svc := NewService(pool)
	list, err := svc.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, len(Catalog))
	for _, a := range list {
		switch a.Key {
		case "russian-booker":
			require.Equal(t, 6, a.Wins)
			require.Equal(t, 4, a.InCatalog)
			require.Equal(t, 2000, a.FirstYear)
		case "nobel":
			require.Equal(t, 2, a.InCatalog)
		case "dar", "prosvetitel", "bely":
			require.Equal(t, manual[a.Key], a.Wins, a.Key)
			require.Equal(t, map[string]int{"prosvetitel": 1}[a.Key], a.InCatalog, a.Key)
		case "pulitzer":
			require.Equal(t, 2, a.Wins)
			require.Equal(t, 1, a.InCatalog)
		case "oscar":
			require.Equal(t, 1, a.Wins, "кинопремия — только экранизации книг каталога")
			require.Equal(t, 1940, a.FirstYear)
		default:
			require.Zero(t, a.Wins, a.Key)
		}
	}
	require.Equal(t, kazantseva, *workOf("prosvetitel/2014/Естественные и точные науки/Кто бы мог подумать! Как мозг заставляет нас делать глупости/Ася Казанцева"))
	var aksenov *int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT author_id FROM award_wins WHERE award = 'bely' AND author LIKE '%Аксёнов'`).Scan(&aksenov))
	require.Nil(t, aksenov, "инициал отчества не совпал — не связываем")

	_, oscar, err := svc.Wins(ctx, "oscar")
	require.NoError(t, err)
	require.Len(t, oscar, 1)
	require.Equal(t, "Унесённые ветром", oscar[0].Title)
	require.Equal(t, gone, *oscar[0].WorkID)
	require.Equal(t, "https://www.wikidata.org/wiki/Q2875", oscar[0].SourceURL)
	badges, err := svc.WorkAwards(ctx, gone)
	require.NoError(t, err)
	require.Equal(t, []Badge{
		{Key: "pulitzer", Name: "Пулитцеровская премия", Year: 1937, Nomination: "Художественная книга"},
		{Key: "oscar", Name: "Оскар", Year: 1940, Nomination: "Лучший фильм", Film: "Унесённые ветром"},
	}, badges)
	_, pulitzer, err := svc.Wins(ctx, "pulitzer")
	require.NoError(t, err)
	require.Equal(t, "author", pulitzer[0].Kind, "1938: человек без работы — премия автору")
	require.Equal(t, "Джон Марканд", pulitzer[0].Author)
	award, wins, err := svc.Wins(ctx, "nobel")
	require.NoError(t, err)
	require.Equal(t, "Нобелевская премия по литературе", award.Name)
	require.Len(t, wins, 2)
	require.Equal(t, 2003, wins[0].Year, "свежие сверху")
	require.Equal(t, "https://fantlab.ru/autor8", wins[0].SourceURL)
	_, _, err = svc.Wins(ctx, "bolshaya-kniga")
	require.ErrorIs(t, err, ErrUnknownAward)

	badges, err = svc.WorkAwards(ctx, disgraceRu)
	require.NoError(t, err)
	require.Equal(t, []Badge{{Key: "russian-booker", Name: "Русский Букер", Year: 2000, Nomination: "Русский Букер"}}, badges)
	badges, err = svc.AuthorAwards(ctx, kutzee)
	require.NoError(t, err)
	require.Equal(t, []Badge{{Key: "nobel", Name: "Нобелевская премия по литературе", Year: 2003}}, badges)

	// Повторная синхронизация: у источника лауреат исчез — запись удаляется;
	// книга ушла из каталога — связь снимается; сбой источника — данные премии остаются.
	_, err = pool.Exec(ctx, `UPDATE books SET deleted = true WHERE work_id IN ($1, $2)`, solaris, solarisDup)
	require.NoError(t, err)
	second.Store(true)
	total, matched, err = s.SyncAll(ctx)
	require.NoError(t, err)
	require.Equal(t, 7+manual[""]+4, total, "Бунин удалён, Букер и Wikidata остались при сбое источника")
	require.Equal(t, 4+3, matched)
	require.Nil(t, workOf("101"), "издания «Соляриса» Лема удалены — связи нет")
	require.True(t, reindexed[solaris], "снятая связь — тоже переиндексация")
}

// manualCounts — лауреатов в manual.json по премиям ("" — всего).
func manualCounts(t *testing.T) map[string]int {
	out := map[string]int{}
	for _, a := range Catalog {
		if !a.Manual {
			continue
		}
		ws, err := manualWins(a)
		require.NoError(t, err)
		out[a.Key] = len(ws)
		out[""] += len(ws)
	}
	return out
}

// sparqlRows — ответ SPARQL с заданными строками (значения — QID или текст).
func sparqlRows(rows ...map[string]string) string {
	type val struct {
		Value string `json:"value"`
	}
	b := make([]map[string]val, 0, len(rows))
	for _, r := range rows {
		m := map[string]val{}
		for k, v := range r {
			if strings.HasPrefix(v, "Q") && !strings.Contains(v, " ") && k != "iru" && k != "ien" {
				v = "http://www.wikidata.org/entity/" + v
			}
			m[k] = val{v}
		}
		b = append(b, m)
	}
	out, _ := json.Marshal(map[string]any{"results": map[string]any{"bindings": b}})
	return string(out)
}

func TestManualWins(t *testing.T) {
	dar, ok := ByKey("dar")
	require.True(t, ok)
	ws, err := manualWins(dar)
	require.NoError(t, err)
	require.NotEmpty(t, ws)
	for _, w := range ws {
		require.Equal(t, "work", w.kind)
		require.NotEmpty(t, w.title)
		require.NotEqual(t, "Мария Галина", w.author, "от победы 2025 отказалась")
	}
	bely, _ := ByKey("bely")
	ws, err = manualWins(bely)
	require.NoError(t, err)
	refs := map[string]bool{}
	for _, w := range ws {
		require.Equal(t, "author", w.kind, "Премия Андрея Белого вручается автору")
		require.Contains(t, []string{"Поэзия", "Проза", "Гуманитарные исследования"}, w.nomination)
		require.False(t, refs[w.ref], "ref уникален: %s", w.ref)
		refs[w.ref] = true
	}
}

func TestWikidataWins(t *testing.T) {
	book := Award{Key: "b"}
	it := WikidataItem{QID: "Q1", Nomination: "Книга"}
	rows := []map[string]string{
		// Книга с двумя авторами — две строки.
		{"item": "e/Q10", "human": "false", "t": "2001-01-01T00:00:00Z", "iru": "Книга", "ien": "Book", "auth": "e/Q20", "aru": "Анна Первая"},
		{"item": "e/Q10", "human": "false", "t": "2001-01-01T00:00:00Z", "iru": "Книга", "ien": "Book", "auth": "e/Q21", "aen": "Bob Second"},
		// Автор той же книги без «за работу» — уже учтён книгой.
		{"item": "e/Q20", "human": "true", "t": "2001-01-01T00:00:00Z", "iru": "Анна Первая"},
		// Человек «за работу» — работа с ним автором.
		{"item": "e/Q30", "human": "true", "t": "2002-01-01T00:00:00Z", "iru": "Пётр Третий", "forw": "e/Q31", "fen": "Only English"},
		// Без года — пропуск.
		{"item": "e/Q40", "human": "false", "t": "", "iru": "Без года"},
		// Премия через 67 лет после публикации — ошибка данных, пропуск.
		{"item": "e/Q60", "human": "false", "t": "2020-01-01T00:00:00Z", "ipub": "1952-04-14T00:00:00Z", "iru": "Старая",
			"auth": "e/Q61", "aru": "Ральф Эллисон"},
	}
	ws := wikidataWins(book, it, 3, rows)
	require.Len(t, ws, 2)
	byYear := map[int]win{}
	for _, w := range ws {
		byYear[w.year] = w
	}
	require.Equal(t, win{year: 2001, nomination: "Книга", nomOrder: 3, kind: "work", title: "Книга", origTitle: "Book",
		author: "Анна Первая, Bob Second", link: "Q10", ref: "b/Q1/2001/Q10"}, byYear[2001])
	require.Equal(t, "Only English", byYear[2002].title)
	require.Empty(t, byYear[2002].origTitle)
	require.Equal(t, "Пётр Третий", byYear[2002].author)

	film := Award{Key: "f", Film: true}
	ws = wikidataWins(film, it, 0, []map[string]string{
		{"item": "e/Q50", "human": "true", "t": "1999-01-01T00:00:00Z", "iru": "Продюсер", "forw": "e/Q51", "fru": "Фильм"},
		{"item": "e/Q52", "human": "true", "t": "1999-01-01T00:00:00Z", "iru": "Режиссёр без фильма"},
	})
	require.Equal(t, []win{{year: 1999, nomination: "Книга", kind: "work", title: "Фильм", link: "Q51", ref: "f/Q1/1999/Q51"}}, ws,
		"кинопремия: фильм «за работу», люди без фильма не нужны")
}

func TestStepSyncsOnCatalogChange(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/sparql" {
			_, _ = w.Write([]byte(sparqlRows()))
			return
		}
		_, _ = w.Write([]byte(`{"contests":[]}`))
	}))
	defer srv.Close()
	s := NewSyncer(pool, nil).WithEndpoint(srv.URL, srv.Client())

	s.step(ctx)
	require.Positive(t, calls.Load(), "первый запуск — загрузка")
	calls.Store(0)
	s.step(ctx)
	require.Zero(t, calls.Load(), "неделя не прошла, список тот же — только сопоставление")
	_, err := pool.Exec(ctx, `UPDATE app_settings SET value = '{"v":"old"}' WHERE key = $1`, syncedKey)
	require.NoError(t, err)
	s.step(ctx)
	require.Positive(t, calls.Load(), "белый список изменился — загрузка сразу")
}
