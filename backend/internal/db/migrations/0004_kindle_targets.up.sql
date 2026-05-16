-- Send-to-Kindle: пользователь может зарегистрировать НЕСКОЛЬКО
-- адресатов (свой Kindle, Kindle жены, второй планшет и т.д.) с
-- человекочитаемыми лейблами. При отправке выбирает целевой.
--
-- Старое поле users.kindle_email оставляем deprecated на grace
-- period: данные из него мигрируем в новую таблицу с лейблом по
-- умолчанию "Мой Kindle". Дроп поля будет отдельной миграцией позже.

CREATE TABLE kindle_targets (
    id         BIGSERIAL PRIMARY KEY,
    user_id    BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    label      TEXT        NOT NULL,
    email      CITEXT      NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, email)
);
CREATE INDEX kindle_targets_user_id ON kindle_targets(user_id);

-- Грейсфул миграция существующих kindle_email (если у кого-то уже
-- было настроено). Лейбл "Мой Kindle" — нейтральный.
INSERT INTO kindle_targets (user_id, label, email)
SELECT id, 'Мой Kindle', kindle_email
FROM users
WHERE kindle_email IS NOT NULL AND kindle_email != '';
