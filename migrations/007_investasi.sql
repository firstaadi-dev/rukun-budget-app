-- Investasi fase 1: emas batangan, saham dan ETF Amerika, serta reksadana
-- Indonesia. Ketiganya berbeda cara dibeli, tapi sama bentuknya — sejumlah unit
-- yang dimiliki, dengan modal yang sudah dikeluarkan dan harga pasar yang
-- bergerak sendiri — jadi ketiganya ditampung satu tabel, bukan tiga.

-- ---------- dompet broker ----------

-- Saham dan reksadana dibeli dari saldo yang sudah lebih dulu ditopup ke
-- brokernya. Saldo itu dompet sungguhan: uangnya sudah keluar dari bank, sudah
-- kena kurs dan biaya transfer, tapi belum jadi aset apa pun. Tanpa dompet
-- sendiri, uang yang mengendap di broker akan hilang dari catatan sampai ia
-- terpakai membeli sesuatu.
--
-- Topupnya tidak butuh jenis transaksi baru: ia transfer antar dompet yang
-- sudah ada, lengkap dengan kurs dan biaya adminnya.
ALTER TABLE wallets DROP CONSTRAINT wallets_type_check;
ALTER TABLE wallets ADD CONSTRAINT wallets_type_check
    CHECK (type IN ('cash', 'bank', 'credit', 'ewallet', 'paylater', 'broker'));

-- ---------- posisi investasi ----------

-- Satu baris untuk satu hal yang dimiliki, bukan satu baris per pembelian.
-- Pembeliannya sendiri hidup di transactions, sama seperti hutang piutang:
-- saldo dompet diturunkan dari satu query di walletSelect, dan sumber kedua
-- yang ikut menggerakkan saldo akan menyimpang tanpa ketahuan.
CREATE TABLE investments (
    id        BIGSERIAL PRIMARY KEY,
    family_id BIGINT NOT NULL REFERENCES families(id) ON DELETE CASCADE,

    kind TEXT NOT NULL CHECK (kind IN ('gold', 'stock', 'fund')),
    name TEXT NOT NULL,
    -- Varian membedakan dua posisi yang namanya sama. Untuk emas ia pecahan
    -- kepingnya ("10 gram"): premi per gram keping kecil lebih mahal dan
    -- harga jualnya kembali berbeda, jadi menggabungkan semua keping Antam
    -- jadi satu posisi akan menyembunyikan selisih yang nyata.
    varian TEXT NOT NULL DEFAULT '',

    -- Simbol di sumber harga pasar. Kosong berarti harganya diisi sendiri —
    -- reksadana Indonesia tidak punya sumber terbuka yang bisa diandalkan.
    symbol   TEXT NOT NULL DEFAULT '',
    currency CHAR(3) NOT NULL,

    -- Dompet broker tempat posisi ini dibeli. NULL untuk emas, yang dibeli
    -- langsung dari dompet mana pun dan tidak menyimpan saldo di mana pun.
    wallet_id BIGINT,

    -- Harga satuan yang diisi sendiri, dipakai saat symbol kosong atau saat
    -- sumber harga sedang tidak bisa dihubungi.
    --
    -- Satuannya empat desimal lebih halus dari satuan terkecil mata uangnya:
    -- NAB reksadana lazim ditulis empat desimal, dan membulatkannya ke rupiah
    -- penuh menghapus persis perbedaan antar hari yang ingin dilihat.
    manual_price_e4 BIGINT CHECK (manual_price_e4 > 0),
    manual_price_on DATE,

    note       TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (family_id, kind, name, varian),
    CONSTRAINT investments_harga_manual CHECK (
        (manual_price_e4 IS NULL) = (manual_price_on IS NULL)
    )
);
ALTER TABLE investments ADD CONSTRAINT investments_family_id_key UNIQUE (family_id, id);
ALTER TABLE investments ADD CONSTRAINT investments_wallet_fkey
    FOREIGN KEY (family_id, wallet_id) REFERENCES wallets (family_id, id);

-- ---------- pembelian di tabel transactions ----------

-- Kuantitas disimpan sebagai bilangan bulat berskala 1e8, bukan pecahan biner.
-- Alasannya sama dengan nominal uang: 0,1 lembar tidak punya wakil eksak di
-- float64, dan galatnya menumpuk di penjumlahan lot lalu tidak bisa dilacak
-- balik. Delapan desimal menampung unit reksadana yang lazim empat desimal dan
-- saham pecahan yang lazim enam.
ALTER TABLE transactions ADD COLUMN investment_id BIGINT;
ALTER TABLE transactions ADD COLUMN qty_e8 BIGINT CHECK (qty_e8 > 0);
ALTER TABLE transactions ADD CONSTRAINT transactions_investment_fkey
    FOREIGN KEY (family_id, investment_id) REFERENCES investments (family_id, id);

ALTER TABLE transactions DROP CONSTRAINT transactions_kind_check;
ALTER TABLE transactions ADD CONSTRAINT transactions_kind_check CHECK (
    kind IN ('expense', 'income', 'transfer',
             'debt_in', 'debt_pay', 'loan_out', 'loan_in',
             'invest_buy')
);

-- Bentuk tiap jenis ditulis ulang menyeluruh, sekali lagi. invest_buy boleh
-- tanpa dompet: emas yang dibeli bertahun-tahun sebelum aplikasi ini dipakai
-- tetap perlu tercatat, dan memaksanya menunjuk sebuah dompet akan mengurangi
-- saldo hari ini karena uang yang keluar jauh di masa lalu.
ALTER TABLE transactions DROP CONSTRAINT transactions_bentuk;
ALTER TABLE transactions ADD CONSTRAINT transactions_bentuk CHECK (
    (kind = 'transfer'
         AND wallet_id IS NOT NULL AND to_wallet_id IS NOT NULL
         AND amount_in_minor IS NOT NULL AND to_wallet_id <> wallet_id
         AND admin_fee_minor < amount_minor
         AND category IS NULL AND party_id IS NULL
         AND investment_id IS NULL AND qty_e8 IS NULL)
    OR (kind IN ('expense', 'income')
         AND wallet_id IS NOT NULL AND to_wallet_id IS NULL
         AND amount_in_minor IS NULL AND admin_fee_minor = 0
         AND category IS NOT NULL AND party_id IS NULL
         AND investment_id IS NULL AND qty_e8 IS NULL)
    OR (kind IN ('debt_in', 'debt_pay', 'loan_out', 'loan_in')
         AND to_wallet_id IS NULL AND amount_in_minor IS NULL
         AND admin_fee_minor = 0 AND category IS NULL
         AND party_id IS NOT NULL
         AND investment_id IS NULL AND qty_e8 IS NULL)
    OR (kind = 'invest_buy'
         AND to_wallet_id IS NULL AND amount_in_minor IS NULL
         AND admin_fee_minor < amount_minor
         AND category IS NULL AND party_id IS NULL
         AND investment_id IS NOT NULL AND qty_e8 IS NOT NULL)
);

CREATE INDEX transactions_family_investment_idx
    ON transactions (family_id, investment_id) WHERE investment_id IS NOT NULL;
