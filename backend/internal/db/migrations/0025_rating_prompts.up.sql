-- Отложенные запросы оценки книг (см. PR «rating prompts»). 99.9% чтения — на
-- Kindle, точной синхронизации статуса нет, поэтому «вероятно прочитано»
-- аппроксимируем по моменту приобретения.

-- acquired_at — момент ПЕРВОГО приобретения издания (Send-to-Kindle / web- /
-- OPDS-скачивание). Ставится один раз; через задержку книга становится пригодной
-- к запросу оценки.
ALTER TABLE reads ADD COLUMN acquired_at TIMESTAMPTZ;

-- Per-work скрытия запроса оценки:
--   'never'  — «не буду оценивать»: скрыто, пока нет явного сигнала прочтения;
--   'snooze' — «ещё не прочитал»: скрыто до snoozed_until.
CREATE TABLE book_rating_prompts (
    user_id       BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    work_id       BIGINT      NOT NULL REFERENCES works (id) ON DELETE CASCADE,
    state         TEXT        NOT NULL CHECK (state IN ('never', 'snooze')),
    snoozed_until TIMESTAMPTZ,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, work_id)
);
