-- name: GetCategories :many
SELECT c.id, c.kind, c.name, COALESCE(p.jumlah, 0) AS usage, c.budget_minor
FROM categories c
LEFT JOIN (
    SELECT t.kind, t.category, count(*) AS jumlah
    FROM transactions t
    WHERE t.family_id = $1
    GROUP BY t.kind, t.category
) p ON p.kind = c.kind AND p.category = c.name
WHERE c.family_id = $1 AND (sqlc.arg(kind)::text = '' OR c.kind = sqlc.arg(kind))
ORDER BY c.kind, c.name;

-- name: GetCategory :one
SELECT id, kind, name FROM categories WHERE id = $1 AND family_id = $2;

-- name: UpdateCategoryBudget :execrows
UPDATE categories SET budget_minor = $1
WHERE family_id = $2 AND id = $3 AND kind = 'expense';

-- name: InsertCategory :exec
INSERT INTO categories (family_id, kind, name) VALUES ($1, $2, $3);

-- name: EnsureCategory :exec
INSERT INTO categories (family_id, kind, name) VALUES ($1, $2, $3)
ON CONFLICT (family_id, kind, name) DO NOTHING;

-- name: RenameCategoryName :exec
UPDATE categories SET name = $1 WHERE id = $2 AND family_id = $3;

-- name: RenameTransactionCategory :exec
UPDATE transactions SET category = sqlc.arg(new_name)
WHERE family_id = sqlc.arg(family_id) AND kind = sqlc.arg(kind)
  AND category = sqlc.arg(old_name);

-- name: CountCategoryTransactions :one
SELECT count(*) FROM transactions
WHERE family_id = $1 AND kind = $2 AND category = $3;

-- name: DeleteCategory :exec
DELETE FROM categories WHERE id = $1 AND family_id = $2;

-- name: GetCategorySpending :many
SELECT t.kind, t.category, w.currency, SUM(t.amount_minor)::bigint AS minor
FROM transactions t
JOIN wallets w ON w.id = t.wallet_id
WHERE t.family_id = sqlc.arg(family_id) AND t.kind IN ('expense', 'income')
  AND t.occurred_on >= sqlc.arg(from_date)::date AND t.occurred_on < sqlc.arg(to_date)::date
  AND t.occurred_on <= sqlc.arg(today)::date AND NOT t.is_adjustment
GROUP BY t.kind, t.category, w.currency;

-- name: GetTransferRates :many
SELECT DISTINCT ON (w.currency, w2.currency)
       w.currency AS from_currency, w2.currency AS to_currency,
       (t.amount_minor - t.admin_fee_minor)::bigint AS from_minor, t.amount_in_minor AS to_minor
FROM transactions t
JOIN wallets w ON w.id = t.wallet_id
JOIN wallets w2 ON w2.id = t.to_wallet_id
WHERE t.family_id = $1 AND t.kind = 'transfer' AND w.currency <> w2.currency
  AND t.amount_minor > t.admin_fee_minor
ORDER BY w.currency, w2.currency, t.occurred_on DESC, t.id DESC;
