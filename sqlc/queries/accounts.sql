-- name: CreateFirebaseUser :one
INSERT INTO users (name, email, firebase_uid)
VALUES ($1, $2, $3)
RETURNING id;

-- name: MarkFirebaseEmailVerified :exec
UPDATE users
SET email_verified_at = COALESCE(email_verified_at, now())
WHERE firebase_uid = $1;

-- name: FirebaseUser :one
SELECT u.id, u.name, u.email, u.firebase_uid, u.email_verified_at,
       COALESCE(u.family_id, 0) AS family_id, COALESCE(f.name, '') AS family_name,
       CASE WHEN u.id = (SELECT min(id) FROM users WHERE family_id = u.family_id)
            THEN true ELSE false END AS kepala,
       u.disabled_at
FROM users u LEFT JOIN families f ON f.id = u.family_id
WHERE u.firebase_uid = $1;

-- name: SessionUser :one
SELECT u.id, u.name, u.email, u.firebase_uid, u.email_verified_at,
       COALESCE(u.family_id, 0) AS family_id, COALESCE(f.name, '') AS family_name,
       CASE WHEN u.id = (SELECT min(id) FROM users WHERE family_id = u.family_id)
            THEN true ELSE false END AS kepala
FROM sessions s
JOIN users u ON u.id = s.user_id
LEFT JOIN families f ON f.id = u.family_id
WHERE s.token = $1 AND s.expires_at > now() AND u.disabled_at IS NULL
  AND u.firebase_uid IS NOT NULL AND u.email_verified_at IS NOT NULL;

-- name: DeleteUserSessions :exec
DELETE FROM sessions s USING users u
WHERE s.user_id = u.id AND u.id = $1 AND u.family_id = $2;

-- name: CreateSession :exec
INSERT INTO sessions (token, user_id, expires_at)
VALUES ($1, $2, $3);

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token = $1;

-- name: PurgeSessions :exec
DELETE FROM sessions WHERE expires_at < now();
