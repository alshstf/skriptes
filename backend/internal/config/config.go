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
}

func Load() (*Config, error) {
	var c Config
	if err := env.Parse(&c); err != nil {
		return nil, err
	}
	return &c, nil
}
