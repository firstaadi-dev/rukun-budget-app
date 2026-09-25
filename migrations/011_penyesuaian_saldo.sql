-- Bedakan koreksi saldo dari belanja/penghasilan tanpa bergantung pada nama
-- kategori yang bisa juga dipakai untuk transaksi biasa.
ALTER TABLE transactions ADD COLUMN is_adjustment BOOLEAN NOT NULL DEFAULT false;
UPDATE transactions SET is_adjustment = true
 WHERE kind IN ('expense', 'income') AND category = 'Penyesuaian Saldo'
   AND note = 'Penyesuaian Saldo';
