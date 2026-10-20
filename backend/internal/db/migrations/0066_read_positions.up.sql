-- История позиций чтения (план recsys/01, задача 1.2): append-only журнал доли
-- прочитанного, из него считается личная скорость чтения («время дочитать»).
-- reads хранит только последнее состояние. Пишет history.SavePosition (строка —
-- если доля сдвинулась ≥ 0.01 или прошло ≥ 10 минут), старше 90 дней — не чаще
-- одной строки в день на книгу (history.CompactReadPositions на старте и раз в сутки).
CREATE TABLE read_positions (
    user_id  BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    book_id  BIGINT      NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    fraction REAL        NOT NULL,
    at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, book_id, at)
);
