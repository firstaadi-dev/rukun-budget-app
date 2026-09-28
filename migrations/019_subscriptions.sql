CREATE TABLE subscriptions (
    id BIGSERIAL PRIMARY KEY,
    family_id BIGINT NOT NULL REFERENCES families(id) ON DELETE CASCADE,
    wallet_id BIGINT NOT NULL,
    name TEXT NOT NULL CHECK (length(name) BETWEEN 2 AND 60),
    category TEXT NOT NULL,
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    next_date DATE NOT NULL,
    billing_day SMALLINT NOT NULL CHECK (billing_day BETWEEN 1 AND 31),
    interval_months SMALLINT NOT NULL CHECK (interval_months IN (1, 2, 3, 6, 12)),
    active BOOLEAN NOT NULL DEFAULT TRUE,
    FOREIGN KEY (family_id, wallet_id) REFERENCES wallets(family_id, id) ON DELETE RESTRICT
);
CREATE INDEX subscriptions_due_idx ON subscriptions (family_id, active, next_date);
