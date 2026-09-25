-- Downgrade toward fewer messages; never restore an opt-out or erase history.
UPDATE mail_preference SET settings=jsonb_set(
    jsonb_set(settings,'{categories}',(SELECT jsonb_object_agg(key,
        CASE WHEN value='frequent' THEN 'immediate' ELSE value END)
        FROM jsonb_each_text(settings->'categories'))),
    '{dailyLimit}',to_jsonb(least(coalesce((settings->>'dailyLimit')::integer,3),8))),
    updated_at=now()
WHERE EXISTS (SELECT 1 FROM jsonb_each_text(settings->'categories') WHERE value='frequent')
   OR (settings->>'dailyLimit')::integer>8;
UPDATE mail_notification SET mode='immediate',due_at=greatest(due_at,now()+interval '10 minutes')
WHERE mode='frequent';
ALTER TABLE mail_notification DROP CONSTRAINT mail_notification_mode_check;
ALTER TABLE mail_notification ADD CONSTRAINT mail_notification_mode_check
    CHECK (mode IN ('digest','immediate'));
