-- Akun dibuat lebih dulu, lalu memilih tepat satu keluarga.
ALTER TABLE users ADD COLUMN username TEXT;
UPDATE users SET username = 'rukun:' || id;
ALTER TABLE users ALTER COLUMN username SET NOT NULL;
CREATE UNIQUE INDEX users_username_idx ON users (lower(username));
ALTER TABLE users ALTER COLUMN family_id DROP NOT NULL;

-- Akun yang belum bergabung tidak boleh dianggap kepala keluarga.
ALTER TABLE users ADD CONSTRAINT users_username_length CHECK (char_length(username) BETWEEN 3 AND 40);
