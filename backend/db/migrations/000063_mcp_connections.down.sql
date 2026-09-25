DROP TRIGGER IF EXISTS revoke_class_agent_connections ON class;
DROP TRIGGER IF EXISTS revoke_user_agent_connections ON app_user;
DROP FUNCTION IF EXISTS revoke_changed_agent_connections();
DROP FUNCTION IF EXISTS mcp_authenticate(BYTEA);
ALTER TABLE export_job DROP COLUMN agent_connection_id;
DROP TABLE agent_upload;
DROP TABLE agent_operation;
DROP TABLE agent_connection;
