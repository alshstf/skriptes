-- user_settings — персональные рантайм-настройки пользователя. Тот же
-- generic key/value JSONB паттерн, что и app_settings, но с привязкой к
-- user_id (составной PK user_id+key). Дефолты живут в коде; здесь — только
-- персональные оверрайды.
--
-- Первый потребитель — раздел «Контент» в профиле (key 'content'):
-- персонально скрытые жанры/языки (убирают книги из выдачи только для
-- этого пользователя, не переопределяя глобальные настройки админа).
--
-- ON DELETE CASCADE: удаление пользователя уносит его настройки.
CREATE TABLE user_settings (
    user_id    BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key        TEXT        NOT NULL,
    value      JSONB       NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, key)
);
