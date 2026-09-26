-- name: GetMembers :many
SELECT u.id, u.name, u.created_at, u.disabled_at,
       CASE WHEN u.id = (SELECT min(id) FROM users WHERE family_id = u.family_id)
            THEN true ELSE false END AS kepala,
       (SELECT count(*) FROM transactions t
        WHERE t.family_id = u.family_id AND t.created_by = u.id) AS tx_count
FROM users u WHERE u.family_id = $1 ORDER BY u.id;

-- name: SetMemberActive :execrows
UPDATE users u SET disabled_at = $1
WHERE u.id = $2 AND u.family_id = $3
  AND NOT (u.id = (SELECT min(id) FROM users WHERE family_id = u.family_id));

-- name: LockFamilyByCode :one
SELECT id FROM families
WHERE signup_code = $1 AND signup_code_expires_at > now() FOR UPDATE;

-- name: LinkUserToFamily :execrows
UPDATE users SET family_id = $1, name = $3
WHERE id = $2 AND family_id IS NULL;

-- name: UserLinkedForUpdate :one
SELECT CASE WHEN family_id IS NOT NULL THEN true ELSE false END AS linked
FROM users WHERE id = $1 FOR UPDATE;

-- name: InsertFamily :one
INSERT INTO families (name, signup_code, billing_owner_user_id)
VALUES ($1, $2, $3)
RETURNING id, name, signup_code, signup_code_expires_at, created_at;

-- name: AssignUserFamily :exec
UPDATE users SET family_id = $1 WHERE id = $2;

-- name: SeedFamilyCategories :exec
INSERT INTO categories (family_id, kind, name)
SELECT $1::bigint, 'expense', name
FROM unnest(ARRAY['Belanja', 'Tagihan', 'Transportasi', 'Makanan', 'Kesehatan', 'Lainnya']) AS name
UNION ALL
SELECT $1::bigint, 'income', name
FROM unnest(ARRAY['Gaji', 'Bonus', 'Hadiah', 'Investasi', 'Lainnya']) AS name;

-- name: CountFamilyUsers :one
SELECT count(*) FROM users WHERE family_id = $1;

-- name: GetFamilyByCode :one
SELECT id, name, signup_code FROM families WHERE signup_code = $1;

-- name: GetFamilyByID :one
SELECT id, name, signup_code, signup_code_expires_at FROM families WHERE id = $1;

-- name: ListFamilies :many
SELECT f.id, f.name, f.signup_code, f.signup_code_expires_at, f.created_at,
       (SELECT count(*) FROM users u WHERE u.family_id = f.id) AS members,
       (SELECT count(*) FROM wallets w WHERE w.family_id = f.id) AS wallets,
       (SELECT count(*) FROM transactions t WHERE t.family_id = f.id) AS txs
FROM families f ORDER BY f.id;

-- name: UpdateFamily :execrows
UPDATE families SET name = COALESCE(NULLIF(sqlc.arg(name)::text, ''), name),
       signup_code = COALESCE(NULLIF(sqlc.arg(code)::text, ''), signup_code),
       signup_code_expires_at = CASE WHEN sqlc.arg(code)::text = '' THEN signup_code_expires_at ELSE now() + interval '7 days' END
WHERE id = sqlc.arg(id);

-- name: GetFamilyDetail :one
SELECT id, name, signup_code, signup_code_expires_at, created_at
FROM families WHERE id = $1;
