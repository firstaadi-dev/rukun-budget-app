-- name: GetWallets :many
SELECT w.id, w.name, w.type, COALESCE(w.provider, '') AS provider, w.currency,
       w.initial_balance_minor, COALESCE(w.settlement_day, 0) AS settlement_day,
       COALESCE(w.payment_day, 0) AS payment_day,
       COALESCE(w.credit_limit_minor, 0) AS credit_limit_minor,
       (w.initial_balance_minor
       + COALESCE((SELECT SUM(CASE WHEN t.kind IN ('income', 'debt_in', 'loan_in', 'invest_sell')
                                   THEN t.amount_minor ELSE -t.amount_minor END)
                   FROM transactions t
                   WHERE t.wallet_id = w.id AND t.occurred_on <= sqlc.arg(today)::date), 0)
       + COALESCE((SELECT SUM(t.amount_in_minor)
                   FROM transactions t
                   WHERE t.to_wallet_id = w.id AND t.occurred_on <= sqlc.arg(today)::date), 0))::bigint AS balance_minor,
       COALESCE((SELECT SUM(CASE WHEN t.kind IN ('income', 'debt_in', 'loan_in', 'invest_sell')
                                 THEN -t.amount_minor ELSE t.amount_minor END)
                 FROM transactions t
                 WHERE t.wallet_id = w.id AND t.occurred_on > sqlc.arg(today)::date
                   AND t.series_kind = 'cicil'), 0)::bigint AS cicilan_mendatang_minor
FROM wallets w
WHERE w.family_id = sqlc.arg(family_id)
  AND (sqlc.arg(wallet_id)::bigint = 0 OR w.id = sqlc.arg(wallet_id))
ORDER BY w.type, w.id;

-- name: CreateWallet :one
INSERT INTO wallets (family_id, name, type, provider, currency, initial_balance_minor,
                     settlement_day, payment_day, credit_limit_minor)
VALUES (sqlc.arg(family_id), sqlc.arg(name), sqlc.arg(type), NULLIF(sqlc.arg(provider)::text, ''),
        sqlc.arg(currency), sqlc.arg(initial_balance_minor),
        NULLIF(sqlc.arg(settlement_day)::smallint, 0),
        NULLIF(sqlc.arg(payment_day)::smallint, 0),
        NULLIF(sqlc.arg(credit_limit_minor)::bigint, 0))
RETURNING id;

-- name: UpdateWallet :execrows
UPDATE wallets SET name = sqlc.arg(name), type = sqlc.arg(type),
       provider = NULLIF(sqlc.arg(provider)::text, ''), currency = sqlc.arg(currency),
       initial_balance_minor = sqlc.arg(initial_balance_minor),
       settlement_day = NULLIF(sqlc.arg(settlement_day)::smallint, 0),
       payment_day = NULLIF(sqlc.arg(payment_day)::smallint, 0),
       credit_limit_minor = NULLIF(sqlc.arg(credit_limit_minor)::bigint, 0)
WHERE id = sqlc.arg(id) AND family_id = sqlc.arg(family_id);

-- name: CountWalletTransactions :one
SELECT count(*) FROM transactions
WHERE family_id = $1 AND (wallet_id = $2 OR to_wallet_id = $2);

-- name: DeleteWallet :execrows
DELETE FROM wallets WHERE id = $1 AND family_id = $2;
