package metadata

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseFb2Year(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"1869-01-01", 1869},
		{"1869", 1869},
		{"2025", 2025},
		{"", 0},
		{"XIX век", 0},
		{"написано в 1980 году", 1980},
		{"1980-е годы", 1980},
		{"3000", 0}, // вне диапазона regexp
		{"999", 0},  // до 1000
		{"abcd", 0}, // не год
	}
	for _, c := range cases {
		require.Equalf(t, c.want, parseFb2Year(c.in), "parseFb2Year(%q)", c.in)
	}
}

func TestFb2Provider_FetchYears(t *testing.T) {
	wrap := func(desc string) []byte {
		return []byte(`<?xml version="1.0"?>
<FictionBook>
  <description>` + desc + `</description>
  <body><p>text</p></body>
</FictionBook>`)
	}

	cases := []struct {
		name        string
		desc        string
		wantWritten int
		wantEdition int
	}{
		{
			name:        "title-info date value-атрибут",
			desc:        `<title-info><date value="1869-01-01">1869</date></title-info>`,
			wantWritten: 1869,
		},
		{
			name:        "title-info date только текст",
			desc:        `<title-info><date>1990</date></title-info>`,
			wantWritten: 1990,
		},
		{
			name:        "publish-info year → edition, без written",
			desc:        `<title-info></title-info><publish-info><year>2003</year></publish-info>`,
			wantEdition: 2003,
		},
		{
			name:        "оба года — разные сущности",
			desc:        `<title-info><date>1869</date></title-info><publish-info><year>2003</year></publish-info>`,
			wantWritten: 1869,
			wantEdition: 2003,
		},
		{
			name: "нет годов",
			desc: `<title-info><book-title>X</book-title></title-info>`,
		},
		{
			name: "мусорный текст даты → 0",
			desc: `<title-info><date>XIX век</date></title-info>`,
		},
		{
			name: "document-info/date игнорируется (это дата файла)",
			desc: `<title-info></title-info><document-info><date value="2005-05-05">2005</date></document-info>`,
		},
		{
			name: "src-title-info/date игнорируется (инфо об оригинале)",
			desc: `<src-title-info><date value="1955-01-01">1955</date></src-title-info><title-info></title-info>`,
		},
	}

	p := NewFb2Provider()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			zipPath := makeFB2Archive(t, wrap(c.desc))
			w, e, err := p.FetchYears(context.Background(), BookQuery{
				ArchivePath: zipPath, FB2Name: "book.fb2",
			})
			require.NoError(t, err)
			require.Equal(t, c.wantWritten, w, "written_year")
			require.Equal(t, c.wantEdition, e, "edition_year")
		})
	}
}

func TestFb2Provider_FetchYears_MissingArchive(t *testing.T) {
	p := NewFb2Provider()
	_, _, err := p.FetchYears(context.Background(), BookQuery{})
	require.ErrorIs(t, err, ErrNotFound)
}
