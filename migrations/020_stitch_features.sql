ALTER TABLE wallets ADD COLUMN owner_label TEXT NOT NULL DEFAULT '', ADD COLUMN last_four TEXT NOT NULL DEFAULT '' CHECK (last_four = '' OR last_four ~ '^[0-9]{4}$'), ADD COLUMN cardholder TEXT NOT NULL DEFAULT '', ADD COLUMN is_primary BOOLEAN NOT NULL DEFAULT FALSE;
CREATE UNIQUE INDEX wallets_one_primary ON wallets(family_id) WHERE is_primary;
ALTER TABLE transactions ADD COLUMN occurred_time TEXT NOT NULL DEFAULT '' CHECK (occurred_time = '' OR occurred_time ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$'), ADD COLUMN recorder_id BIGINT REFERENCES users(id) ON DELETE SET NULL, ADD COLUMN receipt BYTEA, ADD COLUMN receipt_mime TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN avatar BYTEA, ADD COLUMN avatar_mime TEXT NOT NULL DEFAULT '';
ALTER TABLE parties ADD COLUMN due_on DATE, ADD COLUMN payment_wallet_id BIGINT, ADD CONSTRAINT parties_payment_wallet_fk FOREIGN KEY (family_id, payment_wallet_id) REFERENCES wallets(family_id,id);
CREATE TABLE transaction_reactions (transaction_id BIGINT NOT NULL REFERENCES transactions(id) ON DELETE CASCADE, user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE, PRIMARY KEY(transaction_id,user_id));
CREATE TABLE savings_goals (family_id BIGINT PRIMARY KEY REFERENCES families(id) ON DELETE CASCADE, name TEXT NOT NULL CHECK(length(name) BETWEEN 2 AND 80), target_minor BIGINT NOT NULL CHECK(target_minor > 0), target_on DATE);
ALTER TABLE transactions ADD CONSTRAINT transactions_recorder_family_fk FOREIGN KEY (family_id,recorder_id) REFERENCES users(family_id,id);
ALTER TABLE families ADD COLUMN ledger_revision BIGINT NOT NULL DEFAULT 0, ADD COLUMN saving_target SMALLINT NOT NULL DEFAULT 20 CHECK(saving_target BETWEEN 1 AND 100);
CREATE FUNCTION bump_family_ledger() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    UPDATE families SET ledger_revision=ledger_revision+1 WHERE id=OLD.family_id;
    RETURN OLD;
  END IF;
  UPDATE families SET ledger_revision=ledger_revision+1 WHERE id=NEW.family_id;
  RETURN NEW;
END;
$$;
CREATE TRIGGER transactions_revision AFTER INSERT OR UPDATE OR DELETE ON transactions FOR EACH ROW EXECUTE FUNCTION bump_family_ledger();
CREATE TRIGGER wallets_revision AFTER INSERT OR UPDATE OR DELETE ON wallets FOR EACH ROW EXECUTE FUNCTION bump_family_ledger();
CREATE TRIGGER categories_revision AFTER INSERT OR UPDATE OR DELETE ON categories FOR EACH ROW EXECUTE FUNCTION bump_family_ledger();
CREATE TRIGGER subscriptions_revision AFTER INSERT OR UPDATE OR DELETE ON subscriptions FOR EACH ROW EXECUTE FUNCTION bump_family_ledger();
CREATE TRIGGER investments_revision AFTER INSERT OR UPDATE OR DELETE ON investments FOR EACH ROW EXECUTE FUNCTION bump_family_ledger();
CREATE TRIGGER savings_goals_revision AFTER INSERT OR UPDATE OR DELETE ON savings_goals FOR EACH ROW EXECUTE FUNCTION bump_family_ledger();
