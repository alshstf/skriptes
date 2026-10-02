package metadata

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuthorTrace_Reason(t *testing.T) {
	var tr AuthorTrace
	ctx := WithAuthorTrace(context.Background(), &tr)
	// ru: неоднозначность (info) → строгий путь отверг; pass после reject не перебивает.
	traceStep(ctx, TraceStep{Source: "wikipedia", Lang: "ru", Stage: "name_gate", Outcome: TracePass, Value: "Дюма, Александр"})
	traceStep(ctx, TraceStep{Source: "wikipedia", Lang: "ru", Stage: "disambiguation", Outcome: TraceInfo})
	traceStep(ctx, TraceStep{Source: "wikipedia", Lang: "ru", Stage: "strict", Outcome: TraceReject, Value: "books=0"})
	traceStep(ctx, TraceStep{Source: "wikipedia", Lang: "ru", Stage: "occupation", Outcome: TracePass})
	// en: сбой запроса.
	traceStep(ctx, TraceStep{Source: "wikipedia", Lang: "en", Stage: "request", Outcome: TraceError, Value: "429"})
	// wikidata — вспомогательный источник, в причину не идёт.
	traceStep(ctx, TraceStep{Source: "wikidata", Stage: "occupations", Outcome: TraceInfo, Value: "писатель*"})
	// openlibrary: нашёл.
	traceStep(ctx, TraceStep{Source: "openlibrary", Stage: "name_gate", Outcome: TracePass})
	traceStep(ctx, TraceStep{Source: "openlibrary", Stage: "accept", Outcome: TracePass, Value: "OL1A"})

	require.Equal(t, "wikipedia/ru: reject strict «books=0»; wikipedia/en: error request «429»; openlibrary: pass accept «OL1A»",
		tr.Reason())
	require.Len(t, tr.Steps(), 8)
}

func TestTraceStep_NoTraceIsNoop(t *testing.T) {
	require.NotPanics(t, func() {
		traceStep(context.Background(), TraceStep{Source: "wikipedia", Stage: "accept", Outcome: TracePass})
	})
	require.False(t, traceOn(context.Background()))
	var nilTrace *AuthorTrace
	require.Empty(t, nilTrace.Steps())
}

func TestTraceStep_ClipsLongValues(t *testing.T) {
	var tr AuthorTrace
	ctx := WithAuthorTrace(context.Background(), &tr)
	traceStep(ctx, TraceStep{Source: "wikipedia", Stage: "article_is_author", Outcome: TraceReject, Value: strings.Repeat("я", 500)})
	v := tr.Steps()[0].Value
	require.Equal(t, traceValueRunes+1, len([]rune(v)), "обрезано до лимита + многоточие")
}

// Трасса провайдера Википедии: отказ по имени — с кандидатом, в обоих языках.
func TestWikipedia_Trace_NameGateReject(t *testing.T) {
	srv := wikiMockServer(t, []string{"Гарднер, Иван Алексеевич"}, wikiSummary{Title: "Гарднер", Type: "standard"},
		"Иван Алексеевич Гарднер — историк церковного пения.")
	defer srv.Close()
	p := NewWikipediaProvider(srv.Client()).WithAPIRoot(srv.URL)
	var tr AuthorTrace
	_, err := p.FetchAuthorBio(WithAuthorTrace(context.Background(), &tr),
		AuthorQuery{LastName: "Гарднер", FirstName: "Лиза", FullName: "Гарднер Лиза"})
	require.ErrorIs(t, err, ErrNotFound)
	require.Equal(t, "wikipedia/ru: reject name_gate «Гарднер, Иван Алексеевич»; "+
		"wikipedia/en: reject name_gate «Гарднер, Иван Алексеевич»", tr.Reason())
}

// Отказ по профессии: в трассе QID и вердикт; успех — шаг accept.
func TestWikipedia_Trace_Occupation(t *testing.T) {
	q := AuthorQuery{LastName: "Тёзка", FirstName: "Некий", FullName: "Тёзка Некий Иванович"}
	for _, c := range []struct {
		verdict OccupationVerdict
		want    string
	}{
		{OccupationNonWriter, "wikipedia/ru: reject occupation «non-writer»"},
		{OccupationWriter, "wikipedia/ru: pass accept"},
	} {
		t.Run(c.verdict.String(), func(t *testing.T) {
			srv := wikiGatedMockServer(t, "Тёзка, Некий Иванович", "Q1", "Некий Иванович Тёзка — писатель.")
			defer srv.Close()
			p := NewWikipediaProvider(srv.Client()).WithAPIRoot(srv.URL).
				WithOccupationGate(func(context.Context, string) (OccupationVerdict, error) { return c.verdict, nil })
			var tr AuthorTrace
			_, _ = p.FetchAuthorBio(WithAuthorTrace(context.Background(), &tr), q)
			require.True(t, strings.HasPrefix(tr.Reason(), c.want), tr.Reason())
			var qid string
			for _, s := range tr.Steps() {
				if s.Stage == "qid" {
					qid = s.Value
				}
			}
			require.Equal(t, "Q1", qid)
		})
	}
}
