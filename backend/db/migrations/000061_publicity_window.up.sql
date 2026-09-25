ALTER TABLE class_timeline
    ADD COLUMN publicity_open_at TIMESTAMPTZ,
    ADD COLUMN publicity_close_at TIMESTAMPTZ,
    ADD CONSTRAINT class_timeline_publicity_window_check CHECK (
        (publicity_open_at IS NULL AND publicity_close_at IS NULL) OR
        (publicity_open_at IS NOT NULL AND publicity_close_at IS NOT NULL AND publicity_open_at < publicity_close_at)
    );
