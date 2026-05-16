// Package email отправляет письма через SMTP с приложениями.
// На данный момент единственный use case — Send-to-Kindle: epub
// файл прикладывается к письму, Amazon Kindle email service
// обрабатывает приложение и доставляет на устройство.
package email

import (
	"errors"
	"fmt"
	"io"
	"log/slog"

	"gopkg.in/gomail.v2"
)

// ErrNotConfigured — SMTP-конфиг не задан (SMTPHost пустой).
// Handler должен вернуть 503 с понятным сообщением.
var ErrNotConfigured = errors.New("smtp not configured")

// Config — SMTP-параметры. Заполняется из internal/config.Config
// и передаётся в New при старте.
type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	From     string // если пусто — используется User
	UseTLS   bool   // true = implicit TLS (порт 465); false = STARTTLS (587)
}

// Sender — обёртка над gomail.v2 SMTP-клиентом.
// Создаётся один раз; каждый Send открывает свежее соединение
// (gomail.v2 не держит persistent connection, что для нас нормально:
// нагрузка низкая — единичные письма по запросу пользователя).
type Sender struct {
	cfg    Config
	dialer *gomail.Dialer
	logger *slog.Logger
}

// New возвращает Sender. Если cfg.Host пустой — возвращается nil
// (graceful disable); все методы это уважают и возвращают ErrNotConfigured.
func New(cfg Config, logger *slog.Logger) *Sender {
	if cfg.Host == "" {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	d := gomail.NewDialer(cfg.Host, cfg.Port, cfg.User, cfg.Password)
	d.SSL = cfg.UseTLS
	return &Sender{cfg: cfg, dialer: d, logger: logger}
}

// Attachment — файл, прикрепляемый к письму.
// Reader должен быть seekable: gomail.v2 копирует данные в buffer
// перед SMTP-передачей.
type Attachment struct {
	Filename string
	Mime     string
	Data     io.Reader
}

// Send отправляет письмо на toAddr с темой subject, plain-text body
// и опциональным attachment. Возвращает не-nil error если SMTP вернул
// что-то кроме успеха.
//
// Для send-to-Kindle Amazon требует чтобы From-адрес был в approved
// senders в пользовательском Manage Your Content And Devices. Subject
// и body могут быть любыми — Amazon смотрит только attachment.
func (s *Sender) Send(toAddr, subject, body string, att *Attachment) error {
	if s == nil {
		return ErrNotConfigured
	}

	m := gomail.NewMessage()
	from := s.cfg.From
	if from == "" {
		from = s.cfg.User
	}
	m.SetHeader("From", from)
	m.SetHeader("To", toAddr)
	m.SetHeader("Subject", subject)
	m.SetBody("text/plain; charset=UTF-8", body)

	if att != nil && att.Data != nil {
		// gomail-API: Attach через path или через Copier.
		m.Attach(att.Filename, gomail.SetCopyFunc(func(w io.Writer) error {
			_, err := io.Copy(w, att.Data)
			return err
		}), gomail.SetHeader(map[string][]string{
			"Content-Type": {att.Mime + `; name="` + att.Filename + `"`},
		}))
	}

	if err := s.dialer.DialAndSend(m); err != nil {
		return fmt.Errorf("smtp dial+send: %w", err)
	}
	s.logger.Info("email sent", "to", toAddr, "subject", subject, "has_attachment", att != nil)
	return nil
}
