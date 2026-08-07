-- Dua tambahan yang berbagi satu migrasi karena keduanya menyentuh bentuk
-- tabel transactions: siklus tagihan kartu kredit, dan pencatatan hutang
-- piutang terhadap pihak lain.

-- ---------- siklus kartu kredit ----------

-- Tanggal cetak tagihan dan tanggal jatuh tempo, sebagai tanggal dalam bulan.
-- Hanya berarti untuk dompet bertipe 'credit'; tipe lain membiarkannya NULL.
-- Nilai di atas jumlah hari suatu bulan diartikan sebagai hari terakhir bulan
-- itu, jadi 31 tetap masuk akal untuk Februari.
ALTER TABLE wallets ADD COLUMN settlement_day SMALLINT
    CHECK (settlement_day BETWEEN 1 AND 31);
ALTER TABLE wallets ADD COLUMN payment_day SMALLINT
    CHECK (payment_day BETWEEN 1 AND 31);
ALTER TABLE wallets ADD CONSTRAINT wallets_siklus_kartu CHECK (
    type = 'credit' OR (settlement_day IS NULL AND payment_day IS NULL)
);

-- ---------- pihak ----------

-- Lawan transaksi hutang piutang: orang atau lembaga tempat kita berhutang,
-- atau yang berhutang kepada kita.
CREATE TABLE parties (
    id         BIGSERIAL PRIMARY KEY,
    family_id  BIGINT NOT NULL REFERENCES families(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    note       TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (family_id, name)
);
ALTER TABLE parties ADD CONSTRAINT parties_family_id_key UNIQUE (family_id, id);

-- ---------- hutang piutang di tabel transactions ----------

-- Hutang dan piutang ikut menumpang tabel transactions, bukan tabel sendiri.
-- Saldo dompet dihitung dari satu query di walletSelect; kalau ada tabel kedua
-- yang juga menggerakkan saldo, query itu harus menggabungkan dua sumber dan
-- keduanya bisa menyimpang tanpa ketahuan.
ALTER TABLE transactions ADD COLUMN party_id BIGINT;
ALTER TABLE transactions ADD CONSTRAINT transactions_party_fkey
    FOREIGN KEY (family_id, party_id) REFERENCES parties (family_id, id);

-- Hutang boleh dicatat tanpa dompet: meminjam sesuatu yang tidak pernah masuk
-- ke rekening mana pun tetap menambah kewajiban. Nominal seperti itu tidak
-- punya dompet sebagai sumber mata uangnya, jadi mata uangnya disimpan di sini.
ALTER TABLE transactions ALTER COLUMN wallet_id DROP NOT NULL;
ALTER TABLE transactions ADD COLUMN currency CHAR(3);
ALTER TABLE transactions ADD CONSTRAINT transactions_currency_shape CHECK (
    (wallet_id IS NOT NULL AND currency IS NULL)
    OR (wallet_id IS NULL AND currency IS NOT NULL)
);

ALTER TABLE transactions DROP CONSTRAINT IF EXISTS kind_check;
ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_kind_check;
ALTER TABLE transactions ADD CONSTRAINT transactions_kind_check CHECK (
    kind IN ('expense', 'income', 'transfer', 'debt_in', 'debt_pay', 'loan_out', 'loan_in')
);

-- Bentuk tiap jenis ditulis ulang menyeluruh. Empat jenis baru:
--   debt_in   menerima pinjaman, kewajiban bertambah
--   debt_pay  membayar hutang, kewajiban berkurang
--   loan_out  memberi pinjaman, tagihan kita bertambah
--   loan_in   menerima pelunasan, tagihan kita berkurang
ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transfer_shape;
ALTER TABLE transactions ADD CONSTRAINT transactions_bentuk CHECK (
    (kind = 'transfer'
         AND wallet_id IS NOT NULL AND to_wallet_id IS NOT NULL
         AND amount_in_minor IS NOT NULL AND to_wallet_id <> wallet_id
         AND admin_fee_minor < amount_minor
         AND category IS NULL AND party_id IS NULL)
    OR (kind IN ('expense', 'income')
         AND wallet_id IS NOT NULL AND to_wallet_id IS NULL
         AND amount_in_minor IS NULL AND admin_fee_minor = 0
         AND category IS NOT NULL AND party_id IS NULL)
    OR (kind IN ('debt_in', 'debt_pay', 'loan_out', 'loan_in')
         AND to_wallet_id IS NULL AND amount_in_minor IS NULL
         AND admin_fee_minor = 0 AND category IS NULL
         AND party_id IS NOT NULL)
);

CREATE INDEX transactions_family_party_idx
    ON transactions (family_id, party_id) WHERE party_id IS NOT NULL;
