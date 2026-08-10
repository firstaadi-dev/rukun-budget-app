-- Harga pasar yang berhasil diambil disimpan di sini, tidak lagi cuma di
-- memori proses.
--
-- Alasannya: Render menidurkan instance yang sedang tidak dipakai, dan tiap
-- bangun kembali cache di memori kosong. Permintaan pertama langsung
-- menghadapi pembatasan sumbernya, dan sesudah itu tidak ada satu pun "harga
-- terakhir" untuk ditampilkan — kalimat di layar yang menjanjikannya jadi
-- tidak benar persis saat ia paling dibutuhkan.
--
-- Tanpa family_id, dan itu disengaja. Ini harga pasar, bukan catatan keluarga:
-- harga SPUS sama untuk siapa pun yang memegangnya, dan menyalinnya per
-- keluarga cuma melipatgandakan permintaan ke sumber yang memang sedang
-- membatasi kami. Tidak ada satu pun angka milik keluarga di tabel ini, jadi
-- membacanya lintas keluarga tidak membocorkan apa-apa.
CREATE TABLE quotes (
    symbol   TEXT PRIMARY KEY,
    price_e4 BIGINT NOT NULL CHECK (price_e4 > 0),
    currency CHAR(3) NOT NULL,
    -- quoted_at: waktu yang disebut sumbernya sendiri. NULL berarti sumbernya
    -- tidak menerbitkan waktu sama sekali — NAB reksadana begitu — dan
    -- fetched_at yang dipakai sebagai gantinya, dengan label yang berkata
    -- "diambil" alih-alih "per". Itu pula yang dibaca Kuotasi.Diambil.
    quoted_at  TIMESTAMPTZ,
    name       TEXT NOT NULL DEFAULT '',
    fetched_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
