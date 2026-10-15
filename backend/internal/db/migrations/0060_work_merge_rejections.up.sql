-- «Не объединять» (#517): админ отклонил подсказку слияния работ на карточке
-- серии/автора — пара работ запоминается, подсказка больше не показывается, а
-- фоновая группировка (metadata/work_grouper.go) такие пары не склеивает.
-- Пара упорядочена (work_a < work_b); работа исчезла (слита, GC) — пара уходит
-- каскадом.
CREATE TABLE work_merge_rejections (
    work_a      BIGINT NOT NULL REFERENCES works(id) ON DELETE CASCADE,
    work_b      BIGINT NOT NULL REFERENCES works(id) ON DELETE CASCADE,
    rejected_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    rejected_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (work_a, work_b),
    CHECK (work_a < work_b)
);
CREATE INDEX work_merge_rejections_b ON work_merge_rejections (work_b);
