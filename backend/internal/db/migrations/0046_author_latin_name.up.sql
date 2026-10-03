-- 0046: латинское имя автора (#290/#291) — то, за которое голосует больше
-- половины его переводов с латинским src-автором из fb2 (кроме сборников), в
-- нижнем регистре: «doyle arthur conan» у «Дойль Артур Конан». По нему запрос
-- «doyle» узнаёт автора (выдача «сначала его книги»), подсказки и поиск /authors
-- находят его латиницей. Пересчитывает catalog.RecomputeAuthorStats.
ALTER TABLE authors ADD COLUMN latin_name TEXT;
CREATE INDEX authors_latin_name_trgm ON authors USING gin (latin_name gin_trgm_ops) WHERE latin_name IS NOT NULL;
