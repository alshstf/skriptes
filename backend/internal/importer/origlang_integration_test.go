package importer_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	meili "github.com/meilisearch/meilisearch-go"
	"github.com/stretchr/testify/require"
)

// worksDocOrigLangs читает orig_lang дока работы прямо из works-индекса.
func worksDocOrigLangs(ctx context.Context, mgr meili.ServiceManager, workID int64) ([]string, error) {
	var doc struct {
		OrigLangs []string `json:"orig_lang"`
	}
	err := mgr.Index("works").GetDocumentWithContext(ctx, strconv.FormatInt(workID, 10),
		&meili.DocumentQuery{Fields: []string{"id", "orig_lang"}}, &doc)
	return doc.OrigLangs, err
}

// orig_lang — WORK-LEVEL эффективный оригинал (схема v8): у изданий одной
// работы оригинал один, поэтому непустой src_lang ЛЮБОГО издания определяет
// оригинал(ы) всей работы, а издания без src_lang НЕ считаются нативами.
// Прод-кейс «Разум и чувства»: испанский перевод без fb2 <src-lang> рядом с
// ru-изданием (src_lang=en) раньше тащил 'es' в фасет «Язык оригинала».
// Фолбэк: src_lang нет ни у кого → работа нативна, orig_lang = языки изданий.
func TestResyncWorksIndex_OrigLangWorkLevel(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	imp, mgr, _, bookID, workID, execSQL := popularitySetup(t, ctx)

	// Работа из двух изданий: ru-перевод (src_lang=en) + испанский
	// перевод-сирота (src_lang пуст).
	execSQL(`UPDATE books SET lang = 'ru', src_lang = 'en' WHERE id = $1`, bookID)
	execSQL(`
		INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, lang, src_lang, work_id)
		SELECT collection_id, archive_id, 'orig-es', 'f', 'fb2', 'Sentido y Sensibilidad', 'sentido y sensibilidad', 'es', NULL, work_id
		FROM books WHERE id = $1`, bookID)

	n, err := imp.ResyncWorksIndex(ctx)
	require.NoError(t, err)
	require.Greater(t, n, 0)

	langs, err := worksDocOrigLangs(ctx, mgr, workID)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"en"}, langs,
		"оригинал работы известен от ru-издания (src=en); es-сирота — НЕ натив")

	// Фолбэк: src_lang стёрт у всех изданий → работа нативна, union языков.
	execSQL(`UPDATE books SET src_lang = NULL WHERE work_id = $1`, workID)
	n, err = imp.ResyncWorksIndex(ctx)
	require.NoError(t, err)
	require.Greater(t, n, 0)

	langs, err = worksDocOrigLangs(ctx, mgr, workID)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"ru", "es"}, langs,
		"src_lang нет ни у кого → фолбэк на языки изданий (натив)")
}
