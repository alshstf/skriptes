-- metadata_overrides — локальные ручные правки метаданных каталога (только админ,
-- глобально). Значение материализуется в реальную колонку (books.* / works.* /
-- series.*), чтобы попадать в поиск/фильтры/фасеты Meili; здесь хранится
-- original_value (для отката) + сам ФАКТ оверрайда (для индикатора «изменено» и
-- гейтов recompute/ре-импорта, чтобы правка переживала группировку и импорт).
--
--   target_kind 'book'   → target_id = books.id  (edition-уровень)
--               'work'   → target_id = works.id  (work-уровень, dual-write в якорь)
--               'series' → target_id = series.id (отложено: переименование серии)
--   field — имя поля: title|lang|edition_year|written_year|ser_no|series|isbn|
--           publisher|translator|edition_title|genres|authors
--   override_value / original_value — JSONB. Скаляр = обёртка {"v": …} (NULL
--           представим); составные (title/series/genres/authors) — объект, см.
--           metadata/overrides.go. original_value захватывается ДО первой
--           материализации; повторная правка его НЕ перезахватывает (откат
--           всегда к истинному оригиналу).
CREATE TABLE metadata_overrides (
    target_kind    TEXT        NOT NULL CHECK (target_kind IN ('book', 'work', 'series')),
    target_id      BIGINT      NOT NULL,
    field          TEXT        NOT NULL,
    override_value JSONB       NOT NULL,
    original_value JSONB       NOT NULL,
    set_by         BIGINT      REFERENCES users(id) ON DELETE SET NULL,
    set_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (target_kind, target_id, field)
);

-- Гейты recompute/ре-импорта и список правок книги пробуют по (kind, target_id).
CREATE INDEX idx_metadata_overrides_lookup ON metadata_overrides (target_kind, target_id);
