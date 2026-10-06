package metadata

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const fb2Dubliners = `<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
<description><title-info><book-title>Дублинцы</book-title></title-info></description>
<body>
  <title><p>Джеймс Джойс</p><p>Дублинцы</p></title>
  <section><title><p>Сёстры</p></title><p>Текст&nbsp;рассказа</p></section>
  <section><title><p>Аравия</p></title>
    <section><title><p>Глава 1</p></title><p>…</p></section>
  </section>
  <section><title><p>Мертвые</p><p>(Перевод О. Холмской)</p></title><p>…</p></section>
  <section><title><p>Примечания переводчика</p></title><p>…</p></section>
</body>
<body name="notes">
  <section><title><p>1</p></title><p>сноска</p></section>
</body>
</FictionBook>`

func TestScanFb2Contents(t *testing.T) {
	roots := scanFb2Contents(strings.NewReader(fb2Dubliners))
	require.Len(t, roots, 4, "примечания (body name=notes) не в счёт")
	require.Equal(t, "Сёстры", roots[0].title)
	require.Equal(t, "Мертвые (Перевод О. Холмской)", roots[2].title, "абзацы заголовка через пробел")
	require.Len(t, roots[1].children, 1)
	require.Equal(t, "Глава 1", roots[1].children[0].title)

	entries := contentsEntries(roots, "Дублинцы", nil)
	var titles []string
	for _, e := range entries {
		titles = append(titles, e.title)
	}
	require.Equal(t, []string{"Сёстры", "Аравия", "Мертвые"}, titles,
		"главы внутри рассказа не строки состава; примечания — служебный раздел; переводчик — не часть названия")
	require.Equal(t, "В день плюща", displayContentTitle("В день плюща[58] Перевод Н. Л. Дарузес"))
	require.Equal(t, "Перевод с английского как жанр", displayContentTitle("Перевод с английского как жанр"))
}

func TestContentsEntries_WrapperGroupsAndAuthors(t *testing.T) {
	king := newContentAuthor(1, "Кинг", "Стивен")
	straub := newContentAuthor(2, "Страуб", "Питер")
	roots := []tocNode{{title: "Антология ужаса", children: []tocNode{
		{title: "Предисловие"},
		{title: "Стивен Кинг", children: []tocNode{{title: "Туман"}, {title: "Тот, кто хранит"}}},
		{title: "Страуб Питер", children: []tocNode{{title: "Блюз"}}},
		{title: "Повести и рассказы", children: []tocNode{{title: "Окно"}, {title: "Глава 3"}}},
		{title: "Часть вторая", children: []tocNode{{title: "Сад"}}},
	}}}
	entries := contentsEntries(roots, "Антология ужаса", []contentAuthor{king, straub})
	require.Equal(t, []contentEntry{
		{title: "Туман", authorID: 1}, {title: "Тот, кто хранит", authorID: 1}, {title: "Блюз", authorID: 2},
		{title: "Окно"}, {title: "Сад"},
	}, entries, "одна обёртка — уровнем ниже; группы и авторы — их подразделы; «Глава 3» — не произведение")

	require.Nil(t, contentsEntries([]tocNode{{title: "Единственный"}}, "X", nil), "одна строка — не оглавление")
}

func TestContentKey(t *testing.T) {
	for in, want := range map[string]string{
		"Сёстры":           "сестры",
		"IV. Мёртвые":      "мертвые",
		"12) Аравия":       "аравия",
		"Глава 2. Встреча": "встреча",
		"Мертвые (Перевод О. Холмской)": "мертвые",
		"Туман [1]": "туман",
		"«Тот, кто хранит» — рассказ":    "тот кто хранит рассказ",
		"Перевод с английского как жанр": "перевод с английского как жанр",
	} {
		require.Equal(t, want, contentKey(in), in)
	}
	require.True(t, isServiceContentTitle(contentKey("От авторов (предисловие к сборнику)")))
	require.True(t, isServiceContentTitle(contentKey("Часть первая")))
	require.False(t, isServiceContentTitle(contentKey("Часть тьмы")))
}

func TestMatchContentEntry(t *testing.T) {
	king := newContentAuthor(1, "Кинг", "Стивен")
	straub := newContentAuthor(2, "Страуб", "Питер")
	authors := []contentAuthor{king, straub}
	byKey := map[string][]contentCandidate{
		"туман":    {{workID: 10, authors: []int64{1}}},
		"блюз":     {{workID: 20, authors: []int64{2}}},
		"окно":     {{workID: 30, authors: []int64{1}}, {workID: 31, authors: []int64{2}}},
		"талисман": {{workID: 40, authors: []int64{1, 2}}},
	}
	require.EqualValues(t, 10, matchContentEntry(contentEntry{title: "Туман"}, byKey, authors))
	require.EqualValues(t, 0, matchContentEntry(contentEntry{title: "Окно"}, byKey, authors), "два кандидата — не угадываем")
	require.EqualValues(t, 31, matchContentEntry(contentEntry{title: "Окно", authorID: 2}, byKey, authors),
		"под заголовком автора — его работа")
	require.EqualValues(t, 0, matchContentEntry(contentEntry{title: "Туман", authorID: 2}, byKey, authors),
		"работа другого автора под заголовком Страуба — не связываем")
	require.EqualValues(t, 20, matchContentEntry(contentEntry{title: "Питер Страуб. Блюз"}, byKey, authors), "«Автор Название»")
	require.EqualValues(t, 40, matchContentEntry(contentEntry{title: "Талисман"}, byKey, authors))
	require.EqualValues(t, 0, matchContentEntry(contentEntry{title: "Сияние"}, byKey, authors))
	require.EqualValues(t, 10, matchContentEntry(contentEntry{title: "Осень невинности «Туман»"}, byKey, authors),
		"название в «ёлочках» внутри строки")
}
