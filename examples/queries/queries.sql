-- name: InsertUser :execlastid
-- liteorm:arg name string
-- liteorm:arg email string
-- liteorm:arg active int
-- liteorm:arg lastSeen string
INSERT INTO users (name, email, active, last_seen) VALUES (?, ?, ?, ?);

-- name: GetUser :one
-- liteorm:result User
-- liteorm:arg id int64
SELECT id, name, email, active, last_seen FROM users WHERE id = ?;

-- name: ListActive :many
-- liteorm:result User
-- liteorm:arg active bool
SELECT id, name, email, active, last_seen FROM users WHERE active = ? ORDER BY name;

-- name: CountUsers :one
-- liteorm:result int64
SELECT count(*) FROM users;

-- name: Deactivate :exec
-- liteorm:arg id int64
UPDATE users SET active = 0 WHERE id = ?;

-- name: PurgeStale :execrows
-- liteorm:arg before string
DELETE FROM users WHERE last_seen < ?;
