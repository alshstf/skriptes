package config

import (
	"time"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	HTTPAddr  string `env:"SKRIPTES_HTTP_ADDR" envDefault:":8080"`
	LogLevel  string `env:"SKRIPTES_LOG_LEVEL" envDefault:"info"`
	LogFormat string `env:"SKRIPTES_LOG_FORMAT" envDefault:"json"`
	Version   string `env:"SKRIPTES_VERSION" envDefault:"dev"`

	DatabaseURL     string        `env:"SKRIPTES_DATABASE_URL" envDefault:"postgres://skriptes:skriptes@localhost:5432/skriptes?sslmode=disable"`
	DatabaseTimeout time.Duration `env:"SKRIPTES_DATABASE_TIMEOUT" envDefault:"60s"`

	MeiliURL    string `env:"SKRIPTES_MEILI_URL" envDefault:"http://localhost:7700"`
	MeiliAPIKey string `env:"SKRIPTES_MEILI_API_KEY"`

	BooksRoot string `env:"SKRIPTES_BOOKS_ROOT" envDefault:"/data/books"`
	InpxRoot  string `env:"SKRIPTES_INPX_ROOT"  envDefault:"/data/inpx"`
	CacheRoot string `env:"SKRIPTES_CACHE_ROOT" envDefault:"/cache"`

	// FBCPath — путь к бинарю fb2cng (rupor-github/fb2cng).
	// В production-образе лежит в /usr/local/bin/fbc; пустая строка =
	// искать в $PATH.
	FBCPath string `env:"SKRIPTES_FBC_PATH" envDefault:"fbc"`

	// CoverPrewarm — фоновый прогрев fb2-обложек и аннотаций (local-only)
	// для всей коллекции, чтобы список книг был с обложками, а не с
	// пустыми плейсхолдерами. CoverPrewarmWorkers — параллелизм чтения
	// zip-архивов (выше = быстрее, но больше нагрузка на диск).
	CoverPrewarm        bool `env:"SKRIPTES_COVER_PREWARM" envDefault:"true"`
	CoverPrewarmWorkers int  `env:"SKRIPTES_COVER_PREWARM_WORKERS" envDefault:"4"`

	// Auth / cookie. CookieSecure=false ставится в чистом-HTTP dev;
	// в проде / за TLS должно быть true. AllowedOrigins — список origin'ов,
	// откуда разрешены мутирующие запросы (защита от CSRF).
	CookieSecure   bool     `env:"SKRIPTES_COOKIE_SECURE"  envDefault:"true"`
	CookieDomain   string   `env:"SKRIPTES_COOKIE_DOMAIN"`
	AllowedOrigins []string `env:"SKRIPTES_ALLOWED_ORIGINS" envSeparator:"," envDefault:"https://skriptes.localhost"`

	// SMTP для send-to-Kindle. Если SMTPHost пустой — функция
	// отключена (handler вернёт 503), и фронт скроет кнопку.
	// Для Gmail: smtp.gmail.com:587 + app-password (не основной).
	// Для Yandex: smtp.yandex.ru:465 + SMTPUseTLS=true.
	SMTPHost     string `env:"SKRIPTES_SMTP_HOST"`
	SMTPPort     int    `env:"SKRIPTES_SMTP_PORT"      envDefault:"587"`
	SMTPUser     string `env:"SKRIPTES_SMTP_USER"`
	SMTPPassword string `env:"SKRIPTES_SMTP_PASSWORD"`
	SMTPFrom     string `env:"SKRIPTES_SMTP_FROM"` // From-адрес; если пусто — берём SMTPUser
	// SMTPUseTLS=true → implicit TLS (порт 465); false → STARTTLS (587).
	SMTPUseTLS bool `env:"SKRIPTES_SMTP_USE_TLS" envDefault:"false"`
}

func Load() (*Config, error) {
	var c Config
	if err := env.Parse(&c); err != nil {
		return nil, err
	}
	return &c, nil
}
