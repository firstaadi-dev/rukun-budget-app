ALTER TABLE families
    ADD COLUMN period_start_day SMALLINT NOT NULL DEFAULT 1
    CHECK (period_start_day BETWEEN 1 AND 28);
