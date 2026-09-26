-- Kode lama tetap dapat dipakai untuk login lama, tetapi undangan baru
-- kedaluwarsa setelah tujuh hari dan dapat diperbarui kepala keluarga.
ALTER TABLE families ADD COLUMN signup_code_expires_at TIMESTAMPTZ NOT NULL DEFAULT (now() + interval '7 days');
