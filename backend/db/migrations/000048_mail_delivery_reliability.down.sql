-- Never delete platform delivery history just to restore the old NOT NULL.
DO $$ BEGIN
    IF EXISTS(SELECT 1 FROM mail_delivery WHERE class_id IS NULL) THEN
        RAISE EXCEPTION 'platform mail history must be preserved; roll forward instead of removing migration 48';
    END IF;
END $$;
DROP FUNCTION ops_mail_delivery_log_v3(TEXT,BIGINT,TIMESTAMPTZ,TIMESTAMPTZ,INTEGER,INTEGER);
DROP FUNCTION ops_finish_mail_delivery(BIGINT,TEXT,TEXT,TEXT,TEXT);
DROP FUNCTION IF EXISTS ops_lookup_mail_delivery(UUID,TEXT);
DROP FUNCTION IF EXISTS ops_reconcile_mail_delivery(BIGINT,TEXT,TEXT);
DROP FUNCTION ops_start_mail_delivery(BIGINT,UUID,TEXT,TEXT);
DROP INDEX mail_delivery_event_recipient;
ALTER TABLE mail_delivery DROP COLUMN finished_at;
ALTER TABLE mail_delivery DROP COLUMN error_code;
ALTER TABLE mail_delivery DROP COLUMN template;
ALTER TABLE mail_delivery ALTER COLUMN class_id SET NOT NULL;
