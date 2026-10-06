-- Пароли устройств (#389, B2): вход читалок по OPDS и синхронизации без
-- основного пароля. Создаются в профиле (пароль показывается один раз),
-- отзываются по одному. Хранится только проверочное значение
-- hex(SHA-256(hex(MD5(пароль)))): MD5 — потому что KOReader-синхронизация
-- присылает его вместо пароля; пароль — 80 бит случайности, словарём не
-- подбирается, соль не нужна.
CREATE TABLE device_passwords (
    id           BIGSERIAL   PRIMARY KEY,
    user_id      BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT        NOT NULL,
    verifier     TEXT        NOT NULL UNIQUE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ
);

CREATE INDEX device_passwords_user_idx ON device_passwords (user_id);
