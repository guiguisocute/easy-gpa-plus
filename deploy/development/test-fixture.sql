-- A fresh synthetic tenant for legacy integration tests that need one student
-- and one scheme. Its lifetime is the disposable E2E stack, never easygpa-plus-dev.
BEGIN;
INSERT INTO class(name,archived) VALUES ('Go integration fixture',false)
RETURNING id AS fixture_class_id \gset
INSERT INTO whitelist(class_id,sid,name,role)
VALUES (:fixture_class_id,'GO-FIXTURE-'||:fixture_class_id::bigint::text,'Synthetic student','student');
INSERT INTO scheme(class_id,name,version,status,config,created_by)
SELECT :fixture_class_id,'Go fixture',1,'published','{}',id
FROM app_user WHERE class_id=:fixture_class_id ORDER BY id LIMIT 1;
COMMIT;
SELECT :fixture_class_id;
