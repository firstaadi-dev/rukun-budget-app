-- name: GetParties :many
SELECT p.id, p.name, p.note, COALESCE(b.cur, '') AS cur,
       COALESCE(b.hutang, 0)::bigint AS hutang,
       COALESCE(b.piutang, 0)::bigint AS piutang
FROM parties p
LEFT JOIN (
    SELECT t.party_id, COALESCE(w.currency, t.currency) AS cur,
           SUM(CASE t.kind WHEN 'debt_in' THEN t.amount_minor
                           WHEN 'debt_pay' THEN -t.amount_minor ELSE 0 END)::bigint AS hutang,
           SUM(CASE t.kind WHEN 'loan_out' THEN t.amount_minor
                           WHEN 'loan_in' THEN -t.amount_minor ELSE 0 END)::bigint AS piutang
    FROM transactions t
    LEFT JOIN wallets w ON w.id = t.wallet_id
    WHERE t.family_id = $1 AND t.party_id IS NOT NULL
    GROUP BY t.party_id, COALESCE(w.currency, t.currency)
) b ON b.party_id = p.id
WHERE p.family_id = $1
ORDER BY p.name, b.cur;

-- name: GetPartyByName :one
SELECT id, name, note FROM parties
WHERE family_id = sqlc.arg(family_id) AND lower(name) = lower(sqlc.arg(name)::text);

-- name: InsertParty :one
INSERT INTO parties (family_id, name) VALUES ($1, $2) RETURNING id;

-- name: UpdateParty :execrows
UPDATE parties SET name = $1, note = $2 WHERE id = $3 AND family_id = $4;

-- name: CountPartyTransactions :one
SELECT count(*) FROM transactions WHERE family_id = $1 AND party_id = $2;

-- name: DeleteParty :execrows
DELETE FROM parties WHERE id = $1 AND family_id = $2;

-- name: GetCardBalances :one
SELECT (
    (SELECT w.initial_balance_minor FROM wallets w
     WHERE w.id = sqlc.arg(wallet_id) AND w.family_id = sqlc.arg(family_id))
    + COALESCE((SELECT SUM(CASE WHEN t.kind IN ('income', 'debt_in', 'loan_in', 'invest_sell')
                                THEN t.amount_minor ELSE -t.amount_minor END)
                FROM transactions t
                WHERE t.family_id = sqlc.arg(family_id) AND t.wallet_id = sqlc.arg(wallet_id)
                  AND t.occurred_on <= sqlc.arg(settlement)::date), 0)
    + COALESCE((SELECT SUM(t.amount_in_minor) FROM transactions t
                WHERE t.family_id = sqlc.arg(family_id) AND t.to_wallet_id = sqlc.arg(wallet_id)
                  AND t.occurred_on <= sqlc.arg(settlement)::date), 0)
)::bigint AS at_settlement,
(
    COALESCE((SELECT SUM(t.amount_in_minor) FROM transactions t
              WHERE t.family_id = sqlc.arg(family_id) AND t.to_wallet_id = sqlc.arg(wallet_id)
                AND t.occurred_on > sqlc.arg(settlement)::date
                AND t.occurred_on <= sqlc.arg(today)::date), 0)
    + COALESCE((SELECT SUM(t.amount_minor) FROM transactions t
                WHERE t.family_id = sqlc.arg(family_id) AND t.wallet_id = sqlc.arg(wallet_id)
                  AND t.occurred_on > sqlc.arg(settlement)::date
                  AND t.occurred_on <= sqlc.arg(today)::date
                  AND t.kind IN ('income', 'debt_in', 'loan_in', 'invest_sell')), 0)
)::bigint AS credits_since;
