package metadata

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeISBN(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"978-5-389-07435-4", "9785389074354"},
		{"5-352-00269-9", "5352002699"},
		{"ISBN 978-5-04-004587-3", "9785040045873"},
		{"080442957X", "080442957X"},
		{"080442957x", "080442957X"},
		{"", ""},
		{"не указан", ""},
		{"123", ""},                 // короткий мусор → отброшен
		{"978-5-389 (вариант)", ""}, // неполный → отброшен (precision)
	}
	for _, c := range cases {
		require.Equalf(t, c.want, normalizeISBN(c.in), "normalizeISBN(%q)", c.in)
	}
}

func TestNormalizePersonKey(t *testing.T) {
	require.Equal(t, "голышев виктор", normalizePersonKey("  Голышев   Виктор "))
	require.Equal(t, "толкин джон рональд руэл", normalizePersonKey("Толкин Джон Рональд Руэл"))
	require.Equal(t, "", normalizePersonKey("   "))
}

func TestFb2Provider_FetchEditionMeta(t *testing.T) {
	wrap := func(desc string) []byte {
		return []byte(`<?xml version="1.0"?>
<FictionBook>
  <description>` + desc + `</description>
  <body><p>text</p></body>
</FictionBook>`)
	}

	p := NewFb2Provider()

	t.Run("полный перевод: src-title-info + translator + publish-info", func(t *testing.T) {
		desc := `
		<title-info>
			<book-title>Властелин колец</book-title>
			<lang>ru</lang>
			<src-lang>en</src-lang>
			<translator><first-name>Виктор</first-name><last-name>Муравьёв</last-name></translator>
		</title-info>
		<src-title-info>
			<book-title>The Lord of the Rings</book-title>
			<author><first-name>John</first-name><middle-name>Ronald Reuel</middle-name><last-name>Tolkien</last-name></author>
			<lang>en</lang>
		</src-title-info>
		<document-info><id>abc-123-guid</id></document-info>
		<publish-info>
			<book-name>Властелин колец: Братство Кольца</book-name>
			<publisher>АСТ</publisher>
			<year>2015</year>
			<isbn>978-5-17-090556-3</isbn>
		</publish-info>`
		em, err := p.FetchEditionMeta(context.Background(), BookQuery{ArchivePath: makeFB2Archive(t, wrap(desc)), FB2Name: "book.fb2"})
		require.NoError(t, err)
		require.Equal(t, "Муравьёв Виктор", em.Translator)
		require.Equal(t, "The Lord of the Rings", em.SrcTitle)
		require.Equal(t, "Tolkien John Ronald Reuel", em.SrcAuthor)
		require.Equal(t, "en", em.SrcLang)
		require.Equal(t, "ru", em.TitleLang)
		require.Equal(t, "Властелин колец: Братство Кольца", em.EditionTitle)
		require.Equal(t, "АСТ", em.Publisher)
		require.Equal(t, 2015, em.EditionYear)
		require.Equal(t, "9785170905563", em.ISBN)
		require.Equal(t, "abc-123-guid", em.FB2DocID)
	})

	t.Run("src-lang из src-title-info/lang когда в title-info его нет", func(t *testing.T) {
		desc := `<title-info><book-title>X</book-title><lang>ru</lang></title-info>
		<src-title-info><book-title>Original</book-title><lang>de</lang></src-title-info>`
		em, err := p.FetchEditionMeta(context.Background(), BookQuery{ArchivePath: makeFB2Archive(t, wrap(desc)), FB2Name: "book.fb2"})
		require.NoError(t, err)
		require.Equal(t, "de", em.SrcLang)
		require.Equal(t, "Original", em.SrcTitle)
	})

	t.Run("оригинал без перевода: пустые src-поля", func(t *testing.T) {
		desc := `<title-info><book-title>Война и мир</book-title><lang>ru</lang></title-info>
		<publish-info><publisher>Эксмо</publisher><year>2007</year></publish-info>`
		em, err := p.FetchEditionMeta(context.Background(), BookQuery{ArchivePath: makeFB2Archive(t, wrap(desc)), FB2Name: "book.fb2"})
		require.NoError(t, err)
		require.Empty(t, em.SrcTitle)
		require.Empty(t, em.SrcAuthor)
		require.Empty(t, em.Translator)
		require.Equal(t, "Эксмо", em.Publisher)
		require.Equal(t, 2007, em.EditionYear)
	})

	t.Run("title-info author НЕ попадает в SrcAuthor", func(t *testing.T) {
		// Автор книги в title-info не должен ошибочно попасть в src_author.
		desc := `<title-info><author><first-name>Лев</first-name><last-name>Толстой</last-name></author><book-title>X</book-title></title-info>`
		em, err := p.FetchEditionMeta(context.Background(), BookQuery{ArchivePath: makeFB2Archive(t, wrap(desc)), FB2Name: "book.fb2"})
		require.NoError(t, err)
		require.Empty(t, em.SrcAuthor)
	})

	t.Run("мусорный isbn отбрасывается", func(t *testing.T) {
		desc := `<title-info><book-title>X</book-title></title-info><publish-info><isbn>нет</isbn></publish-info>`
		em, err := p.FetchEditionMeta(context.Background(), BookQuery{ArchivePath: makeFB2Archive(t, wrap(desc)), FB2Name: "book.fb2"})
		require.NoError(t, err)
		require.Empty(t, em.ISBN)
	})
}

func TestFb2Provider_FetchEditionMeta_MissingArchive(t *testing.T) {
	p := NewFb2Provider()
	_, err := p.FetchEditionMeta(context.Background(), BookQuery{})
	require.ErrorIs(t, err, ErrNotFound)
}
