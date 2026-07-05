package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/skriptes/skriptes/backend/internal/settings"
)

// ContentDeps — настройки видимости контента (скрытые жанры/языки).
// Resolver может быть nil — тогда фильтрация выдачи и hard-block выключены
// (поведение «как раньше»: всё видно).
type ContentDeps struct {
	Resolver *settings.ContentResolver
}

// contentSettings — DTO скрытых жанров/языков. Используется и в admin-, и в
// profile-эндпоинтах (PUT-тело и часть GET-ответа).
type contentSettings struct {
	HiddenGenres    []string `json:"hidden_genres"`
	HiddenLanguages []string `json:"hidden_languages"`
	// HideCompilations — «Скрывать сборники» (opt-in, только персональные
	// настройки профиля; admin-UI переключатель не показывает).
	HideCompilations bool `json:"hide_compilations,omitempty"`
}

// meContentResponse — персональные скрытые + глобально скрытые (admin),
// чтобы UI профиля показал admin-скрытые как заблокированные (их нельзя
// включить обратно), а персональные — редактируемыми.
type meContentResponse struct {
	HiddenGenres         []string `json:"hidden_genres"`
	HiddenLanguages      []string `json:"hidden_languages"`
	HideCompilations     bool     `json:"hide_compilations"`
	AdminHiddenGenres    []string `json:"admin_hidden_genres"`
	AdminHiddenLanguages []string `json:"admin_hidden_languages"`
}

// requireBookVisible — middleware для маршрутов «по id книги» (детал ка,
// скачивание, ридер, обложка, send-to-kindle): если контент книги скрыт
// ГЛОБАЛЬНО (admin-настройки), отдаёт 404 даже по прямой ссылке.
//
// Персональные настройки сюда НЕ входят: они лишь убирают книгу из выдачи,
// но не блокируют прямой доступ (выбор «профиль — из выдачи»).
//
// Оптимизация: если глобально ничего не скрыто (дефолт) — гейт бесплатный,
// без единого запроса в БД. Лёгкий запрос genres+lang делается только когда
// админ что-то скрыл. На ошибку чтения деградируем мягко (пропускаем) —
// сам handler ниже корректно отработает отсутствие книги.
func requireBookVisible(content ContentDeps, bd BooksDeps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if content.Resolver != nil && bd.Service != nil {
				admin := content.Resolver.Admin()
				if len(admin.HiddenGenres) > 0 || len(admin.HiddenLanguages) > 0 {
					if id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64); err == nil && id > 0 {
						ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
						codes, lang, qErr := bd.Service.GenresAndLang(ctx, id)
						cancel()
						if qErr == nil && admin.Hides(codes, lang) {
							http.NotFound(w, r)
							return
						}
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// handleGetAdminContent — GET /api/admin/content. Текущие глобальные скрытые
// жанры/языки.
func handleGetAdminContent(d ContentDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		cfg := d.Resolver.Admin()
		writeJSON(w, http.StatusOK, contentSettings{
			HiddenGenres:    orEmpty(cfg.HiddenGenres),
			HiddenLanguages: orEmpty(cfg.HiddenLanguages),
		})
	}
}

// handleUpdateAdminContent — PUT /api/admin/content. Сохраняет глобальные
// скрытые жанры/языки и живо обновляет кэш (применяется без рестарта).
func handleUpdateAdminContent(d ContentDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, ok := decodeContentBody(w, r)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		cfg := settings.ContentConfig{HiddenGenres: body.HiddenGenres, HiddenLanguages: body.HiddenLanguages}
		if err := d.Resolver.SetAdmin(ctx, cfg); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save content settings failed"})
			return
		}
		out := d.Resolver.Admin()
		writeJSON(w, http.StatusOK, contentSettings{
			HiddenGenres:    orEmpty(out.HiddenGenres),
			HiddenLanguages: orEmpty(out.HiddenLanguages),
		})
	}
}

// handleGetMeContent — GET /api/me/content. Персональные скрытые жанры/языки
// + глобально скрытые (для блокировки в UI).
func handleGetMeContent(d ContentDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		user, err := d.Resolver.User(ctx, u.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read content settings failed"})
			return
		}
		admin := d.Resolver.Admin()
		writeJSON(w, http.StatusOK, meContentResponse{
			HiddenGenres:         orEmpty(user.HiddenGenres),
			HiddenLanguages:      orEmpty(user.HiddenLanguages),
			HideCompilations:     user.HideCompilations,
			AdminHiddenGenres:    orEmpty(admin.HiddenGenres),
			AdminHiddenLanguages: orEmpty(admin.HiddenLanguages),
		})
	}
}

// handleUpdateMeContent — PUT /api/me/content. Сохраняет ПЕРСОНАЛЬНЫЕ скрытые
// жанры/языки. Не может переопределить admin-настройки (они применяются как
// объединение в выдаче независимо от персональных).
func handleUpdateMeContent(d ContentDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		body, ok := decodeContentBody(w, r)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		cfg := settings.ContentConfig{HiddenGenres: body.HiddenGenres, HiddenLanguages: body.HiddenLanguages, HideCompilations: body.HideCompilations}
		if err := d.Resolver.SetUser(ctx, u.ID, cfg); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save content settings failed"})
			return
		}
		user, err := d.Resolver.User(ctx, u.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read content settings failed"})
			return
		}
		admin := d.Resolver.Admin()
		writeJSON(w, http.StatusOK, meContentResponse{
			HiddenGenres:         orEmpty(user.HiddenGenres),
			HiddenLanguages:      orEmpty(user.HiddenLanguages),
			HideCompilations:     user.HideCompilations,
			AdminHiddenGenres:    orEmpty(admin.HiddenGenres),
			AdminHiddenLanguages: orEmpty(admin.HiddenLanguages),
		})
	}
}

// handleEffectiveContent — GET /api/content/effective. Объединение скрытого
// (admin ∪ персональное) для текущего пользователя. Фронт прячет эти
// жанры/языки из панели фильтров (бэкенд уже исключает их из выдачи —
// это лишь для чистого UI).
func handleEffectiveContent(d ContentDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var uid int64
		if u, ok := UserFromContext(r.Context()); ok {
			uid = u.ID
		}
		genres, langs, hideComps := d.Resolver.Exclusions(r.Context(), uid)
		writeJSON(w, http.StatusOK, contentSettings{
			HiddenGenres:     orEmpty(genres),
			HiddenLanguages:  orEmpty(langs),
			HideCompilations: hideComps,
		})
	}
}

// decodeContentBody читает и валидирует тело PUT-эндпоинтов content.
// Возвращает ok=false и уже отправленный ответ при ошибке.
func decodeContentBody(w http.ResponseWriter, r *http.Request) (contentSettings, bool) {
	var body contentSettings
	// 64KB c запасом: полный словарь ~250 жанров ≈ 5KB.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return contentSettings{}, false
	}
	return body, true
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
