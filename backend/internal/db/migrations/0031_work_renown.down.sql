DROP TABLE work_renown_lookups;

ALTER TABLE works
    DROP COLUMN fantlab_marks,
    DROP COLUMN ol_ratings_count,
    DROP COLUMN ol_want_count;
