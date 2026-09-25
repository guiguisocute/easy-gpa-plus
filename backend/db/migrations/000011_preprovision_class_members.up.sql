-- 名单成员先于登录账号存在：导入名单时就建立稳定 app_user.id，
-- 注册只给这条身份补充 password_hash。这样未注册学生也能成为计分对象。

ALTER TABLE app_user ALTER COLUMN password_hash DROP NOT NULL;

-- 未注册的成员身份应随一条尚未产生业务引用的名单记录一起删除；
-- 一旦已有提交/扣分等引用，其他外键仍会阻止删除，避免业务数据悬空。
ALTER TABLE app_user DROP CONSTRAINT app_user_class_id_whitelist_id_fkey;
ALTER TABLE app_user
    ADD CONSTRAINT app_user_class_id_whitelist_id_fkey
    FOREIGN KEY (class_id, whitelist_id) REFERENCES whitelist(class_id, id) ON DELETE CASCADE;

CREATE FUNCTION sync_member_identity_from_whitelist()
RETURNS TRIGGER
LANGUAGE plpgsql SECURITY DEFINER
SET search_path = public, pg_temp
SET row_security = off
AS $$
BEGIN
    INSERT INTO app_user (class_id, whitelist_id, sid, name, password_hash, role, status)
    VALUES (
        NEW.class_id,
        NEW.id,
        NEW.sid,
        NEW.name,
        NULL,
        NEW.role,
        CASE WHEN NEW.active THEN 'active' ELSE 'disabled' END
    )
    ON CONFLICT (whitelist_id) DO UPDATE
       SET sid = EXCLUDED.sid,
           name = EXCLUDED.name,
           role = EXCLUDED.role,
           status = EXCLUDED.status,
           updated_at = now()
     WHERE app_user.password_hash IS NULL;
    RETURN NEW;
END
$$;

REVOKE ALL ON FUNCTION sync_member_identity_from_whitelist() FROM PUBLIC;

CREATE TRIGGER whitelist_member_identity
AFTER INSERT OR UPDATE OF sid, name, role, active ON whitelist
FOR EACH ROW EXECUTE FUNCTION sync_member_identity_from_whitelist();

-- 回填历史导入名单。已注册账号通过 whitelist_id 冲突保持原样；
-- 已有的未注册身份则按名单刷新姓名、角色和启停状态。
INSERT INTO app_user (class_id, whitelist_id, sid, name, password_hash, role, status)
SELECT w.class_id,
       w.id,
       w.sid,
       w.name,
       NULL,
       w.role,
       CASE WHEN w.active THEN 'active' ELSE 'disabled' END
  FROM whitelist w
ON CONFLICT (whitelist_id) DO UPDATE
   SET sid = EXCLUDED.sid,
       name = EXCLUDED.name,
       role = EXCLUDED.role,
       status = EXCLUDED.status,
       updated_at = now()
 WHERE app_user.password_hash IS NULL;
