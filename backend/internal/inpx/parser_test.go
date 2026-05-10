package inpx_test

import (
	"strings"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/inpx"
	"github.com/stretchr/testify/require"
)

// helper для построения записи из map "field name → value" по DefaultSchema.
func mustRecord(t *testing.T, m map[string]string) inpx.Record {
	t.Helper()
	parts := make([]string, len(inpx.DefaultSchema))
	for i, name := range inpx.DefaultSchema {
		parts[i] = m[name]
	}
	line := []byte(strings.Join(parts, "\x04"))
	r, err := inpx.ParseRecord(line, inpx.DefaultSchema)
	require.NoError(t, err)
	return r
}

func TestParseRecord_Minimal(t *testing.T) {
	r := mustRecord(t, map[string]string{
		inpx.FieldAuthor: "Иванов,Иван,Иванович:",
		inpx.FieldGenre:  "sf:",
		inpx.FieldTitle:  "Хроники",
		inpx.FieldFile:   "12345",
		inpx.FieldSize:   "1024",
		inpx.FieldLibID:  "12345",
		inpx.FieldDel:    "",
		inpx.FieldExt:    "fb2",
		inpx.FieldDate:   "2023-06-15",
		inpx.FieldLang:   "ru",
	})
	require.Equal(t, "Хроники", r.Title)
	require.False(t, r.Deleted)
	require.Equal(t, int64(1024), r.Size)
	require.Equal(t, "ru", r.Lang)
	require.Equal(t, "12345", r.LibID)
	require.Equal(t, "fb2", r.Ext)
	require.NotNil(t, r.Date)
	require.Equal(t, "2023-06-15", r.Date.Format("2006-01-02"))
	require.Len(t, r.Authors, 1)
	require.Equal(t, inpx.Author{LastName: "Иванов", FirstName: "Иван", MiddleName: "Иванович"}, r.Authors[0])
	require.Equal(t, []string{"sf"}, r.Genres)
}

func TestParseRecord_MultipleAuthorsAndGenres(t *testing.T) {
	r := mustRecord(t, map[string]string{
		inpx.FieldAuthor: "Стругацкий,Аркадий,Натанович:Стругацкий,Борис,Натанович:",
		inpx.FieldGenre:  "sf_social:sf:prose_classic:",
		inpx.FieldTitle:  "Понедельник начинается в субботу",
	})
	require.Len(t, r.Authors, 2)
	require.Equal(t, "Аркадий", r.Authors[0].FirstName)
	require.Equal(t, "Борис", r.Authors[1].FirstName)
	require.Equal(t, []string{"sf_social", "sf", "prose_classic"}, r.Genres)
}

func TestParseRecord_EmptyOptionalFields(t *testing.T) {
	r := mustRecord(t, map[string]string{
		inpx.FieldAuthor: "Doe,John,:",
		inpx.FieldTitle:  "T",
		// SERIES, SERNO, SIZE, DATE, LANG, LIBRATE, KEYWORDS — все пустые
	})
	require.Equal(t, "", r.Series)
	require.Equal(t, 0, r.SerNo)
	require.Equal(t, int64(0), r.Size)
	require.Nil(t, r.Date)
	require.Equal(t, 0, r.Rating)
	require.Equal(t, "", r.Keywords)
	require.Len(t, r.Authors, 1)
	require.Equal(t, "John", r.Authors[0].FirstName)
	require.Equal(t, "", r.Authors[0].MiddleName)
}

func TestParseRecord_Deleted(t *testing.T) {
	r := mustRecord(t, map[string]string{
		inpx.FieldAuthor: "X,Y,:",
		inpx.FieldTitle:  "Removed",
		inpx.FieldDel:    "1",
	})
	require.True(t, r.Deleted)
}

func TestParseRecord_BadDateIsNil(t *testing.T) {
	r := mustRecord(t, map[string]string{
		inpx.FieldAuthor: "X,Y,:",
		inpx.FieldTitle:  "T",
		inpx.FieldDate:   "not-a-date",
	})
	require.Nil(t, r.Date, "невалидная дата должна стать nil, а не ошибкой")
}

func TestParseRecord_BadIntIsError(t *testing.T) {
	parts := make([]string, len(inpx.DefaultSchema))
	for i, name := range inpx.DefaultSchema {
		switch name {
		case inpx.FieldAuthor:
			parts[i] = "X,Y,:"
		case inpx.FieldTitle:
			parts[i] = "T"
		case inpx.FieldSize:
			parts[i] = "not-a-number"
		}
	}
	_, err := inpx.ParseRecord([]byte(strings.Join(parts, "\x04")), inpx.DefaultSchema)
	require.Error(t, err)
	require.Contains(t, err.Error(), "SIZE")
}

func TestParseRecord_ExtraFieldsGoToExtra(t *testing.T) {
	short := inpx.Schema{inpx.FieldAuthor, inpx.FieldTitle}
	line := []byte("Doe,John,\x04Hello\x04extra1\x04extra2")
	r, err := inpx.ParseRecord(line, short)
	require.NoError(t, err)
	require.Equal(t, "Hello", r.Title)
	require.Equal(t, "extra1", r.Extra["_extra0"])
	require.Equal(t, "extra2", r.Extra["_extra1"])
}

func TestParseInp_StreamsAllRecords(t *testing.T) {
	t1 := buildLine(t, map[string]string{inpx.FieldAuthor: "A,B,:", inpx.FieldTitle: "One", inpx.FieldDate: "2020-01-01"})
	t2 := buildLine(t, map[string]string{inpx.FieldAuthor: "C,D,:", inpx.FieldTitle: "Two"})
	body := strings.Join([]string{string(t1), string(t2)}, "\r\n") + "\r\n"

	var got []string
	err := inpx.ParseInp(strings.NewReader(body), inpx.DefaultSchema, func(r inpx.Record) error {
		got = append(got, r.Title)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, []string{"One", "Two"}, got)
}

func TestParseInp_StopsOnCallbackError(t *testing.T) {
	t1 := buildLine(t, map[string]string{inpx.FieldAuthor: "A,B,:", inpx.FieldTitle: "One"})
	t2 := buildLine(t, map[string]string{inpx.FieldAuthor: "C,D,:", inpx.FieldTitle: "Two"})
	body := strings.Join([]string{string(t1), string(t2)}, "\r\n") + "\r\n"

	calls := 0
	err := inpx.ParseInp(strings.NewReader(body), inpx.DefaultSchema, func(_ inpx.Record) error {
		calls++
		if calls == 1 {
			return errStop
		}
		return nil
	})
	require.ErrorIs(t, err, errStop)
	require.Equal(t, 1, calls)
}

func TestParseSchema_DefaultsWhenEmpty(t *testing.T) {
	s, err := inpx.ParseSchema(strings.NewReader(""))
	require.NoError(t, err)
	require.Equal(t, inpx.DefaultSchema, s)
}

func TestParseSchema_ReadsLinesUppercased(t *testing.T) {
	s, err := inpx.ParseSchema(strings.NewReader("Author\nTitle\nFile\n"))
	require.NoError(t, err)
	require.Equal(t, inpx.Schema{"AUTHOR", "TITLE", "FILE"}, s)
}

// ── helpers ────────────────────────────────────────────────────

var errStop = stopErr{}

type stopErr struct{}

func (stopErr) Error() string { return "stop" }

func buildLine(t *testing.T, m map[string]string) []byte {
	t.Helper()
	parts := make([]string, len(inpx.DefaultSchema))
	for i, name := range inpx.DefaultSchema {
		parts[i] = m[name]
	}
	return []byte(strings.Join(parts, "\x04"))
}

// keep time used to avoid unused-import error if optimizer trims things.
var _ = time.Now
