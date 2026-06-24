package metadata

import (
	"net/http"
	"time"
)

// enricherUserAgent — осмысленный User-Agent для внешних книжных API (OpenLibrary,
// Google Books). По умолчанию Go шлёт UA вида "Go-http-client/1.1"; OpenLibrary
// (как и Wikimedia/Wikidata, см. wikiUserAgent/wdUserAgent) хуже относится к
// анонимным клиентам — троттлит/таймаутит/блочит. Формат — имя/версия + контакт,
// по аналогии с https://meta.wikimedia.org/wiki/User-Agent_policy.
const enricherUserAgent = "skriptes/0.1 (https://github.com/alshstf/skriptes; metadata-enricher)"

// uaRoundTripper проставляет User-Agent на КАЖДЫЙ исходящий запрос клиента (если
// заголовок ещё не задан явно). Так UA попадает во все вызовы OL/GB без правки
// каждой точки запроса (их много) и без риска перетереть явно выставленный UA.
type uaRoundTripper struct {
	ua   string
	base http.RoundTripper
}

func (t *uaRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Header.Get("User-Agent") == "" {
		// Клонируем — мутировать переданный request нельзя (RoundTripper-контракт).
		r = r.Clone(r.Context())
		r.Header.Set("User-Agent", t.ua)
	}
	return t.base.RoundTrip(r)
}

// NewEnricherHTTPClient — http.Client с заданным таймаутом и осмысленным
// User-Agent (enricherUserAgent) для внешних источников. Используется в main
// wiring для OpenLibrary/Google Books.
func NewEnricherHTTPClient(timeout time.Duration) *http.Client {
	base := http.DefaultTransport
	return &http.Client{
		Timeout:   timeout,
		Transport: &uaRoundTripper{ua: enricherUserAgent, base: base},
	}
}
