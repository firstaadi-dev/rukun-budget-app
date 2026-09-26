-- Firebase menjadi sumber autentikasi. Identitas bisnis dan keluarga tetap
-- berada di users agar semua foreign key data keluarga tidak berubah.
ALTER TABLE users
    ADD COLUMN email TEXT,
    ADD COLUMN firebase_uid TEXT,
    ADD COLUMN email_verified_at TIMESTAMPTZ;

CREATE UNIQUE INDEX users_email_lower_unique
    ON users (lower(email)) WHERE email IS NOT NULL;

CREATE UNIQUE INDEX users_firebase_uid_unique
    ON users (firebase_uid) WHERE firebase_uid IS NOT NULL;
