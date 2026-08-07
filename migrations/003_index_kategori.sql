-- Index untuk penyaringan dan penjumlahan per kategori.
--
-- Dipakai tiga tempat: hitung pemakaian di halaman Kategori, filter kategori di
-- daftar transaksi, dan pengelompokan ringkasan bulanan di dashboard. Ketiganya
-- selalu menyaring family_id lebih dulu, jadi kolom itu yang memimpin.
CREATE INDEX transactions_family_category_idx
    ON transactions (family_id, kind, category);

-- Dua index berikut sudah tidak bisa terpakai sejak aplikasi jadi multi-tenant,
-- tapi tetap dibayar biayanya pada setiap INSERT dan UPDATE.
--
-- tx_recent_idx diawali occurred_on, sedangkan setiap query transaksi sekarang
-- menyaring family_id lebih dulu; penggantinya transactions_family_recent_idx.
DROP INDEX IF EXISTS tx_recent_idx;

-- wallets_family_idx hanya berisi family_id, yang sudah jadi kolom pertama pada
-- wallets_family_id_key (family_id, id) — pencarian per keluarga tetap terlayani.
DROP INDEX IF EXISTS wallets_family_idx;
