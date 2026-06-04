package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/jackc/pgx/v5"
)

const enrichmentGatesKey = "enrichment_gates"

// EnrichmentGates — «выключатели» ленивого (on-view) обогащения по типам
// данных. Каждый флаг, будучи true, ПОДАВЛЯЕТ инициацию нового lazy-обогащения
// соответствующего типа при открытии карточки (book / author / adaptations).
//
// Это отдельная ось от фоновых воркеров и от локального прогрева: те решают
// «наполнять ли заранее», а эти — «дёргать ли внешние API по запросу
// пользователя вообще». UI-режим «Выкл» = соответствующий флаг true + оба фона
// этого типа off.
//
// Семантика намеренно узкая: «Выкл» НЕ стирает уже сохранённые данные и НЕ
// форсит ре-фетч при обратном включении — он лишь перестаёт ИНИЦИИРОВАТЬ новые
// lazy-запросы. Уже закэшированные обложки/био/постеры продолжают отдаваться.
//
// Год сюда НЕ входит: у него нет lazy-пути (наполняется только локальным
// проходом / внешним воркером), поэтому «выключать lazy» нечего.
//
// Дефолт — всё false = поведение «как раньше» (lazy работает для всех типов).
type EnrichmentGates struct {
	CoverDisabled      bool `json:"cover_disabled"`
	AnnotationDisabled bool `json:"annotation_disabled"`
	AuthorDisabled     bool `json:"author_disabled"`
	AdaptationDisabled bool `json:"adaptation_disabled"`
}

// DefaultEnrichmentGates — ничего не выключено (lazy включён для всех типов).
func DefaultEnrichmentGates() EnrichmentGates {
	return EnrichmentGates{}
}

// EnrichmentGates читает глобальные «выключатели» lazy-обогащения. Нет
// оверрайда в БД → дефолт (всё включено). Поля, отсутствующие в JSON,
// остаются дефолтными (мердж поверх DefaultEnrichmentGates).
func (s *Store) EnrichmentGates(ctx context.Context) (EnrichmentGates, error) {
	cfg := DefaultEnrichmentGates()
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT value FROM app_settings WHERE key = $1`, enrichmentGatesKey).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read enrichment gates: %w", err)
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return DefaultEnrichmentGates(), fmt.Errorf("decode enrichment gates: %w", err)
	}
	return cfg, nil
}

// SetEnrichmentGates сохраняет глобальные «выключатели» lazy-обогащения (upsert).
func (s *Store) SetEnrichmentGates(ctx context.Context, cfg EnrichmentGates) error {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode enrichment gates: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO app_settings (key, value, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
	`, enrichmentGatesKey, raw)
	if err != nil {
		return fmt.Errorf("save enrichment gates: %w", err)
	}
	return nil
}

// EnrichmentGateResolver — горячий доступ к «выключателям» lazy-обогащения.
//
// Зеркало ContentResolver: глобальный конфиг кэшируется в памяти (atomic), он
// читается на каждый GET карточки книги/автора/экранизаций (горячий путь),
// держать его в БД-чтении дорого. Кэш обновляется на старте (Load) и при
// сохранении из админки (SetGates) — живо, без рестарта.
type EnrichmentGateResolver struct {
	store *Store
	gates atomic.Pointer[EnrichmentGates]
}

func NewEnrichmentGateResolver(store *Store) *EnrichmentGateResolver {
	r := &EnrichmentGateResolver{store: store}
	def := DefaultEnrichmentGates()
	r.gates.Store(&def)
	return r
}

// Load загружает конфиг из БД в кэш. Вызывается на старте; при ошибке кэш
// остаётся дефолтным (всё включено).
func (r *EnrichmentGateResolver) Load(ctx context.Context) error {
	cfg, err := r.store.EnrichmentGates(ctx)
	if err != nil {
		return err
	}
	r.gates.Store(&cfg)
	return nil
}

// Gates возвращает закэшированный конфиг. nil-resolver безопасен (вызывается
// как метод указателя из триггеров): nil → дефолт (ничего не выключено).
func (r *EnrichmentGateResolver) Gates() EnrichmentGates {
	if r == nil {
		return DefaultEnrichmentGates()
	}
	if p := r.gates.Load(); p != nil {
		return *p
	}
	return DefaultEnrichmentGates()
}

// SetGates персистит конфиг и обновляет кэш.
func (r *EnrichmentGateResolver) SetGates(ctx context.Context, cfg EnrichmentGates) error {
	if err := r.store.SetEnrichmentGates(ctx, cfg); err != nil {
		return err
	}
	r.gates.Store(&cfg)
	return nil
}
