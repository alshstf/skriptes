// skriptes-seed создаёт начального admin-пользователя.
// Использование:
//
//	skriptes-seed --email=alice@example.com --display-name="Alice"
//	  # пароль читается из stdin (с подтверждением)
//
// Или с паролем из окружения (для CI / autotest):
//
//	SKRIPTES_SEED_PASSWORD=secret123 skriptes-seed --email=alice@... --no-prompt
//
// DSN берётся из SKRIPTES_DATABASE_URL — той же переменной что использует
// сам бекенд, чтобы было невозможно случайно сидить не в ту базу.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/skriptes/skriptes/backend/internal/auth"
	"github.com/skriptes/skriptes/backend/internal/config"
	"github.com/skriptes/skriptes/backend/internal/db"
	"golang.org/x/term"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		email       = flag.String("email", "", "email пользователя (обязателен)")
		displayName = flag.String("display-name", "", "отображаемое имя; по умолчанию = email до @")
		role        = flag.String("role", "admin", "роль: admin или user")
		noPrompt    = flag.Bool("no-prompt", false, "брать пароль из SKRIPTES_SEED_PASSWORD вместо интерактивного prompt")
	)
	flag.Parse()

	if *email == "" {
		return errors.New("--email is required")
	}
	if *role != string(auth.RoleAdmin) && *role != string(auth.RoleUser) {
		return fmt.Errorf("invalid role %q (use admin or user)", *role)
	}
	if *displayName == "" {
		if at := strings.IndexByte(*email, '@'); at > 0 {
			*displayName = (*email)[:at]
		} else {
			*displayName = *email
		}
	}

	password, err := readPassword(*noPrompt)
	if err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect db: %w", err)
	}
	defer pool.Close()

	svc := auth.New(pool, 0)
	user, err := svc.CreateUser(ctx, *email, *displayName, password, auth.Role(*role))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return fmt.Errorf("user with email %q already exists", *email)
		}
		return fmt.Errorf("create user: %w", err)
	}
	fmt.Printf("created user id=%d email=%s role=%s\n", user.ID, user.Email, user.Role)
	return nil
}

func readPassword(noPrompt bool) (string, error) {
	if noPrompt {
		p := os.Getenv("SKRIPTES_SEED_PASSWORD")
		if p == "" {
			return "", errors.New("--no-prompt set but SKRIPTES_SEED_PASSWORD is empty")
		}
		return p, nil
	}
	// Интерактивный режим: запрашиваем пароль дважды и сравниваем.
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", errors.New("stdin is not a TTY; pass --no-prompt and SKRIPTES_SEED_PASSWORD env")
	}
	fmt.Fprint(os.Stderr, "password: ")
	p1, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	fmt.Fprint(os.Stderr, "confirm:  ")
	p2, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if string(p1) != string(p2) {
		return "", errors.New("passwords do not match")
	}
	if len(p1) < 8 {
		return "", errors.New("password must be at least 8 characters")
	}
	return string(p1), nil
}

// keep bufio referenced in case future helpers need it (silencing unused-import if added).
var _ = bufio.NewReader
