-- 0051: состав сборников из оглавления fb2 (#388).
--
-- Сборник — работа с works.kind (collection / anthology / omnibus). Его состав —
-- строки оглавления издания-якоря по порядку; work_id — отдельная работа каталога,
-- если строка с ней совпала (точно по названию среди работ авторов сборника), NULL
-- — рассказа нет в каталоге отдельно. Заполняет фоновый metadata.ContentsScanner;
-- works.contents_scanned_at — оглавление уже разобрано (в том числе пустое).
CREATE TABLE work_contents (
    compilation_work_id BIGINT  NOT NULL REFERENCES works(id) ON DELETE CASCADE,
    position            INT     NOT NULL,
    title               TEXT    NOT NULL,
    work_id             BIGINT  REFERENCES works(id) ON DELETE SET NULL,
    PRIMARY KEY (compilation_work_id, position)
);
-- «Входит в сборники» — по работе.
CREATE INDEX work_contents_work_idx ON work_contents (work_id) WHERE work_id IS NOT NULL;

ALTER TABLE works ADD COLUMN contents_scanned_at TIMESTAMPTZ;
