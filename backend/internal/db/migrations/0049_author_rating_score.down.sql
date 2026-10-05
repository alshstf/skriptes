ALTER INDEX authors_rating_score_idx RENAME TO authors_max_rating_idx;
ALTER TABLE authors RENAME COLUMN rating_score TO max_rating;
