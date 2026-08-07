-- Skema awal Rukun (fase 1 dan 2). Berkas migrasi tidak boleh diubah lagi
-- setelah pernah diterapkan di database yang hidup: perubahan berikutnya
-- ditulis sebagai berkas baru bernomor lebih besar.

CREATE TABLE IF NOT EXISTS users (
    id            BIGSERIAL PRIMARY KEY,
    name          TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS sessions (
    token      TEXT PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_expiry_idx ON sessions (expires_at);

CREATE TABLE IF NOT EXISTS wallets (
    id                    BIGSERIAL PRIMARY KEY,
    name                  TEXT NOT NULL,
    type                  TEXT NOT NULL CHECK (type IN ('cash', 'bank', 'credit', 'ewallet')),
    provider              TEXT,
    currency              CHAR(3) NOT NULL,
    -- Saldo awal. Satuan terkecil mata uang dompet, lihat money.go.
    initial_balance_minor BIGINT NOT NULL DEFAULT 0,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Kategori pengeluaran dan pemasukan yang bisa ditambah keluarga sendiri.
-- transactions.category tetap menyimpan namanya sebagai teks, bukan foreign key:
-- dengan begitu tabel ini murni sumber daftar pilihan, dan transaksi lama tetap
-- terbaca apa adanya kalau sebuah kategori dihapus. Konsekuensinya, mengganti
-- nama kategori harus ikut memperbarui transaksinya — lihat RenameCategory.
CREATE TABLE IF NOT EXISTS categories (
    id         BIGSERIAL PRIMARY KEY,
    kind       TEXT NOT NULL CHECK (kind IN ('expense', 'income')),
    name       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (kind, name)
);

-- Kategori bawaan, sama dengan yang ada di mockup fase 1. Hanya terpasang saat
-- tabel masih kosong, sehingga keluarga yang sudah merapikan daftarnya sendiri
-- tidak kebanjiran kategori bawaan lagi setiap kali aplikasi start.
INSERT INTO categories (kind, name)
SELECT 'expense', name FROM unnest(ARRAY['Belanja', 'Tagihan', 'Transportasi', 'Makanan', 'Kesehatan', 'Lainnya']) AS name
WHERE NOT EXISTS (SELECT 1 FROM categories)
UNION ALL
SELECT 'income', name FROM unnest(ARRAY['Gaji', 'Bonus', 'Hadiah', 'Investasi', 'Lainnya']) AS name
WHERE NOT EXISTS (SELECT 1 FROM categories);

CREATE TABLE IF NOT EXISTS transactions (
    id              BIGSERIAL PRIMARY KEY,
    kind            TEXT NOT NULL CHECK (kind IN ('expense', 'income', 'transfer')),
    occurred_on     DATE NOT NULL,
    -- Sisi sumber. Untuk pemasukan, dompet inilah yang bertambah.
    wallet_id       BIGINT NOT NULL REFERENCES wallets(id) ON DELETE RESTRICT,
    amount_minor    BIGINT NOT NULL CHECK (amount_minor > 0),
    category        TEXT,
    -- Sisi tujuan, khusus transfer.
    to_wallet_id    BIGINT REFERENCES wallets(id) ON DELETE RESTRICT,
    amount_in_minor BIGINT CHECK (amount_in_minor > 0),
    -- Biaya admin, dalam mata uang dompet sumber, sudah termasuk di
    -- amount_minor. Kurs transfer tidak disimpan: ia rasio amount_minor
    -- dikurangi biaya admin terhadap amount_in_minor, jadi selalu eksak.
    admin_fee_minor BIGINT NOT NULL DEFAULT 0 CHECK (admin_fee_minor >= 0),
    note            TEXT NOT NULL DEFAULT '',
    created_by      BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT transfer_shape CHECK (
        (kind = 'transfer'
             AND to_wallet_id IS NOT NULL
             AND amount_in_minor IS NOT NULL
             AND to_wallet_id <> wallet_id
             AND admin_fee_minor < amount_minor)
        OR (kind <> 'transfer'
             AND to_wallet_id IS NULL
             AND amount_in_minor IS NULL
             AND admin_fee_minor = 0
             AND category IS NOT NULL)
    )
);

CREATE INDEX IF NOT EXISTS tx_wallet_idx ON transactions (wallet_id);
CREATE INDEX IF NOT EXISTS tx_to_wallet_idx ON transactions (to_wallet_id);
CREATE INDEX IF NOT EXISTS tx_recent_idx ON transactions (occurred_on DESC, id DESC);
