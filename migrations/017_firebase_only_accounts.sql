-- Akun Firebase baru tidak membutuhkan kredensial lokal. Kolom lama tetap
-- tersedia selama versi aplikasi sebelum deploy selesai berjalan.
ALTER TABLE users
    ALTER COLUMN username DROP NOT NULL,
    ALTER COLUMN password_hash DROP NOT NULL;
