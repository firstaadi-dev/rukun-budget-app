-- Cicilan dan transaksi berulang dicatat sebagai beberapa transaksi bulanan
-- biasa. Kolom-kolom di bawah mengikat mereka jadi satu rangkaian, supaya
-- kategori dan catatan seluruhnya bisa diubah sekaligus dan seluruhnya bisa
-- dihapus sekaligus. Tanpa ini satu-satunya penanda adalah teks yang ditempel
-- di catatan — cukup untuk dibaca manusia, tidak cukup untuk dicari mesin.
--
-- Nomor urut dan jumlahnya ikut disimpan, tidak dihitung dari barisnya. Dihitung
-- lewat window function, "3 dari 12" berubah jadi "1 dari 1" begitu daftarnya
-- disaring per bulan — dan penyaring per bulan justru tampilan bawaannya.
CREATE SEQUENCE tx_series_seq;

ALTER TABLE transactions ADD COLUMN series_id BIGINT;
ALTER TABLE transactions ADD COLUMN series_seq SMALLINT;
ALTER TABLE transactions ADD COLUMN series_n SMALLINT;
ALTER TABLE transactions ADD COLUMN series_kind TEXT;

-- Batas 60 di bawah sama dengan cicilanMaks di cicilan.go. Keduanya harus
-- bergerak bersama: menaikkan yang di Go saja membuat form menerima angka yang
-- lalu ditolak database sebagai kesalahan server, bukan sebagai isian keliru.
ALTER TABLE transactions ADD CONSTRAINT transactions_series_shape CHECK (
    (series_id IS NULL AND series_seq IS NULL AND series_n IS NULL AND series_kind IS NULL)
    OR (series_id IS NOT NULL
        AND series_kind IN ('cicil', 'ulang')
        AND series_n BETWEEN 2 AND 60
        AND series_seq BETWEEN 1 AND series_n)
);

-- Rangkaian selalu dibaca utuh, dan hanya untuk baris yang memang punya.
CREATE INDEX transactions_series_idx
    ON transactions (family_id, series_id) WHERE series_id IS NOT NULL;
