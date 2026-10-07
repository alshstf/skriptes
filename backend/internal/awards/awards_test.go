package awards

import (
	"context"
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
	require.Equal(t, authorWant{id: 1, first: "дж", rest: "м кутзе", last: "кутзе", whole: "дж м кутзе"}, w)
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
		require.Positive(t, a.FantlabID, a.Key)
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

	var second atomic.Bool // вторая синхронизация: Букер недоступен, у Нобелевской один лауреат
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case second.Load() && r.URL.Path == "/award/74":
			_, _ = w.Write([]byte(`{"contests":[{"nameyear":2003,"contest_works":[{"contest_work_id":201,"cw_winner":1,
				"cw_link_type":"autor","cw_link_id":8,"autor_rusname":"Дж. М. Кутзее"}]}]}`))
		case second.Load():
			http.Error(w, "nope", http.StatusInternalServerError)
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
				 "nomination_number":1,"nomination_rusname":"Русский Букер","autor_rusname":"Нет Такого","work_rusname":"Нет в каталоге"}
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
	s := NewSyncer(pool, nil).WithEndpoint(srv.URL, srv.Client())

	total, matched, err := s.SyncAll(ctx)
	require.NoError(t, err)
	require.Equal(t, 7, total)
	require.Equal(t, 5, matched)

	workOf := func(ref string) *int64 {
		var id *int64
		require.NoError(t, pool.QueryRow(ctx, `SELECT work_id FROM award_wins WHERE source_ref = $1`, ref).Scan(&id))
		return id
	}
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
			require.Equal(t, 5, a.Wins)
			require.Equal(t, 3, a.InCatalog)
			require.Equal(t, 2000, a.FirstYear)
		case "nobel":
			require.Equal(t, 2, a.InCatalog)
		default:
			require.Zero(t, a.Wins, a.Key)
		}
	}
	award, wins, err := svc.Wins(ctx, "nobel")
	require.NoError(t, err)
	require.Equal(t, "Нобелевская премия по литературе", award.Name)
	require.Len(t, wins, 2)
	require.Equal(t, 2003, wins[0].Year, "свежие сверху")
	require.Equal(t, "https://fantlab.ru/autor8", wins[0].SourceURL)
	_, _, err = svc.Wins(ctx, "bolshaya-kniga")
	require.ErrorIs(t, err, ErrUnknownAward)

	badges, err := svc.WorkAwards(ctx, disgraceRu)
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
	require.Equal(t, 6, total, "Бунин удалён, Букер остался при сбое источника")
	require.Equal(t, 3, matched)
	require.Nil(t, workOf("101"), "издания «Соляриса» Лема удалены — связи нет")
}
