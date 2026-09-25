-- Penjualan mengembalikan uang ke dompet dan mengurangi unit posisi.
-- Harga pokok rata-rata saat jual disimpan agar laba terealisasi tetap dapat
-- diaudit dari transaksi penjualan, termasuk setelah pembelian berikutnya.
ALTER TABLE transactions ADD COLUMN cost_basis_minor BIGINT;
ALTER TABLE transactions ADD CONSTRAINT transactions_cost_basis_shape CHECK (
    (kind = 'invest_sell' AND cost_basis_minor IS NOT NULL AND cost_basis_minor >= 0)
    OR (kind <> 'invest_sell' AND cost_basis_minor IS NULL)
);

ALTER TABLE transactions DROP CONSTRAINT transactions_kind_check;
ALTER TABLE transactions ADD CONSTRAINT transactions_kind_check CHECK (
    kind IN ('expense', 'income', 'transfer',
             'debt_in', 'debt_pay', 'loan_out', 'loan_in',
             'invest_buy', 'invest_sell')
);

ALTER TABLE transactions DROP CONSTRAINT transactions_bentuk;
ALTER TABLE transactions ADD CONSTRAINT transactions_bentuk CHECK (
    (kind = 'transfer'
         AND wallet_id IS NOT NULL AND to_wallet_id IS NOT NULL
         AND amount_in_minor IS NOT NULL AND to_wallet_id <> wallet_id
         AND admin_fee_minor < amount_minor
         AND category IS NULL AND party_id IS NULL
         AND investment_id IS NULL AND qty_e8 IS NULL)
    OR (kind IN ('expense', 'income')
         AND wallet_id IS NOT NULL AND to_wallet_id IS NULL
         AND amount_in_minor IS NULL AND admin_fee_minor = 0
         AND category IS NOT NULL AND party_id IS NULL
         AND investment_id IS NULL AND qty_e8 IS NULL)
    OR (kind IN ('debt_in', 'debt_pay', 'loan_out', 'loan_in')
         AND to_wallet_id IS NULL AND amount_in_minor IS NULL
         AND admin_fee_minor = 0 AND category IS NULL
         AND party_id IS NOT NULL
         AND investment_id IS NULL AND qty_e8 IS NULL)
    OR (kind = 'invest_buy'
         AND to_wallet_id IS NULL AND amount_in_minor IS NULL
         AND admin_fee_minor < amount_minor
         AND category IS NULL AND party_id IS NULL
         AND investment_id IS NOT NULL AND qty_e8 IS NOT NULL)
    OR (kind = 'invest_sell'
         AND wallet_id IS NOT NULL AND to_wallet_id IS NULL
         AND amount_in_minor IS NULL AND admin_fee_minor = 0
         AND category IS NULL AND party_id IS NULL
         AND investment_id IS NOT NULL AND qty_e8 IS NOT NULL)
);
