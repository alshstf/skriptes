package metadata

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestWorkYearRules — #465: год раньше рождения автора (+10) не берётся (Азимов
// «Сами боги» с опечаткой 1073); год Фантлаба сильнее fb2-года, который раньше
// него больше чем на 100 лет; год издания раньше 1450 и равный ему fb2-год —
// заглушки; у средневекового автора ранний год остаётся.
func TestWorkYearRules(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	collID, archID := seedTitleFixture(t, ctx, pool)
	exec := func(sql string, args ...any) {
		t.Helper()
		_, err := pool.Exec(ctx, sql, args...)
		require.NoError(t, err)
	}
	years := func(book int64, written, edition int) {
		exec(`UPDATE books SET written_year = NULLIF($2,0), written_year_source = CASE WHEN $2 > 0 THEN 'fb2_title' END,
			edition_year = NULLIF($3,0) WHERE id = $1`, book, written, edition)
	}
	year := func(work int64) int {
		var y *int
		require.NoError(t, pool.QueryRow(ctx, `SELECT written_year FROM works WHERE id = $1`, work).Scan(&y))
		if y == nil {
			return 0
		}
		return *y
	}

	asimov := seedGroupAuthor(t, ctx, pool, "Азимов", "азимов айзек")
	exec(`UPDATE authors SET born_year = 1920, died_year = 1992 WHERE id = $1`, asimov)
	gods1 := seedGroupBook(t, ctx, pool, collID, archID, asimov, "A1", "Сами боги", "сами боги", "ru", "", "", "")
	gods2 := seedGroupBook(t, ctx, pool, collID, archID, asimov, "A2", "Les dieux eux-mêmes", "les dieux eux-mêmes", "fr", "", "", "")
	gods := workIDOf(t, ctx, pool, gods1)
	exec(`UPDATE books SET work_id = $1 WHERE id = $2`, gods, gods2)
	years(gods1, 1972, 1976)
	years(gods2, 1073, 1982)

	lukyanenko := seedGroupAuthor(t, ctx, pool, "Лукьяненко", "лукьяненко сергей")
	breach := seedGroupBook(t, ctx, pool, collID, archID, lukyanenko, "L1", "Нарушение", "нарушение", "ru", "", "", "")
	years(breach, 1014, 2007)
	exec(`UPDATE works SET external_year = 1989, external_year_source = 'fantlab' WHERE id = $1`, workIDOf(t, ctx, pool, breach))

	charushin := seedGroupAuthor(t, ctx, pool, "Чарушин", "чарушин евгений")
	// Посмертная публикация: написано в 1835-м, Фантлаб даёт 1938 — год написания остаётся.
	pushkin := seedGroupAuthor(t, ctx, pool, "Пушкин", "пушкин александр")
	peter := seedGroupBook(t, ctx, pool, collID, archID, pushkin, "P1", "История Петра I", "история петра i", "ru", "", "", "")
	years(peter, 1835, 1950)
	exec(`UPDATE works SET external_year = 1938, external_year_source = 'fantlab' WHERE id = $1`, workIDOf(t, ctx, pool, peter))

	boy := seedGroupBook(t, ctx, pool, collID, archID, charushin, "C1", "Глупый мальчишка", "глупый мальчишка", "ru", "", "", "")
	years(boy, 1000, 1000)

	boccaccio := seedGroupAuthor(t, ctx, pool, "Боккаччо", "боккаччо джованни")
	exec(`UPDATE authors SET born_year = 1313 WHERE id = $1`, boccaccio)
	dec := seedGroupBook(t, ctx, pool, collID, archID, boccaccio, "B1", "Декамерон", "декамерон", "ru", "", "", "")
	years(dec, 1353, 1970)

	// Издание без года издания — fb2-год остаётся (NULL в сравнении не отбрасывает).
	crusoe := seedGroupBook(t, ctx, pool, collID, archID, boccaccio, "R1", "Робинзон", "робинзон", "ru", "", "", "")
	years(crusoe, 1719, 0)

	// Старинная литература: год текста, продублированный в год издания, — не заглушка.
	filo := seedGroupBook(t, ctx, pool, collID, archID, boccaccio, "F1", "Филострато", "филострато", "ru", "", "", "")
	years(filo, 1335, 1335)
	exec(`INSERT INTO genres (fb2_code, name_ru) VALUES ('antique_european', 'Европейская старинная литература') ON CONFLICT DO NOTHING`)
	exec(`INSERT INTO book_genres (book_id, genre_id) SELECT $1, id FROM genres WHERE fb2_code = 'antique_european'`, filo)

	_, err := RecomputeAllWorkYears(ctx, pool)
	require.NoError(t, err)
	require.Equal(t, 1335, year(workIDOf(t, ctx, pool, filo)), "старинная литература — год остаётся")
	require.Equal(t, 1719, year(workIDOf(t, ctx, pool, crusoe)), "без года издания fb2-год остаётся")
	require.Equal(t, 1972, year(gods), "1073 раньше рождения Азимова — опечатка")
	require.Equal(t, 1989, year(workIDOf(t, ctx, pool, breach)), "год Фантлаба сильнее fb2-года на 975 лет раньше")
	require.Equal(t, 1835, year(workIDOf(t, ctx, pool, peter)), "посмертная публикация — год написания остаётся")
	require.Zero(t, year(workIDOf(t, ctx, pool, boy)), "1000 = год издания 1000 — заглушка")
	require.Equal(t, 1353, year(workIDOf(t, ctx, pool, dec)), "средневековый автор — ранний год остаётся")

	// Годы жизни пришли позже — шаг после импорта чинит год.
	exec(`UPDATE authors SET born_year = NULL WHERE id = $1`, asimov)
	_, err = RecomputeAllWorkYears(ctx, pool)
	require.NoError(t, err)
	require.Equal(t, 1073, year(gods), "без годов жизни опечатка побеждает, как раньше")
	exec(`UPDATE authors SET born_year = 1920 WHERE id = $1`, asimov)
	changed, err := RecomputeYearsBeforeBirth(ctx, pool)
	require.NoError(t, err)
	require.Equal(t, []int64{gods}, changed)
	require.Equal(t, 1972, year(gods))
}

// TestRecordingCandidateCheck — принятый кандидат с QID пишет автору QID и годы
// жизни; отвергнутый — ничего.
func TestRecordingCandidateCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	a := seedGroupAuthor(t, ctx, pool, "Азимов", "азимов айзек")
	b := seedGroupAuthor(t, ctx, pool, "Тёзка", "тёзка")
	facts := func(_ context.Context, qid string) (CandidateFacts, error) {
		return CandidateFacts{QID: qid, Born: 1920, Died: 1992}, nil
	}
	accept := func(_ context.Context, q AuthorQuery, _, _, _, _ string, _ MatchKind) (bool, error) {
		return q.ID == a, nil
	}
	check := RecordingCandidateCheck(accept, facts, pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ok, err := check(ctx, AuthorQuery{ID: a}, "wikipedia", "ru", "Азимов, Айзек", "Q34981", MatchName)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = check(ctx, AuthorQuery{ID: b}, "wikipedia", "ru", "Тёзка", "Q1", MatchName)
	require.NoError(t, err)
	require.False(t, ok)

	var qid string
	var born, died *int
	require.NoError(t, pool.QueryRow(ctx, `SELECT COALESCE(ext_ids->>'wd_qid',''), born_year, died_year FROM authors WHERE id = $1`, a).Scan(&qid, &born, &died))
	require.Equal(t, "Q34981", qid)
	require.Equal(t, 1920, *born)
	require.Equal(t, 1992, *died)
	require.NoError(t, pool.QueryRow(ctx, `SELECT COALESCE(ext_ids->>'wd_qid',''), born_year FROM authors WHERE id = $1`, b).Scan(&qid, &born))
	require.Empty(t, qid)
	require.Nil(t, born)
}
