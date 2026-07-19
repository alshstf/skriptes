ALTER TABLE authors DROP COLUMN IF EXISTS events_fetched_at;
DROP INDEX IF EXISTS author_events_author_year;
DROP TABLE IF EXISTS author_events;
