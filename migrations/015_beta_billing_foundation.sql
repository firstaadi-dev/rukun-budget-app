-- Metadata kesiapan monetisasi disimpan per keluarga karena data produk
-- dimiliki bersama oleh seluruh anggota keluarga.
ALTER TABLE families
    ADD COLUMN billing_owner_user_id BIGINT,
    ADD COLUMN beta_started_at TIMESTAMPTZ,
    ADD COLUMN beta_cohort TEXT;

-- Keluarga yang sudah ada masuk cohort beta; kepala keluarga yang selama ini
-- ditentukan dari anggota pertama menjadi pemilik billing awal.
UPDATE families f
SET billing_owner_user_id = (
        SELECT min(u.id) FROM users u WHERE u.family_id = f.id
    ),
    beta_started_at = f.created_at,
    beta_cohort = 'existing-beta';

ALTER TABLE families
    ALTER COLUMN beta_started_at SET DEFAULT now(),
    ALTER COLUMN beta_started_at SET NOT NULL,
    ALTER COLUMN beta_cohort SET DEFAULT 'beta',
    ALTER COLUMN beta_cohort SET NOT NULL;

-- Pemilik billing harus merupakan anggota keluarga yang sama. Ditunda karena
-- keluarga baru dibuat sebelum user dipautkan dalam transaksi yang sama.
ALTER TABLE families
    ADD CONSTRAINT families_billing_owner_member_fk
    FOREIGN KEY (id, billing_owner_user_id)
    REFERENCES users (family_id, id)
    DEFERRABLE INITIALLY DEFERRED;
