-- version.info из INPX (парсится inpx.Open→i.Version) — чтобы в UI видеть, какая
-- версия коллекции сейчас импортирована (обновилась ли после нового INPX).
ALTER TABLE collections ADD COLUMN inpx_version TEXT;
