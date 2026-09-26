-- name: GetTransactions :many
SELECT t.id, t.kind, t.occurred_on,
       COALESCE(t.wallet_id, 0) AS wallet_id, COALESCE(w.name, '') AS wallet_name,
       COALESCE(w.currency, t.currency, '') AS wallet_cur,
       t.amount_minor, COALESCE(t.category, '') AS category,
       COALESCE(t.to_wallet_id, 0) AS to_wallet_id, COALESCE(w2.name, '') AS to_wallet_name,
       COALESCE(w2.currency, '') AS to_wallet_cur,
       COALESCE(t.amount_in_minor, 0) AS amount_in_minor, t.admin_fee_minor,
       COALESCE(t.party_id, 0) AS party_id, COALESCE(p.name, '') AS party_name,
       COALESCE(t.investment_id, 0) AS investment_id,
       COALESCE(iv.name, '') AS investment_name,
       COALESCE(iv.varian, '') AS investment_variant,
       COALESCE(t.qty_e8, 0) AS qty_e8, COALESCE(t.cost_basis_minor, 0) AS cost_basis_minor,
       COALESCE(t.series_id, 0) AS series_id, COALESCE(t.series_seq, 0) AS series_seq,
       COALESCE(t.series_n, 0) AS series_n, COALESCE(t.series_kind, '') AS series_kind,
       t.note, COALESCE(u.name, '') AS created_by, t.created_at, t.is_adjustment
FROM transactions t
LEFT JOIN wallets w ON w.id = t.wallet_id
LEFT JOIN wallets w2 ON w2.id = t.to_wallet_id
LEFT JOIN parties p ON p.id = t.party_id
LEFT JOIN investments iv ON iv.id = t.investment_id
LEFT JOIN users u ON u.id = t.created_by
WHERE t.family_id = sqlc.arg(family_id)
  AND (sqlc.arg(transaction_id)::bigint = 0 OR t.id = sqlc.arg(transaction_id))
  AND (sqlc.arg(party_id)::bigint = 0 OR t.party_id = sqlc.arg(party_id))
  AND (cardinality(sqlc.arg(kinds)::text[]) = 0 OR t.kind = ANY(sqlc.arg(kinds)::text[]))
  AND (sqlc.arg(category)::text = '' OR t.category = sqlc.arg(category)::text)
  AND (sqlc.narg(from_date)::date IS NULL OR t.occurred_on >= sqlc.narg(from_date)::date)
  AND (sqlc.narg(to_date)::date IS NULL OR t.occurred_on < sqlc.narg(to_date)::date)
  AND (sqlc.arg(search)::text = '' OR t.note ILIKE '%' || sqlc.arg(search)::text || '%'
      OR t.category ILIKE '%' || sqlc.arg(search)::text || '%'
      OR p.name ILIKE '%' || sqlc.arg(search)::text || '%'
      OR w.name ILIKE '%' || sqlc.arg(search)::text || '%'
      OR iv.name ILIKE '%' || sqlc.arg(search)::text || '%')
  AND (sqlc.arg(wallet_id)::bigint = 0 OR t.wallet_id = sqlc.arg(wallet_id)
       OR t.to_wallet_id = sqlc.arg(wallet_id))
  AND (sqlc.arg(investment_id)::bigint = 0 OR t.investment_id = sqlc.arg(investment_id))
  AND (sqlc.narg(before_date)::date IS NULL
       OR (t.occurred_on, t.id) < (sqlc.narg(before_date)::date, sqlc.arg(before_id)::bigint))
ORDER BY t.occurred_on DESC, t.id DESC
LIMIT NULLIF(sqlc.arg(limit_count)::integer, 0);

-- name: InsertTransaction :one
INSERT INTO transactions
  (family_id, kind, occurred_on, wallet_id, currency, amount_minor, category,
   to_wallet_id, amount_in_minor, admin_fee_minor, party_id,
   investment_id, qty_e8, cost_basis_minor, series_id, series_seq, series_n, series_kind,
   note, created_by, is_adjustment)
VALUES (sqlc.arg(family_id), sqlc.arg(kind), sqlc.arg(occurred_on)::date,
        NULLIF(sqlc.arg(wallet_id)::bigint, 0), NULLIF(sqlc.arg(currency)::text, '')::char(3),
        sqlc.arg(amount_minor), NULLIF(sqlc.arg(category)::text, ''),
        NULLIF(sqlc.arg(to_wallet_id)::bigint, 0), NULLIF(sqlc.arg(amount_in_minor)::bigint, 0),
        sqlc.arg(admin_fee_minor), NULLIF(sqlc.arg(party_id)::bigint, 0),
        NULLIF(sqlc.arg(investment_id)::bigint, 0), NULLIF(sqlc.arg(qty_e8)::bigint, 0),
        sqlc.narg(cost_basis_minor)::bigint,
        NULLIF(sqlc.arg(series_id)::bigint, 0), NULLIF(sqlc.arg(series_seq)::smallint, 0),
        NULLIF(sqlc.arg(series_n)::smallint, 0), NULLIF(sqlc.arg(series_kind)::text, ''),
        sqlc.arg(note), sqlc.arg(created_by), sqlc.arg(is_adjustment))
RETURNING id;

-- name: UpdateTransaction :execrows
UPDATE transactions SET occurred_on = sqlc.arg(occurred_on)::date,
       wallet_id = NULLIF(sqlc.arg(wallet_id)::bigint, 0),
       currency = NULLIF(sqlc.arg(currency)::text, '')::char(3),
       amount_minor = sqlc.arg(amount_minor), category = NULLIF(sqlc.arg(category)::text, ''),
       to_wallet_id = NULLIF(sqlc.arg(to_wallet_id)::bigint, 0),
       amount_in_minor = NULLIF(sqlc.arg(amount_in_minor)::bigint, 0),
       admin_fee_minor = sqlc.arg(admin_fee_minor),
       party_id = NULLIF(sqlc.arg(party_id)::bigint, 0), note = sqlc.arg(note)
WHERE id = sqlc.arg(id) AND kind = sqlc.arg(kind) AND family_id = sqlc.arg(family_id);

-- name: NextTransactionSeriesID :one
SELECT nextval('tx_series_seq');

-- name: UpdateTransactionSeries :execrows
UPDATE transactions SET category = NULLIF(sqlc.arg(category)::text, ''), note = sqlc.arg(note)
WHERE family_id = sqlc.arg(family_id) AND series_id = sqlc.arg(series_id);

-- name: DeleteTransactionSeries :execrows
DELETE FROM transactions WHERE family_id = $1 AND series_id = $2;

-- name: DeleteTransaction :execrows
DELETE FROM transactions WHERE id = $1 AND family_id = $2;
