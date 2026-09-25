ALTER TABLE class_timeline
    DROP CONSTRAINT class_timeline_publicity_window_check,
    DROP COLUMN publicity_open_at,
    DROP COLUMN publicity_close_at;
