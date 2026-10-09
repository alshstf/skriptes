-- Годы жизни автора (#465): из Wikidata (P569/P570) статьи, принятой поиском
-- био (политика приёма кандидата уже их проверяет — теперь сохраняем). Нужны
-- правилу года работы (год раньше рождения + 10 — опечатка fb2) и био-таймлайну.
-- QID автора — authors.ext_ids->>'wd_qid'.
ALTER TABLE authors ADD COLUMN born_year SMALLINT, ADD COLUMN died_year SMALLINT;
