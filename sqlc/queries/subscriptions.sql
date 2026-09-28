-- name: ListSubscriptions :many
SELECT s.id, s.name, s.wallet_id, w.name AS wallet_name, w.currency,
       s.category, s.amount_minor, s.next_date, s.interval_months, s.active
FROM subscriptions s JOIN wallets w ON w.id = s.wallet_id
WHERE s.family_id = $1 AND (NOT sqlc.arg(active_only)::boolean OR s.active)
ORDER BY s.active DESC, s.next_date, s.name;

-- name: CreateSubscription :one
INSERT INTO subscriptions (family_id, wallet_id, name, category, amount_minor, next_date, billing_day, interval_months)
VALUES ($1,$2,$3,$4,$5,$6,EXTRACT(DAY FROM $6::date)::smallint,$7) RETURNING id;

-- name: UpdateSubscription :execrows
UPDATE subscriptions SET wallet_id=$1, name=$2, category=$3, amount_minor=$4,
    next_date=$5, billing_day=EXTRACT(DAY FROM $5::date)::smallint, interval_months=$6, active=$7
WHERE family_id=$8 AND id=$9;

-- name: DeleteSubscription :execrows
DELETE FROM subscriptions WHERE family_id=$1 AND id=$2;

-- name: GetSubscription :one
SELECT id, wallet_id, name, category, amount_minor, next_date, interval_months
FROM subscriptions WHERE family_id=$1 AND id=$2 AND active;

-- name: AdvanceSubscription :execrows
UPDATE subscriptions SET next_date = date_trunc('month', next_date + interval_months * INTERVAL '1 month')::date
    + LEAST(billing_day, EXTRACT(DAY FROM date_trunc('month', next_date + interval_months * INTERVAL '1 month') + INTERVAL '1 month - 1 day')::int) - 1
WHERE family_id=$1 AND id=$2 AND next_date=$3 AND active;

-- name: RenameSubscriptionCategory :exec
UPDATE subscriptions SET category=$1 WHERE family_id=$2 AND category=$3;

-- name: CountSubscriptionCategories :one
SELECT count(*) FROM subscriptions WHERE family_id=$1 AND category=$2;
