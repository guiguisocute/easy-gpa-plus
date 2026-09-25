-- 回退到支持 GPA 开关的旧版本时保持开放，截止沿用该班封存截止。
-- 已移除的旧开关和单独截止不再恢复。
UPDATE class_timeline
   SET capabilities = capabilities || jsonb_build_object(
           'gpa', jsonb_build_object('on', true, 'close', close_at)
       ), updated_at = now()
 WHERE NOT (capabilities ? 'gpa');
