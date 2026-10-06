package history_test

import (
	"context"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/history"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestAnnotations — закладки и выделения (#389, B6): сохранение, то же место —
// обновление, заметка, порядок по ходу книги, «Мои заметки» по всем изданиям
// работы, чужое не трогается.
func TestAnnotations(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	svc := history.New(pool)

	var collID, archID, workID int64
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO collections (name, inpx_filename) VALUES ('c','c.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, collID).Scan(&archID))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO works (title, normalized_title) VALUES ('Дюна','дюна') RETURNING id`).Scan(&workID))
	edition := func(lib, title, lang string) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, work_id, lang)
			VALUES ($1,$2,$3,$4,'fb2',$5,$6,$7,$8) RETURNING id`, collID, archID, lib, lib, title, title, workID, lang).Scan(&id))
		return id
	}
	ru, en := edition("1", "Дюна", "ru"), edition("2", "Dune", "en")
	user := func(email string) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx, `INSERT INTO users (email, display_name, password_hash, role)
			VALUES ($1,'U','x','user') RETURNING id`, email).Scan(&id))
		return id
	}
	me, other := user("me@e.com"), user("other@e.com")
	f := func(v float64) *float64 { return &v }

	_, err := svc.SaveAnnotation(ctx, me, ru, history.Annotation{Kind: "note", CFI: "x"})
	require.ErrorIs(t, err, history.ErrInvalidAnnotation)

	late, err := svc.SaveAnnotation(ctx, me, ru, history.Annotation{Kind: "highlight", CFI: "epubcfi(/6/8!/4/2,/1:0,/1:20)",
		Excerpt: "Страх убивает разум.", Fraction: f(0.6), Label: "Глава 3"})
	require.NoError(t, err)
	_, err = svc.SaveAnnotation(ctx, me, ru, history.Annotation{Kind: "bookmark", CFI: "epubcfi(/6/4!/4/2)", Fraction: f(0.1)})
	require.NoError(t, err)
	again, err := svc.SaveAnnotation(ctx, me, ru, history.Annotation{Kind: "highlight", CFI: "epubcfi(/6/8!/4/2,/1:0,/1:20)",
		Excerpt: "Страх убивает разум.", Note: "литания", Fraction: f(0.6)})
	require.NoError(t, err)
	require.Equal(t, late.ID, again.ID, "то же место — та же запись")
	require.Equal(t, "литания", again.Note)

	list, err := svc.BookAnnotations(ctx, me, ru)
	require.NoError(t, err)
	require.Len(t, list, 2)
	require.Equal(t, "bookmark", list[0].Kind, "по ходу книги")

	_, err = svc.SaveAnnotation(ctx, me, en, history.Annotation{Kind: "highlight", CFI: "epubcfi(/6/2!/4/2,/1:0,/1:5)", Excerpt: "Fear"})
	require.NoError(t, err)
	work, err := svc.WorkAnnotations(ctx, me, workID)
	require.NoError(t, err)
	require.Len(t, work, 3, "все издания работы")
	require.Equal(t, "Dune", work[2].EditionTitle)

	_, err = svc.UpdateAnnotationNote(ctx, other, late.ID, "чужое")
	require.ErrorIs(t, err, history.ErrAnnotationNotFound)
	require.ErrorIs(t, svc.DeleteAnnotation(ctx, other, late.ID), history.ErrAnnotationNotFound)
	upd, err := svc.UpdateAnnotationNote(ctx, me, late.ID, "  о страхе  ")
	require.NoError(t, err)
	require.Equal(t, "о страхе", upd.Note)
	require.NoError(t, svc.DeleteAnnotation(ctx, me, late.ID))
	list, err = svc.BookAnnotations(ctx, me, ru)
	require.NoError(t, err)
	require.Len(t, list, 1)
	none, err := svc.BookAnnotations(ctx, other, ru)
	require.NoError(t, err)
	require.Empty(t, none)
}
