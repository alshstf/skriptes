-- Причина скрытия из ленты рекомендаций (план recsys/01, задача 1.3).
-- NULL — скрыто до появления причин (= «не интересно»). «Уже читал» и «потом»
-- не считаются негативом в модели близости (internal/recsys).
ALTER TABLE feed_dismissals ADD COLUMN reason TEXT
    CHECK (reason IN ('not_interested', 'already_read', 'later'));
