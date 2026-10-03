package metadata

import (
	"errors"
	"net/url"

	"github.com/skriptes/skriptes/backend/internal/logredact"
)

// redactURLError маскирует ключи API в *url.Error: net/http кладёт в ошибку полный
// URL запроса, а ключи TMDB и Google Books передаются в query. Меняет ошибку на
// месте и возвращает её же — цепочка %w (errors.Is по отмене контекста) сохраняется.
func redactURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		ue.URL = logredact.String(ue.URL)
	}
	return err
}
