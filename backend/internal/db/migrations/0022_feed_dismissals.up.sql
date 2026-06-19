-- feed_dismissals — пользователь скрыл работу из ленты «Новинки по подпискам»
-- («не интересно, не показывай вечно»). Скрытие по РАБОТЕ (лента схлопнута по
-- work_id), чтобы книга не вернулась через другое издание. ON DELETE CASCADE:
-- удаление юзера или GC работы (после merge) убирает запись.
CREATE TABLE feed_dismissals (
    user_id      BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    work_id      BIGINT      NOT NULL REFERENCES works(id) ON DELETE CASCADE,
    dismissed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, work_id)
);
