-- name: CreateClass :one
INSERT INTO class (name) VALUES ($1) RETURNING *;

-- name: ListClasses :many
SELECT * FROM class WHERE archived = FALSE ORDER BY id;
