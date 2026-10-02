package metadata

import (
	"context"
	"errors"
	"fmt"
)

// Сухой прогон обогащения автора (#280, разбор ошибок био и фото): что нашли бы
// текущие провайдеры и гейты и почему — с трассой каждого шага. В базу не
// пишет, фото не скачивает (берёт только адрес). Тот же порядок провайдеров и
// та же логика «сбой ≠ не найдено», что у перепроверки (fetchAuthorBio/Photo).

// AuthorExplain — итог сухого прогона по одному автору.
type AuthorExplain struct {
	ID           int64         `json:"id"`
	LastName     string        `json:"last_name"`
	FirstName    string        `json:"first_name,omitempty"`
	MiddleName   string        `json:"middle_name,omitempty"`
	Note         string        `json:"note,omitempty"`
	Namesakes    bool          `json:"namesakes,omitempty"`
	Strict       bool          `json:"strict,omitempty"`
	BookTitles   []string      `json:"book_titles,omitempty"`
	Renown       int64         `json:"renown"`
	CurrentBio   string        `json:"current_bio,omitempty"`   // начало сохранённой био
	CurrentPhoto string        `json:"current_photo,omitempty"` // имя файла в кэше фото
	Bio          ExplainResult `json:"bio"`
	Photo        ExplainResult `json:"photo"`
}

// ExplainResult — что нашёл бы поиск одного поля.
type ExplainResult struct {
	Provider  string      `json:"provider,omitempty"` // кто нашёл; пусто — никто
	Value     string      `json:"value,omitempty"`    // начало био или адрес фото
	Transient bool        `json:"transient,omitempty"`
	Reason    string      `json:"reason"`
	Steps     []TraceStep `json:"steps"`
}

// explainBioRunes — сколько текста био отдавать: для разметки «тот ли человек»
// хватает начала статьи (кто, годы жизни, чем известен).
const explainBioRunes = 1000

// authorPhotoSourcer — провайдер умеет назвать адрес фото, не скачивая его.
type authorPhotoSourcer interface {
	AuthorPhotoSource(ctx context.Context, q AuthorQuery) (string, error)
}

// ExplainAuthor — сухой прогон по автору id.
func (e *Enricher) ExplainAuthor(ctx context.Context, id int64) (AuthorExplain, error) {
	ex := AuthorExplain{ID: id}
	var fullName string
	if err := e.pool.QueryRow(ctx, `
		SELECT COALESCE(last_name, ''), COALESCE(first_name, ''), COALESCE(middle_name, ''),
		       TRIM(CONCAT_WS(' ', last_name, first_name, middle_name)),
		       COALESCE(bio, ''), COALESCE(photo_path, ''), COALESCE(renown, 0)
		FROM authors WHERE id = $1`, id).Scan(&ex.LastName, &ex.FirstName, &ex.MiddleName, &fullName,
		&ex.CurrentBio, &ex.CurrentPhoto, &ex.Renown); err != nil {
		return ex, fmt.Errorf("read author %d: %w", id, err)
	}
	ex.CurrentBio = clipRunes(ex.CurrentBio, explainBioRunes)
	q := e.withNamesakeContext(ctx, AuthorQuery{
		ID: id, LastName: ex.LastName, FirstName: ex.FirstName, MiddleName: ex.MiddleName, FullName: fullName,
	})
	ex.Note, ex.Namesakes, ex.Strict, ex.BookTitles = q.Note, q.Namesakes, q.Strict(), q.BookTitles

	var bt AuthorTrace
	bctx := WithAuthorTrace(ctx, &bt)
	for _, p := range e.authorBioProviders {
		text, err := p.FetchAuthorBio(bctx, q)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			ex.Bio.Transient = true
			continue
		}
		if text != "" {
			ex.Bio.Provider, ex.Bio.Value, ex.Bio.Transient = p.Name(), clipRunes(text, explainBioRunes), false
			break
		}
	}
	ex.Bio.Reason, ex.Bio.Steps = bt.Reason(), bt.Steps()

	var pt AuthorTrace
	pctx := WithAuthorTrace(ctx, &pt)
	for _, p := range e.authorPhotoProviders {
		ps, ok := p.(authorPhotoSourcer)
		if !ok {
			continue
		}
		src, err := ps.AuthorPhotoSource(pctx, q)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			ex.Photo.Transient = true
			continue
		}
		if src != "" {
			ex.Photo.Provider, ex.Photo.Value, ex.Photo.Transient = p.Name(), src, false
			break
		}
	}
	ex.Photo.Reason, ex.Photo.Steps = pt.Reason(), pt.Steps()
	return ex, nil
}
