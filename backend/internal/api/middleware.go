package api

import (
	"context"
	"net/http"
	"slices"
	"time"
)

// requireAuth — middleware: на каждый защищённый запрос вытаскивает
// session-cookie, ищет пользователя, кладёт его в request.Context.
// Если сессии нет / истекла — 401.
func requireAuth(d AuthDeps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, err := r.Cookie(sessionCookieName)
			if err != nil || c.Value == "" {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			defer cancel()
			user, ok := d.Service.UserByToken(ctx, c.Value)
			if !ok {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
				return
			}
			r = r.WithContext(context.WithValue(r.Context(), userCtxKey{}, user))
			next.ServeHTTP(w, r)
		})
	}
}

// requireAdmin не реализован в этом PR. Будет добавлен в PR 6 когда
// появятся admin-only эндпоинты (триггер импорта, управление пользователями).

// originCheck — простая CSRF-защита через сверку Origin / Referer на
// мутирующих методах. Срабатывает только для POST/PUT/PATCH/DELETE и
// только если в запросе есть Origin или Referer (т.е. это HTTPS-запрос
// из браузера). Для curl / нативных клиентов без Origin — пропускает.
//
// Это не замена нормальному CSRF-токену для критичных приложений, но
// для домашнего семейного сервера за SameSite=Lax cookies этого достаточно.
func originCheck(allowed []string) func(http.Handler) http.Handler {
	mutating := map[string]bool{
		http.MethodPost:   true,
		http.MethodPut:    true,
		http.MethodPatch:  true,
		http.MethodDelete: true,
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !mutating[r.Method] {
				next.ServeHTTP(w, r)
				return
			}
			origin := r.Header.Get("Origin")
			if origin == "" {
				origin = r.Header.Get("Referer")
			}
			if origin == "" {
				// Запрос без Origin/Referer (curl/wget/native client) пропускаем.
				next.ServeHTTP(w, r)
				return
			}
			if len(allowed) > 0 && !slices.ContainsFunc(allowed, func(a string) bool {
				return originMatches(origin, a)
			}) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "origin not allowed"})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// originMatches возвращает true если origin начинается с allowed.
// Достаточно для нашей модели: allowed = "https://skriptes.localhost"
// → строгое сравнение по префиксу схемы+хоста.
func originMatches(origin, allowed string) bool {
	if allowed == "" || origin == "" {
		return false
	}
	if origin == allowed {
		return true
	}
	// разрешаем "https://host:port/some/path" если allowed = "https://host:port"
	if len(origin) > len(allowed) && origin[:len(allowed)] == allowed {
		// следующий символ должен быть '/' или конец строки — иначе это другой хост
		next := origin[len(allowed)]
		return next == '/' || next == ':'
	}
	return false
}
