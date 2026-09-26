-- name: GetQuotes :many
SELECT symbol, price_e4, currency, quoted_at, name, fetched_at
FROM quotes
WHERE cardinality(sqlc.arg(symbols)::text[]) = 0
   OR symbol = ANY(sqlc.arg(symbols)::text[]);

-- name: SaveQuote :batchexec
INSERT INTO quotes (symbol, price_e4, currency, quoted_at, name, fetched_at)
VALUES ($1, $2, $3, $4, $5, now())
ON CONFLICT (symbol) DO UPDATE SET
  price_e4 = EXCLUDED.price_e4, currency = EXCLUDED.currency,
  quoted_at = EXCLUDED.quoted_at, fetched_at = EXCLUDED.fetched_at,
  name = COALESCE(NULLIF(EXCLUDED.name, ''), quotes.name);

-- name: GetUsedSymbols :many
SELECT DISTINCT symbol, kind FROM investments WHERE symbol <> '';
