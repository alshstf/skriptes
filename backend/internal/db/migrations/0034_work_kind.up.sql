-- Тип работы: сборники/антологии/тома собраний сочинений отделяются от обычных
-- произведений (карточка автора, фильтры). NULL = обычное произведение.
--   kind:        'collection' (авторский сборник) | 'anthology' (многоавторная
--                антология/подшивка) | 'omnibus' (том собрания сочинений/избранного)
--   kind_source: 'heuristic' | 'fantlab' | 'override' — происхождение метки,
--                приоритет override > fantlab > heuristic (зеркало written_year_source).
ALTER TABLE works ADD COLUMN kind TEXT;
ALTER TABLE works ADD COLUMN kind_source TEXT;

CREATE INDEX idx_works_kind ON works (kind) WHERE kind IS NOT NULL;
