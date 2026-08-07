-- Rukun jadi multi-tenant: satu deployment melayani banyak keluarga, dan data
-- tiap keluarga tidak boleh terlihat oleh keluarga lain.
--
-- family_id ditempelkan ke setiap tabel data, termasuk transactions yang
-- sebenarnya bisa disimpulkan lewat wallet_id. Denormalisasi itu disengaja:
-- dengan kolomnya ada langsung, setiap query cukup menyaring satu kolom, dan
-- foreign key gabungan di bawah bisa menjamin transaksi tidak pernah menunjuk
-- dompet milik keluarga lain — jaminan di level database, bukan sekadar
-- kedisiplinan kode.

CREATE TABLE families (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT NOT NULL,
    -- Kode undangan, dulunya satu env var untuk seluruh deployment.
    signup_code TEXT NOT NULL UNIQUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Satu keluarga penampung untuk data yang sudah ada, sekaligus keluarga
-- pertama pada database yang masih kosong. Kodenya acak; developer melihatnya
-- lewat API admin, lalu mengganti namanya dari sana.
INSERT INTO families (name, signup_code)
VALUES ('Keluarga', substr(replace(gen_random_uuid()::text, '-', ''), 1, 16));

-- ---------- users ----------

ALTER TABLE users ADD COLUMN family_id BIGINT REFERENCES families(id) ON DELETE CASCADE;
UPDATE users SET family_id = (SELECT id FROM families ORDER BY id LIMIT 1);
ALTER TABLE users ALTER COLUMN family_id SET NOT NULL;

-- Nama anggota cukup unik di dalam keluarganya: hampir setiap keluarga punya
-- "Ayah". Karena itu login butuh kode keluarga untuk menentukan yang mana.
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_name_key;
CREATE UNIQUE INDEX users_family_name_idx ON users (family_id, lower(name));
ALTER TABLE users ADD CONSTRAINT users_family_id_key UNIQUE (family_id, id);

-- ---------- wallets ----------

ALTER TABLE wallets ADD COLUMN family_id BIGINT REFERENCES families(id) ON DELETE CASCADE;
UPDATE wallets SET family_id = (SELECT id FROM families ORDER BY id LIMIT 1);
ALTER TABLE wallets ALTER COLUMN family_id SET NOT NULL;
ALTER TABLE wallets ADD CONSTRAINT wallets_family_id_key UNIQUE (family_id, id);
CREATE INDEX wallets_family_idx ON wallets (family_id);

-- ---------- categories ----------

ALTER TABLE categories ADD COLUMN family_id BIGINT REFERENCES families(id) ON DELETE CASCADE;
UPDATE categories SET family_id = (SELECT id FROM families ORDER BY id LIMIT 1);
ALTER TABLE categories ALTER COLUMN family_id SET NOT NULL;
ALTER TABLE categories DROP CONSTRAINT IF EXISTS categories_kind_name_key;
ALTER TABLE categories ADD CONSTRAINT categories_family_kind_name_key UNIQUE (family_id, kind, name);

-- ---------- transactions ----------

ALTER TABLE transactions ADD COLUMN family_id BIGINT REFERENCES families(id) ON DELETE CASCADE;
UPDATE transactions SET family_id = (SELECT id FROM families ORDER BY id LIMIT 1);
ALTER TABLE transactions ALTER COLUMN family_id SET NOT NULL;

-- Foreign key gabungan menggantikan yang lama. Sekarang tidak ada nilai
-- wallet_id yang bisa dimasukkan kalau dompetnya milik keluarga lain, berapa
-- pun cerobohnya kode di atasnya. Untuk to_wallet_id yang boleh NULL, aturan
-- MATCH SIMPLE bawaan Postgres membuat batasannya dilewati saat kolomnya NULL,
-- yang memang perilaku yang diinginkan untuk pengeluaran dan pemasukan.
ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_wallet_id_fkey;
ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_to_wallet_id_fkey;
ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_created_by_fkey;

ALTER TABLE transactions ADD CONSTRAINT transactions_wallet_fkey
    FOREIGN KEY (family_id, wallet_id) REFERENCES wallets (family_id, id) ON DELETE RESTRICT;
ALTER TABLE transactions ADD CONSTRAINT transactions_to_wallet_fkey
    FOREIGN KEY (family_id, to_wallet_id) REFERENCES wallets (family_id, id) ON DELETE RESTRICT;
ALTER TABLE transactions ADD CONSTRAINT transactions_created_by_fkey
    FOREIGN KEY (family_id, created_by) REFERENCES users (family_id, id);

CREATE INDEX transactions_family_recent_idx ON transactions (family_id, occurred_on DESC, id DESC);
