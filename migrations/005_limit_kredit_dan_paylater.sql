-- Limit adalah pembeda utama akun berbasis kredit: ia yang menentukan berapa
-- lagi yang boleh dipakai, dan tanpa itu tidak ada yang menahan pemakaian.
--
-- PayLater dipisahkan jadi jenis dompet sendiri. Sebelumnya ia hanya bisa
-- dicatat dengan menyamar sebagai kartu kredit, padahal penyedianya berbeda dan
-- namanya di layar jadi salah.

ALTER TABLE wallets ADD COLUMN credit_limit_minor BIGINT
    CHECK (credit_limit_minor > 0);

ALTER TABLE wallets DROP CONSTRAINT wallets_type_check;
ALTER TABLE wallets ADD CONSTRAINT wallets_type_check
    CHECK (type IN ('cash', 'bank', 'credit', 'ewallet', 'paylater'));

-- Limit dan siklus tagihan hanya berarti untuk jenis berbasis kredit. Jenis
-- lain tidak boleh menyimpannya sama sekali, supaya tidak ada kolom terisi yang
-- tak pernah dibaca lalu membingungkan saat ditelusuri belakangan.
ALTER TABLE wallets DROP CONSTRAINT wallets_siklus_kartu;
ALTER TABLE wallets ADD CONSTRAINT wallets_siklus_kartu CHECK (
    type IN ('credit', 'paylater')
    OR (settlement_day IS NULL AND payment_day IS NULL AND credit_limit_minor IS NULL)
);
