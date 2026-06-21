package api

import (
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/skriptes/skriptes/backend/internal/auth"
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

// requireAdmin — обёртка requireAuth с дополнительной проверкой role.
// Используется для /api/admin/* (управление пользователями). Если юзер
// не admin — 403 Forbidden (а не 401, чтобы фронт мог отличать «не
// залогинен» от «нет прав»).
//
// Реализация: оборачиваем requireAuth (он положит user в context); внутри
// проверяем role. UserFromContext всегда возвращает ok=true внутри
// этого внутреннего handler'а, потому что requireAuth уже отработал.
func requireAdmin(d AuthDeps) func(http.Handler) http.Handler {
	inner := requireAuth(d)
	return func(next http.Handler) http.Handler {
		return inner(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u, ok := UserFromContext(r.Context())
			if !ok {
				// Не должно случиться (requireAuth выше), но защищаемся.
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
				return
			}
			if string(u.Role) != "admin" {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin only"})
				return
			}
			next.ServeHTTP(w, r)
		}))
	}
}

// requireBasicAuth — middleware для OPDS-роутов. E-reader приложения
// (KOReader / Moon+Reader / FBReader) не поддерживают cookie+CSRF, но
// умеют HTTP Basic — шлют Authorization header каждым запросом.
//
// На каждый запрос проверяем credentials через auth.Service.ValidateCredentials.
// Сессия НЕ создаётся (нам не нужна — credentials всё равно приходят
// каждый раз; создавать тысячи сессий было бы расходом БД ни на что).
//
// При неудаче — 401 с WWW-Authenticate (это триггер диалога логина
// в e-reader'е); тело — короткий plain-text, OPDS-клиент его покажет.
//
// realm — строка в WWW-Authenticate; по соглашению "skriptes OPDS".
func requireBasicAuth(d AuthDeps) func(http.Handler) http.Handler {
	const realm = "skriptes OPDS"
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			email, password, ok := r.BasicAuth()
			if !ok || email == "" || password == "" {
				w.Header().Set("WWW-Authenticate", `Basic realm="`+realm+`", charset="UTF-8"`)
				http.Error(w, "authentication required", http.StatusUnauthorized)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			defer cancel()
			user, err := d.Service.ValidateCredentials(ctx, email, password)
			if err != nil {
				// ValidateCredentials имеет timing-mitigation, мы не различаем
				// "нет такого" и "неверный пароль" в ответе.
				w.Header().Set("WWW-Authenticate", `Basic realm="`+realm+`", charset="UTF-8"`)
				http.Error(w, "invalid credentials", http.StatusUnauthorized)
				return
			}
			// Кладём пользователя в контекст (нейтральный auth-хелпер) — OPDS-хендлеры
			// (пакет opds, без цикла на api) читают его, напр. для учёта приобретения.
			next.ServeHTTP(w, r.WithContext(auth.ContextWithUser(r.Context(), user)))
		})
	}
}

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
