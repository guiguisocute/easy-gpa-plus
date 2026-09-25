DROP TRIGGER IF EXISTS whitelist_member_identity ON whitelist;
DROP FUNCTION IF EXISTS sync_member_identity_from_whitelist();

-- 只删除尚未注册的预建身份。如果它们已经被业务记录引用，删除会被外键拒绝，
-- 迁移也应停止，而不是静默丢掉已发生的扣分或认定。
DELETE FROM app_user u
 USING whitelist w
 WHERE u.whitelist_id = w.id
   AND w.registered_at IS NULL
   AND u.password_hash IS NULL;

ALTER TABLE app_user ALTER COLUMN password_hash SET NOT NULL;

ALTER TABLE app_user DROP CONSTRAINT app_user_class_id_whitelist_id_fkey;
ALTER TABLE app_user
    ADD CONSTRAINT app_user_class_id_whitelist_id_fkey
    FOREIGN KEY (class_id, whitelist_id) REFERENCES whitelist(class_id, id);
