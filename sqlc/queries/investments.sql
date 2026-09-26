-- name: GetInvestments :many
SELECT i.id, i.kind, i.name, i.varian, i.symbol, i.currency,
       COALESCE(i.wallet_id, 0) AS wallet_id, COALESCE(w.name, '') AS wallet_name,
       COALESCE(i.manual_price_e4, 0) AS manual_price_e4, i.manual_price_on,
       i.note, i.created_at,
       COALESCE((SELECT SUM(CASE WHEN t.kind = 'invest_buy' THEN t.qty_e8 ELSE -t.qty_e8 END)
                 FROM transactions t WHERE t.investment_id = i.id), 0)::bigint AS qty_e8,
       COALESCE((SELECT SUM(CASE WHEN t.kind = 'invest_buy' THEN t.amount_minor ELSE -t.cost_basis_minor END)
                 FROM transactions t WHERE t.investment_id = i.id), 0)::bigint AS modal_minor,
       COALESCE((SELECT SUM(t.admin_fee_minor) FROM transactions t
                 WHERE t.investment_id = i.id AND t.kind = 'invest_buy'), 0)::bigint AS fee_minor,
       (SELECT count(*) FROM transactions t WHERE t.investment_id = i.id AND t.kind = 'invest_buy') AS lots,
       (SELECT t.occurred_on FROM transactions t
        WHERE t.investment_id = i.id AND t.kind = 'invest_buy'
        ORDER BY t.occurred_on DESC LIMIT 1) AS last_buy,
       (SELECT count(*) FROM transactions t WHERE t.investment_id = i.id AND t.kind = 'invest_sell') AS sales,
       COALESCE((SELECT SUM(t.amount_minor - t.cost_basis_minor) FROM transactions t
                 WHERE t.investment_id = i.id AND t.kind = 'invest_sell'), 0)::bigint AS realized_minor
FROM investments i
LEFT JOIN wallets w ON w.id = i.wallet_id
WHERE i.family_id = sqlc.arg(family_id)
  AND (sqlc.arg(investment_id)::bigint = 0 OR i.id = sqlc.arg(investment_id))
ORDER BY CASE i.kind WHEN 'gold' THEN 1 WHEN 'stock' THEN 2 ELSE 3 END, i.name, i.varian;

-- name: CreateInvestment :one
INSERT INTO investments
  (family_id, kind, name, varian, symbol, currency, wallet_id,
   manual_price_e4, manual_price_on, note)
VALUES (sqlc.arg(family_id), sqlc.arg(kind), sqlc.arg(name), sqlc.arg(varian),
        sqlc.arg(symbol), sqlc.arg(currency), NULLIF(sqlc.arg(wallet_id)::bigint, 0),
        NULLIF(sqlc.arg(manual_price_e4)::bigint, 0), sqlc.narg(manual_price_on)::date,
        sqlc.arg(note))
RETURNING id;

-- name: UpdateInvestment :execrows
UPDATE investments SET name = sqlc.arg(name), varian = sqlc.arg(varian),
       symbol = sqlc.arg(symbol), currency = sqlc.arg(currency),
       wallet_id = NULLIF(sqlc.arg(wallet_id)::bigint, 0),
       manual_price_e4 = NULLIF(sqlc.arg(manual_price_e4)::bigint, 0),
       manual_price_on = sqlc.narg(manual_price_on)::date, note = sqlc.arg(note)
WHERE family_id = sqlc.arg(family_id) AND id = sqlc.arg(id);

-- name: DeleteInvestment :execrows
DELETE FROM investments i
WHERE i.family_id = $1 AND i.id = $2
  AND NOT EXISTS (SELECT 1 FROM transactions t WHERE t.investment_id = i.id);

-- name: LockInvestment :one
SELECT id FROM investments WHERE family_id = $1 AND id = $2 FOR UPDATE;

-- name: LastInvestmentSaleDate :one
SELECT occurred_on FROM transactions
WHERE family_id = $1 AND investment_id = $2 AND kind = 'invest_sell'
ORDER BY occurred_on DESC LIMIT 1;

-- name: InvestmentTradePosition :one
SELECT COALESCE(SUM(CASE WHEN t.kind = 'invest_buy' THEN t.qty_e8 ELSE -t.qty_e8 END), 0)::bigint AS qty_e8,
       COALESCE(SUM(CASE WHEN t.kind = 'invest_buy' THEN t.amount_minor ELSE -t.cost_basis_minor END), 0)::bigint AS cost_minor,
       (SELECT latest.occurred_on FROM transactions latest
        WHERE latest.family_id = $1 AND latest.investment_id = $2
        ORDER BY latest.occurred_on DESC LIMIT 1) AS last_trade
FROM transactions t WHERE t.family_id = $1 AND t.investment_id = $2;

-- name: CountLaterInvestmentSales :one
SELECT count(*) FROM transactions
WHERE family_id = $1 AND investment_id = $2 AND kind = 'invest_sell' AND id > $3;

-- name: DeleteInvestmentTrade :execrows
DELETE FROM transactions
WHERE family_id = $1 AND investment_id = $2 AND id = $3
  AND kind IN ('invest_buy', 'invest_sell');
