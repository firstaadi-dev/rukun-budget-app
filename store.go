package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ db *pgxpool.Pool }

var ErrNotFound = errors.New("data tidak ditemukan")

// Aplikasi ini multi-tenant. Setiap metode di bawah yang menyentuh data
// keluarga mengambil familyID sebagai argumen pertama, dan tidak ada varian
// tanpa batas yang bisa dipanggil sembarangan — satu query yang lupa menyaring
// berarti keluarga lain melihat isi rekening orang. Sebagai lapisan kedua,
// migrasi 002 memasang foreign key gabungan sehingga database sendiri menolak
// transaksi yang menunjuk dompet milik keluarga lain.

// ---------- Wallet ----------

type Wallet struct {
	ID           int64
	Name         string
	Type         string // cash | bank | credit | ewallet
	Provider     string
	Currency     string
	InitialMinor int64
	BalanceMinor int64
	// Tanggal cetak tagihan dan jatuh tempo, hanya untuk akun berbasis kredit.
	// Nol berarti siklusnya belum diisi.
	SettlementDay int
	PaymentDay    int
	// LimitMinor: pagu pemakaian. Nol berarti belum diisi, bukan nol rupiah —
	// tanpa limit aplikasi tidak menahan pemakaian sama sekali.
	LimitMinor int64
}

func (w Wallet) HasCycle() bool { return w.SettlementDay > 0 && w.PaymentDay > 0 }
func (w Wallet) HasLimit() bool { return w.LimitMinor > 0 }

// IsCredit: akun yang saldonya bergerak ke arah negatif saat dipakai, dan
// dilunasi belakangan. Kartu kredit dan PayLater berperilaku sama persis di
// seluruh aplikasi — perbedaannya cuma nama dan daftar penyedianya.
func (w Wallet) IsCredit() bool { return w.Type == "credit" || w.Type == "paylater" }

// TerpakaiMinor: berapa yang sedang terpakai dari limit. Saldo akun kredit
// negatif saat dipakai, jadi ini kebalikan tandanya.
func (w Wallet) TerpakaiMinor() int64 { return max(0, -w.BalanceMinor) }

// SisaLimitMinor: berapa lagi yang boleh dipakai.
func (w Wallet) SisaLimitMinor() int64 { return max(0, w.LimitMinor-w.TerpakaiMinor()) }

var walletTypeLabel = map[string]string{
	"cash": "Tunai", "bank": "Bank", "credit": "Kartu Kredit",
	"ewallet": "E-Wallet", "paylater": "PayLater",
}

func (w Wallet) TypeLabel() string { return walletTypeLabel[w.Type] }

// Subtitle: "BCA · Bank" untuk bank/kartu kredit, "GoPay" untuk e-wallet.
func (w Wallet) Subtitle() string {
	if w.Provider == "" {
		return w.TypeLabel()
	}
	if w.Type == "bank" || w.IsCredit() {
		return w.Provider + " · " + w.TypeLabel()
	}
	return w.Provider
}

// Saldo dompet selalu diturunkan dari transaksi, tidak pernah disimpan —
// tidak ada kolom yang bisa melenceng dari catatan.
//
// Penyaring family_id sengaja jadi bagian dari potongan query ini, bukan
// ditambahkan tiap pemanggil: potongan yang netral hanya aman selama semua
// pemanggilnya ingat, dan yang lupa tidak akan menghasilkan error apa pun.
// Pemanggil menambahkan syarat lain dengan AND, mulai dari $2.
const walletSelect = `
SELECT w.id, w.name, w.type, COALESCE(w.provider, ''), w.currency, w.initial_balance_minor,
       COALESCE(w.settlement_day, 0), COALESCE(w.payment_day, 0),
       COALESCE(w.credit_limit_minor, 0),
       w.initial_balance_minor
       + COALESCE((SELECT SUM(CASE WHEN t.kind IN ('income', 'debt_in', 'loan_in')
                                   THEN t.amount_minor ELSE -t.amount_minor END)
                   FROM transactions t WHERE t.wallet_id = w.id), 0)
       + COALESCE((SELECT SUM(t.amount_in_minor)
                   FROM transactions t WHERE t.to_wallet_id = w.id), 0) AS balance_minor
FROM wallets w
WHERE w.family_id = $1`

func scanWallets(rows pgx.Rows) ([]Wallet, error) {
	defer rows.Close()
	var out []Wallet
	for rows.Next() {
		var w Wallet
		if err := rows.Scan(&w.ID, &w.Name, &w.Type, &w.Provider, &w.Currency, &w.InitialMinor,
			&w.SettlementDay, &w.PaymentDay, &w.LimitMinor, &w.BalanceMinor); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) Wallets(ctx context.Context, familyID int64) ([]Wallet, error) {
	rows, err := s.db.Query(ctx, walletSelect+" ORDER BY w.type, w.id", familyID)
	if err != nil {
		return nil, err
	}
	return scanWallets(rows)
}

func (s *Store) Wallet(ctx context.Context, familyID, id int64) (Wallet, error) {
	rows, err := s.db.Query(ctx, walletSelect+" AND w.id = $2", familyID, id)
	if err != nil {
		return Wallet{}, err
	}
	ws, err := scanWallets(rows)
	if err != nil {
		return Wallet{}, err
	}
	if len(ws) == 0 {
		return Wallet{}, ErrNotFound
	}
	return ws[0], nil
}

func (s *Store) CreateWallet(ctx context.Context, familyID int64, w Wallet) (int64, error) {
	var id int64
	err := s.db.QueryRow(ctx,
		`INSERT INTO wallets (family_id, name, type, provider, currency, initial_balance_minor,
		                      settlement_day, payment_day, credit_limit_minor)
		 VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6,
		         NULLIF($7, 0), NULLIF($8, 0), NULLIF($9, 0)::BIGINT) RETURNING id`,
		familyID, w.Name, w.Type, w.Provider, w.Currency, w.InitialMinor,
		w.SettlementDay, w.PaymentDay, w.LimitMinor).Scan(&id)
	return id, err
}

func (s *Store) UpdateWallet(ctx context.Context, familyID int64, w Wallet) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE wallets SET name = $1, type = $2, provider = NULLIF($3, ''),
		        currency = $4, initial_balance_minor = $5,
		        settlement_day = NULLIF($6, 0), payment_day = NULLIF($7, 0),
		        credit_limit_minor = NULLIF($8, 0)::BIGINT
		 WHERE id = $9 AND family_id = $10`,
		w.Name, w.Type, w.Provider, w.Currency, w.InitialMinor,
		w.SettlementDay, w.PaymentDay, w.LimitMinor, w.ID, familyID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// DeleteWallet menolak menghapus dompet yang masih dipakai transaksi —
// menghapusnya diam-diam akan mengubah saldo dompet lain tanpa jejak.
func (s *Store) DeleteWallet(ctx context.Context, familyID, id int64) error {
	var n int
	if err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM transactions
		  WHERE family_id = $1 AND (wallet_id = $2 OR to_wallet_id = $2)`, familyID, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("dompet ini masih dipakai %d transaksi, hapus transaksinya dulu", n)
	}
	tag, err := s.db.Exec(ctx, `DELETE FROM wallets WHERE id = $1 AND family_id = $2`, id, familyID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// ---------- Transaction ----------

type Tx struct {
	ID   int64
	Kind string // expense | income | transfer | debt_in | debt_pay | loan_out | loan_in
	Date time.Time
	// WalletID nol berarti transaksi ini tidak menyentuh dompet mana pun.
	// Hanya mungkin untuk hutang piutang: meminjam sesuatu yang tidak pernah
	// masuk rekening tetap menambah kewajiban.
	WalletID     int64
	WalletName   string
	WalletCur    string // dari dompetnya, atau dari kolom currency kalau tanpa dompet
	AmountMinor  int64  // sisi sumber, sudah termasuk biaya admin
	Category     string
	ToWalletID   int64
	ToWalletName string
	ToWalletCur  string
	AmountInMino int64
	AdminFee     int64
	PartyID      int64
	PartyName    string
	Note         string
	CreatedBy    string
	CreatedAt    time.Time
}

// Jenis hutang piutang. debt_* menyangkut kewajiban kita, loan_* menyangkut
// tagihan kita kepada orang lain.
var kindHutangPiutang = map[string]bool{
	"debt_in": true, "debt_pay": true, "loan_out": true, "loan_in": true,
}

// kindMenambahSaldo: jenis yang membuat saldo dompet bertambah. Dipakai
// walletSelect juga; keduanya harus selalu sepakat, karena jenis yang terlewat
// di salah satunya membuat saldo bohong tanpa error apa pun.
var kindMenambahSaldo = map[string]bool{
	"income": true, "debt_in": true, "loan_in": true,
}

func (t Tx) IsDebt() bool      { return kindHutangPiutang[t.Kind] }
func (t Tx) HasWallet() bool   { return t.WalletID != 0 }
func (t Tx) AddsBalance() bool { return kindMenambahSaldo[t.Kind] }

func (t Tx) IsTransfer() bool   { return t.Kind == "transfer" }
func (t Tx) IsCrossCur() bool   { return t.IsTransfer() && t.WalletCur != t.ToWalletCur }
func (t Tx) NetOutMinor() int64 { return t.AmountMinor - t.AdminFee }

// Rate: kurs efektif transfer, diturunkan dari nominal tersimpan.
func (t Tx) Rate() Rate {
	if !t.IsCrossCur() {
		return Rate{}
	}
	return EffectiveRate(t.AmountMinor, t.AdminFee, t.AmountInMino, t.WalletCur, t.ToWalletCur)
}

// currencyKolom: mata uang hanya disimpan sendiri saat transaksi tidak punya
// dompet. Kalau dompetnya ada, mata uangnya diturunkan dari dompet itu, dan
// menyimpannya dua kali membuka peluang keduanya berbeda.
func (t Tx) currencyKolom() string {
	if t.WalletID != 0 {
		return ""
	}
	return t.WalletCur
}

// Sama seperti walletSelect: penyaring keluarga menempel di potongannya,
// pemanggil menambah syarat lain dengan AND mulai dari $2.
const txSelect = `
SELECT t.id, t.kind, t.occurred_on,
       COALESCE(t.wallet_id, 0), COALESCE(w.name, ''), COALESCE(w.currency, t.currency, ''),
       t.amount_minor, COALESCE(t.category, ''),
       COALESCE(t.to_wallet_id, 0), COALESCE(w2.name, ''), COALESCE(w2.currency, ''),
       COALESCE(t.amount_in_minor, 0), t.admin_fee_minor,
       COALESCE(t.party_id, 0), COALESCE(p.name, ''),
       t.note, COALESCE(u.name, ''), t.created_at
FROM transactions t
LEFT JOIN wallets w ON w.id = t.wallet_id
LEFT JOIN wallets w2 ON w2.id = t.to_wallet_id
LEFT JOIN parties p ON p.id = t.party_id
LEFT JOIN users u ON u.id = t.created_by
WHERE t.family_id = $1`

func scanTxs(rows pgx.Rows) ([]Tx, error) {
	defer rows.Close()
	var out []Tx
	for rows.Next() {
		var t Tx
		if err := rows.Scan(&t.ID, &t.Kind, &t.Date, &t.WalletID, &t.WalletName, &t.WalletCur,
			&t.AmountMinor, &t.Category, &t.ToWalletID, &t.ToWalletName, &t.ToWalletCur,
			&t.AmountInMino, &t.AdminFee, &t.PartyID, &t.PartyName,
			&t.Note, &t.CreatedBy, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// TxFilter menyaring daftar transaksi. Setiap ruas yang dibiarkan kosong
// berarti tidak menyaring apa-apa di sisi itu.
type TxFilter struct {
	// Kinds kosong berarti semua jenis. Category kosong berarti semua kategori;
	// transfer dan hutang piutang tidak punya kategori, jadi menyaringnya dengan
	// sendirinya menyisakan pengeluaran dan pemasukan saja.
	Kinds    []string
	Category string
	// From inklusif, To eksklusif. Keduanya nol berarti seluruh waktu.
	From, To time.Time
	// Cari dicocokkan ke catatan, kategori, nama pihak, dan nama dompet —
	// keempat tempat nama sebuah transaksi bisa diingat kembali.
	Cari string
	// Limit 0 berarti tanpa batas.
	Limit int
}

func (s *Store) Transactions(ctx context.Context, familyID int64, f TxFilter) ([]Tx, error) {
	q := txSelect + ` AND (cardinality($2::text[]) = 0 OR t.kind = ANY($2))
	                  AND ($3 = '' OR t.category = $3)
	                  AND ($4::date IS NULL OR t.occurred_on >= $4)
	                  AND ($5::date IS NULL OR t.occurred_on < $5)
	                  AND ($6 = '' OR t.note ILIKE '%' || $6 || '%'
	                              OR t.category ILIKE '%' || $6 || '%'
	                              OR p.name ILIKE '%' || $6 || '%'
	                              OR w.name ILIKE '%' || $6 || '%')
	                  ORDER BY t.occurred_on DESC, t.id DESC`
	kinds := f.Kinds
	if kinds == nil {
		kinds = []string{}
	}
	// Tanggal nol dikirim sebagai NULL, bukan sebagai 1 Januari tahun 1:
	// yang kedua tetap menyaring, hanya dengan batas yang kebetulan longgar.
	var from, to any
	if !f.From.IsZero() {
		from = f.From
	}
	if !f.To.IsZero() {
		to = f.To
	}
	args := []any{familyID, kinds, f.Category, from, to, f.Cari}
	if f.Limit > 0 {
		q += " LIMIT $7"
		args = append(args, f.Limit)
	}
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return scanTxs(rows)
}

func (s *Store) Transaction(ctx context.Context, familyID, id int64) (Tx, error) {
	rows, err := s.db.Query(ctx, txSelect+" AND t.id = $2", familyID, id)
	if err != nil {
		return Tx{}, err
	}
	ts, err := scanTxs(rows)
	if err != nil {
		return Tx{}, err
	}
	if len(ts) == 0 {
		return Tx{}, ErrNotFound
	}
	return ts[0], nil
}

func (s *Store) CreateTx(ctx context.Context, familyID int64, t Tx, userID int64) (int64, error) {
	var id int64
	err := s.db.QueryRow(ctx,
		`INSERT INTO transactions
		   (family_id, kind, occurred_on, wallet_id, currency, amount_minor, category,
		    to_wallet_id, amount_in_minor, admin_fee_minor, party_id, note, created_by)
		 VALUES ($1, $2, $3, NULLIF($4, 0)::BIGINT, NULLIF($5, '')::CHAR(3), $6, NULLIF($7, ''),
		         NULLIF($8, 0)::BIGINT, NULLIF($9, 0)::BIGINT, $10, NULLIF($11, 0)::BIGINT, $12, $13)
		 RETURNING id`,
		familyID, t.Kind, t.Date, t.WalletID, t.currencyKolom(), t.AmountMinor, t.Category,
		t.ToWalletID, t.AmountInMino, t.AdminFee, t.PartyID, t.Note, userID).Scan(&id)
	return id, err
}

func (s *Store) UpdateTx(ctx context.Context, familyID int64, t Tx) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE transactions SET occurred_on = $1, wallet_id = NULLIF($2, 0)::BIGINT,
		        currency = NULLIF($3, '')::CHAR(3), amount_minor = $4,
		        category = NULLIF($5, ''), to_wallet_id = NULLIF($6, 0)::BIGINT,
		        amount_in_minor = NULLIF($7, 0)::BIGINT, admin_fee_minor = $8,
		        party_id = NULLIF($9, 0)::BIGINT, note = $10
		 WHERE id = $11 AND kind = $12 AND family_id = $13`,
		t.Date, t.WalletID, t.currencyKolom(), t.AmountMinor, t.Category, t.ToWalletID,
		t.AmountInMino, t.AdminFee, t.PartyID, t.Note, t.ID, t.Kind, familyID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) DeleteTx(ctx context.Context, familyID, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM transactions WHERE id = $1 AND family_id = $2`, id, familyID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// ---------- Kategori ----------

type Category struct {
	ID    int64
	Kind  string // expense | income
	Name  string
	Usage int // jumlah transaksi yang memakainya
}

// Categories mengembalikan kategori beserta jumlah pemakaiannya. kind kosong
// berarti semua jenis.
//
// Jumlah pemakaian dihitung lewat satu agregat yang di-join, bukan subquery
// berkorelasi per baris: bentuk yang kedua menjalankan satu query terpisah untuk
// setiap kategori, sehingga membuka halaman Kategori berarti belasan pemindaian
// tabel transaksi sekaligus.
func (s *Store) Categories(ctx context.Context, familyID int64, kind string) ([]Category, error) {
	rows, err := s.db.Query(ctx, `
		SELECT c.id, c.kind, c.name, COALESCE(p.jumlah, 0)
		FROM categories c
		LEFT JOIN (
			SELECT t.kind, t.category, count(*) AS jumlah
			FROM transactions t
			WHERE t.family_id = $1
			GROUP BY t.kind, t.category
		) p ON p.kind = c.kind AND p.category = c.name
		WHERE c.family_id = $1 AND ($2 = '' OR c.kind = $2)
		ORDER BY c.kind, c.name`, familyID, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Category
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.Kind, &c.Name, &c.Usage); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CategoryNames: nama saja, untuk mengisi pilihan di form transaksi.
func (s *Store) CategoryNames(ctx context.Context, familyID int64, kind string) ([]string, error) {
	cats, err := s.Categories(ctx, familyID, kind)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(cats))
	for i, c := range cats {
		out[i] = c.Name
	}
	return out, nil
}

func (s *Store) Category(ctx context.Context, familyID, id int64) (Category, error) {
	var c Category
	err := s.db.QueryRow(ctx,
		`SELECT id, kind, name FROM categories WHERE id = $1 AND family_id = $2`,
		id, familyID).Scan(&c.ID, &c.Kind, &c.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return Category{}, ErrNotFound
	}
	return c, err
}

func (s *Store) CreateCategory(ctx context.Context, familyID int64, kind, name string) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO categories (family_id, kind, name) VALUES ($1, $2, $3)`, familyID, kind, name)
	return err
}

// RenameCategory mengganti nama kategori sekaligus semua transaksi yang
// memakainya, dalam satu transaksi database. Kalau hanya salah satu yang
// berubah, transaksi lama akan menggantung tanpa kategori yang cocok.
func (s *Store) RenameCategory(ctx context.Context, familyID, id int64, name string) error {
	old, err := s.Category(ctx, familyID, id)
	if err != nil {
		return err
	}
	if old.Name == name {
		return nil
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE categories SET name = $1 WHERE id = $2 AND family_id = $3`,
		name, id, familyID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE transactions SET category = $1
		  WHERE family_id = $2 AND kind = $3 AND category = $4`,
		name, familyID, old.Kind, old.Name); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DeleteCategory menolak menghapus kategori yang masih dipakai: transaksinya
// akan kehilangan label tanpa jejak, dan ringkasan per kategori ikut pincang.
func (s *Store) DeleteCategory(ctx context.Context, familyID, id int64) error {
	c, err := s.Category(ctx, familyID, id)
	if err != nil {
		return err
	}
	var n int
	if err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM transactions
		  WHERE family_id = $1 AND kind = $2 AND category = $3`,
		familyID, c.Kind, c.Name).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("kategori %q masih dipakai %d transaksi, ubah kategori transaksinya dulu", c.Name, n)
	}
	_, err = s.db.Exec(ctx, `DELETE FROM categories WHERE id = $1 AND family_id = $2`, id, familyID)
	return err
}

// CategorySpend menjumlahkan pengeluaran dan pemasukan per kategori dalam satu
// rentang tanggal. Nominalnya dikembalikan per mata uang dompet, karena
// konversi ke mata uang dasar butuh kurs yang bukan urusan lapisan ini.
type CategorySpend struct {
	Kind     string
	Category string
	Currency string
	Minor    int64
}

func (s *Store) CategorySpending(ctx context.Context, familyID int64, from, to time.Time) ([]CategorySpend, error) {
	rows, err := s.db.Query(ctx, `
		SELECT t.kind, t.category, w.currency, SUM(t.amount_minor)
		FROM transactions t
		JOIN wallets w ON w.id = t.wallet_id
		WHERE t.family_id = $1 AND t.kind IN ('expense', 'income')
		  AND t.occurred_on >= $2 AND t.occurred_on < $3
		GROUP BY t.kind, t.category, w.currency`, familyID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CategorySpend
	for rows.Next() {
		var c CategorySpend
		if err := rows.Scan(&c.Kind, &c.Category, &c.Currency, &c.Minor); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---------- Kurs ----------

// Rates mengembalikan kurs terakhir untuk tiap pasangan mata uang, diambil
// dari transfer lintas mata uang yang sudah tercatat. Ini sekaligus jadi
// "kurs default penyedia" di form transfer dan dasar konversi total di
// dashboard — tanpa API kurs eksternal.
// ponytail: kalau nanti butuh kurs harian tanpa harus transfer dulu, tambahkan
// tabel rates dan pakai nilai itu sebagai fallback di sini saja.
func (s *Store) Rates(ctx context.Context, familyID int64) (map[string]Rate, error) {
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT ON (w.currency, w2.currency)
		       w.currency, w2.currency, t.amount_minor - t.admin_fee_minor, t.amount_in_minor
		FROM transactions t
		JOIN wallets w ON w.id = t.wallet_id
		JOIN wallets w2 ON w2.id = t.to_wallet_id
		WHERE t.family_id = $1 AND t.kind = 'transfer' AND w.currency <> w2.currency
		  AND t.amount_minor > t.admin_fee_minor
		ORDER BY w.currency, w2.currency, t.occurred_on DESC, t.id DESC`, familyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]Rate{}
	for rows.Next() {
		var from, to string
		var fromMinor, toMinor int64
		if err := rows.Scan(&from, &to, &fromMinor, &toMinor); err != nil {
			return nil, err
		}
		r := Rate{FromMinor: fromMinor, From: from, ToMinor: toMinor, To: to}
		out[from+">"+to] = r
		// Arah sebaliknya hanya dipakai kalau belum ada catatan aslinya.
		if _, ok := out[to+">"+from]; !ok {
			out[to+">"+from] = r.Invert()
		}
	}
	return out, rows.Err()
}

// ---------- User & sesi ----------

type User struct {
	ID         int64
	Name       string
	FamilyID   int64
	FamilyName string
	// Kepala: anggota pertama keluarga ini, yaitu yang dibuat bersama
	// keluarganya lewat API admin. Dialah satu-satunya yang boleh menonaktifkan
	// anggota lain. Perannya diturunkan dari urutan pendaftaran, bukan disimpan
	// sebagai kolom sendiri: dengan begitu tidak ada keluarga yang bisa
	// kehilangan kepalanya karena satu baris data salah ubah.
	Kepala bool
	// Disabled: aksesnya sudah dicabut. Sesi yang sudah berjalan ikut mati
	// karena SessionUser menyaringnya.
	Disabled bool
}

// Member: satu anggota beserta jejaknya, untuk halaman pengaturan.
type Member struct {
	User
	CreatedAt time.Time
	// Txs: jumlah transaksi yang pernah dicatatnya. Ditampilkan supaya jelas
	// bahwa menonaktifkan anggota tidak menghapus apa pun yang sudah dicatat.
	Txs int
}

// kepalaKeluarga: potongan yang menandai anggota pertama sebuah keluarga.
const kepalaKeluarga = `u.id = (SELECT min(id) FROM users WHERE family_id = u.family_id)`

// Members mengurut sesuai urutan pendaftaran, jadi kepala keluarga selalu di
// atas dan anggota yang baru masuk selalu di bawah.
func (s *Store) Members(ctx context.Context, familyID int64) ([]Member, error) {
	rows, err := s.db.Query(ctx, `
		SELECT u.id, u.name, u.created_at, u.disabled_at IS NOT NULL, `+kepalaKeluarga+`,
		       (SELECT count(*) FROM transactions t
		        WHERE t.family_id = u.family_id AND t.created_by = u.id)
		FROM users u WHERE u.family_id = $1 ORDER BY u.id`, familyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Member
	for rows.Next() {
		m := Member{User: User{FamilyID: familyID}}
		if err := rows.Scan(&m.ID, &m.Name, &m.CreatedAt, &m.Disabled, &m.Kepala, &m.Txs); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// PasswordHash dipakai untuk memastikan yang mengganti sandi memang tahu sandi
// lamanya. Tanpa itu, perangkat yang tertinggal dalam keadaan login bisa
// mengunci pemiliknya sendiri keluar dari akunnya.
func (s *Store) PasswordHash(ctx context.Context, familyID, id int64) (string, error) {
	var hash string
	err := s.db.QueryRow(ctx,
		`SELECT password_hash FROM users WHERE id = $1 AND family_id = $2`, id, familyID).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return hash, err
}

func (s *Store) SetPassword(ctx context.Context, familyID, id int64, hash string) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE users SET password_hash = $1 WHERE id = $2 AND family_id = $3`, hash, id, familyID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// SetMemberActive mencabut atau memulihkan akses seorang anggota. Kepala
// keluarga dikecualikan di query-nya sendiri, bukan hanya di handler: keluarga
// yang kepalanya nonaktif tidak punya siapa pun yang bisa memulihkannya lagi.
func (s *Store) SetMemberActive(ctx context.Context, familyID, id int64, aktif bool) error {
	var waktu any
	if !aktif {
		waktu = time.Now()
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE users u SET disabled_at = $1
		WHERE u.id = $2 AND u.family_id = $3 AND NOT (`+kepalaKeluarga+`)`, waktu, id, familyID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// DeleteUserSessions memutus seluruh sesi seorang anggota di semua perangkat.
// Dipanggil setelah sandi diganti dan setelah akses dicabut — tanpa ini,
// keduanya baru benar-benar berlaku sebulan kemudian saat sesi lamanya habis.
func (s *Store) DeleteUserSessions(ctx context.Context, familyID, userID int64) error {
	_, err := s.db.Exec(ctx, `
		DELETE FROM sessions s USING users u
		WHERE s.user_id = u.id AND u.id = $1 AND u.family_id = $2`, userID, familyID)
	return err
}

func (s *Store) CreateUser(ctx context.Context, familyID int64, name, hash string) (int64, error) {
	var id int64
	err := s.db.QueryRow(ctx,
		`INSERT INTO users (family_id, name, password_hash) VALUES ($1, $2, $3) RETURNING id`,
		familyID, name, hash).Scan(&id)
	return id, err
}

// UserByName mencari anggota di dalam satu keluarga. Nama hanya unik per
// keluarga, jadi pencarian tanpa familyID akan mencocokkan orang yang salah.
func (s *Store) UserByName(ctx context.Context, familyID int64, name string) (User, string, error) {
	var u User
	var hash string
	err := s.db.QueryRow(ctx, `
		SELECT u.id, u.name, u.family_id, f.name, u.password_hash,
		       u.disabled_at IS NOT NULL, `+kepalaKeluarga+`
		FROM users u JOIN families f ON f.id = u.family_id
		WHERE u.family_id = $1 AND lower(u.name) = lower($2)`, familyID, name).
		Scan(&u.ID, &u.Name, &u.FamilyID, &u.FamilyName, &hash, &u.Disabled, &u.Kepala)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, "", ErrNotFound
	}
	return u, hash, err
}

func (s *Store) UserCount(ctx context.Context, familyID int64) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `SELECT count(*) FROM users WHERE family_id = $1`, familyID).Scan(&n)
	return n, err
}

func (s *Store) CreateSession(ctx context.Context, token string, userID int64, until time.Time) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO sessions (token, user_id, expires_at) VALUES ($1, $2, $3)`, token, userID, until)
	return err
}

// SessionUser mengembalikan anggota beserta keluarganya. Dari sinilah familyID
// yang dipakai seluruh handler berasal — tidak pernah dari parameter URL atau
// isian form, yang bisa dikarang siapa saja.
//
// Anggota nonaktif disaring di sini, bukan di tiap handler. Setiap permintaan
// melewati satu query ini, jadi mencabut akses langsung berlaku di semua
// perangkatnya pada permintaan berikutnya — bukan sebulan lagi saat sesinya
// kedaluwarsa sendiri.
func (s *Store) SessionUser(ctx context.Context, token string) (User, error) {
	var u User
	err := s.db.QueryRow(ctx, `
		SELECT u.id, u.name, u.family_id, f.name, `+kepalaKeluarga+`
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		JOIN families f ON f.id = u.family_id
		WHERE s.token = $1 AND s.expires_at > now() AND u.disabled_at IS NULL`, token).
		Scan(&u.ID, &u.Name, &u.FamilyID, &u.FamilyName, &u.Kepala)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM sessions WHERE token = $1`, token)
	return err
}

func (s *Store) PurgeSessions(ctx context.Context) error {
	_, err := s.db.Exec(ctx, `DELETE FROM sessions WHERE expires_at < now()`)
	return err
}

// ---------- Keluarga ----------

type Family struct {
	ID         int64
	Name       string
	SignupCode string
	Members    int
	Wallets    int
	Txs        int
	CreatedAt  time.Time
}

// FamilyByCode dipakai saat login dan pendaftaran: kode undangan yang
// menentukan keluarga mana yang dimaksud.
func (s *Store) FamilyByCode(ctx context.Context, code string) (Family, error) {
	var f Family
	err := s.db.QueryRow(ctx,
		`SELECT id, name, signup_code FROM families WHERE signup_code = $1`, code).
		Scan(&f.ID, &f.Name, &f.SignupCode)
	if errors.Is(err, pgx.ErrNoRows) {
		return Family{}, ErrNotFound
	}
	return f, err
}

func (s *Store) Families(ctx context.Context) ([]Family, error) {
	rows, err := s.db.Query(ctx, `
		SELECT f.id, f.name, f.signup_code, f.created_at,
		       (SELECT count(*) FROM users u WHERE u.family_id = f.id),
		       (SELECT count(*) FROM wallets w WHERE w.family_id = f.id),
		       (SELECT count(*) FROM transactions t WHERE t.family_id = f.id)
		FROM families f ORDER BY f.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Family
	for rows.Next() {
		var f Family
		if err := rows.Scan(&f.ID, &f.Name, &f.SignupCode, &f.CreatedAt,
			&f.Members, &f.Wallets, &f.Txs); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// CreateFamily membuat keluarga beserta kepala keluarganya dan kategori
// bawaannya dalam satu transaksi. Keluarga tanpa anggota tidak ada gunanya, dan
// keluarga tanpa kategori membuat form transaksi pertama buntu.
func (s *Store) CreateFamily(ctx context.Context, name, code, headName, headHash string) (Family, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Family{}, err
	}
	defer tx.Rollback(ctx)

	var f Family
	if err := tx.QueryRow(ctx,
		`INSERT INTO families (name, signup_code) VALUES ($1, $2)
		 RETURNING id, name, signup_code, created_at`, name, code).
		Scan(&f.ID, &f.Name, &f.SignupCode, &f.CreatedAt); err != nil {
		return Family{}, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO users (family_id, name, password_hash) VALUES ($1, $2, $3)`,
		f.ID, headName, headHash); err != nil {
		return Family{}, err
	}
	if _, err := tx.Exec(ctx, seedCategoriesSQL, f.ID); err != nil {
		return Family{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Family{}, err
	}
	f.Members = 1
	return f, nil
}

// seedCategoriesSQL memasang kategori bawaan untuk keluarga baru. Daftarnya
// sama dengan yang dipasang migrasi 001 untuk keluarga pertama.
//
// $1 harus di-cast eksplisit: di dalam UNION, Postgres tidak bisa menyimpulkan
// tipe parameter dari kolom tujuannya dan menganggapnya text.
const seedCategoriesSQL = `
INSERT INTO categories (family_id, kind, name)
SELECT $1::bigint, 'expense', name
FROM unnest(ARRAY['Belanja', 'Tagihan', 'Transportasi', 'Makanan', 'Kesehatan', 'Lainnya']) AS name
UNION ALL
SELECT $1::bigint, 'income', name
FROM unnest(ARRAY['Gaji', 'Bonus', 'Hadiah', 'Investasi', 'Lainnya']) AS name`

// UpdateFamily mengganti nama dan/atau kode undangan. Argumen kosong berarti
// tidak diubah, supaya pemanggil bisa memutar kode tanpa menyentuh namanya.
func (s *Store) UpdateFamily(ctx context.Context, id int64, name, code string) (Family, error) {
	tag, err := s.db.Exec(ctx,
		`UPDATE families SET name = COALESCE(NULLIF($1, ''), name),
		        signup_code = COALESCE(NULLIF($2, ''), signup_code)
		 WHERE id = $3`, name, code, id)
	if err != nil {
		return Family{}, err
	}
	if tag.RowsAffected() == 0 {
		return Family{}, ErrNotFound
	}
	var f Family
	err = s.db.QueryRow(ctx,
		`SELECT id, name, signup_code, created_at FROM families WHERE id = $1`, id).
		Scan(&f.ID, &f.Name, &f.SignupCode, &f.CreatedAt)
	return f, err
}

// ---------- Pihak, hutang, dan piutang ----------

type Party struct {
	ID   int64
	Name string
	Note string
	// Saldo per mata uang. Hutang berarti kita yang berkewajiban, piutang
	// berarti pihak itu yang berkewajiban kepada kita.
	Saldo []PartyBalance
}

type PartyBalance struct {
	Currency     string
	HutangMinor  int64 // debt_in dikurangi debt_pay
	PiutangMinor int64 // loan_out dikurangi loan_in
}

func (b PartyBalance) NetMinor() int64 { return b.PiutangMinor - b.HutangMinor }

// partyBalanceSQL menjumlahkan hutang dan piutang per pihak per mata uang.
// Mata uangnya diambil dari dompet kalau ada, kalau tidak dari kolom currency
// pada transaksinya sendiri — hutang tanpa dompet tetap punya mata uang.
const partyBalanceSQL = `
SELECT t.party_id, COALESCE(w.currency, t.currency) AS cur,
       SUM(CASE t.kind WHEN 'debt_in' THEN t.amount_minor
                       WHEN 'debt_pay' THEN -t.amount_minor ELSE 0 END) AS hutang,
       SUM(CASE t.kind WHEN 'loan_out' THEN t.amount_minor
                       WHEN 'loan_in' THEN -t.amount_minor ELSE 0 END) AS piutang
FROM transactions t
LEFT JOIN wallets w ON w.id = t.wallet_id
WHERE t.family_id = $1 AND t.party_id IS NOT NULL
GROUP BY t.party_id, COALESCE(w.currency, t.currency)`

// Parties mengembalikan seluruh pihak beserta saldo hutang piutangnya.
// Pihak yang saldonya sudah nol tetap ditampilkan: riwayatnya masih berguna,
// dan menghilangkannya membuat user mengira catatannya hilang.
func (s *Store) Parties(ctx context.Context, familyID int64) ([]Party, error) {
	rows, err := s.db.Query(ctx, `
		SELECT p.id, p.name, p.note, b.cur, b.hutang, b.piutang
		FROM parties p
		LEFT JOIN (`+partyBalanceSQL+`) b ON b.party_id = p.id
		WHERE p.family_id = $1
		ORDER BY p.name, b.cur`, familyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Party
	for rows.Next() {
		var (
			id            int64
			name, note    string
			cur           *string
			hutang, piutg *int64
		)
		if err := rows.Scan(&id, &name, &note, &cur, &hutang, &piutg); err != nil {
			return nil, err
		}
		if n := len(out); n == 0 || out[n-1].ID != id {
			out = append(out, Party{ID: id, Name: name, Note: note})
		}
		if cur != nil && (*hutang != 0 || *piutg != 0) {
			p := &out[len(out)-1]
			p.Saldo = append(p.Saldo, PartyBalance{
				Currency: *cur, HutangMinor: *hutang, PiutangMinor: *piutg,
			})
		}
	}
	return out, rows.Err()
}

func (s *Store) Party(ctx context.Context, familyID, id int64) (Party, error) {
	parties, err := s.Parties(ctx, familyID)
	if err != nil {
		return Party{}, err
	}
	for _, p := range parties {
		if p.ID == id {
			return p, nil
		}
	}
	return Party{}, ErrNotFound
}

func (s *Store) PartyByName(ctx context.Context, familyID int64, name string) (Party, error) {
	var p Party
	err := s.db.QueryRow(ctx,
		`SELECT id, name, note FROM parties WHERE family_id = $1 AND lower(name) = lower($2)`,
		familyID, name).Scan(&p.ID, &p.Name, &p.Note)
	if errors.Is(err, pgx.ErrNoRows) {
		return Party{}, ErrNotFound
	}
	return p, err
}

// EnsureParty mencari pihak dengan nama itu, membuatnya kalau belum ada.
// Pencatatan hutang jadi bisa dilakukan dalam satu langkah tanpa memaksa user
// mendaftarkan pihaknya lebih dulu.
func (s *Store) EnsureParty(ctx context.Context, familyID int64, name string) (int64, error) {
	if p, err := s.PartyByName(ctx, familyID, name); err == nil {
		return p.ID, nil
	} else if !errors.Is(err, ErrNotFound) {
		return 0, err
	}
	var id int64
	err := s.db.QueryRow(ctx,
		`INSERT INTO parties (family_id, name) VALUES ($1, $2) RETURNING id`,
		familyID, name).Scan(&id)
	return id, err
}

func (s *Store) UpdateParty(ctx context.Context, familyID, id int64, name, note string) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE parties SET name = $1, note = $2 WHERE id = $3 AND family_id = $4`,
		name, note, id, familyID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// DeleteParty menolak menghapus pihak yang masih punya catatan transaksi.
// Menghapusnya akan membuat riwayat hutang menggantung tanpa lawan transaksi.
func (s *Store) DeleteParty(ctx context.Context, familyID, id int64) error {
	var n int
	if err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM transactions WHERE family_id = $1 AND party_id = $2`,
		familyID, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("pihak ini masih punya %d catatan, hapus catatannya dulu", n)
	}
	tag, err := s.db.Exec(ctx, `DELETE FROM parties WHERE id = $1 AND family_id = $2`, id, familyID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// PartyTxs: riwayat hutang piutang satu pihak, terbaru dulu.
func (s *Store) PartyTxs(ctx context.Context, familyID, partyID int64) ([]Tx, error) {
	rows, err := s.db.Query(ctx,
		txSelect+" AND t.party_id = $2 ORDER BY t.occurred_on DESC, t.id DESC",
		familyID, partyID)
	if err != nil {
		return nil, err
	}
	return scanTxs(rows)
}

// ---------- Kartu kredit ----------

// CardStatus: keadaan satu kartu kredit pada satu tanggal.
type CardStatus struct {
	Wallet Wallet
	// PayableMinor: tagihan yang sudah tercetak dan harus dibayar sebelum
	// jatuh tempo, sudah dikurangi pembayaran yang masuk setelah tanggal cetak.
	PayableMinor int64
	// OutstandingMinor: seluruh pemakaian sampai hari ini, termasuk belanja
	// yang belum masuk tagihan mana pun.
	OutstandingMinor int64
	// HasCycle: siklus tagihannya sudah diisi. Tanpa itu Settlement, Due, dan
	// PayableMinor tidak punya arti dan tidak boleh ditampilkan.
	HasCycle   bool
	Settlement time.Time // tanggal cetak terakhir
	Due        time.Time // jatuh tempo tagihan itu
}

// CardBalances mengambil dua angka mentah yang dibutuhkan CardStatus:
// saldo kartu pada tanggal cetak terakhir, dan pembayaran yang masuk sesudahnya.
func (s *Store) CardBalances(ctx context.Context, familyID, walletID int64, settlement time.Time) (atSettlement, creditsSince int64, err error) {
	err = s.db.QueryRow(ctx, `
		SELECT
		  (SELECT w.initial_balance_minor FROM wallets w
		    WHERE w.id = $2 AND w.family_id = $1)
		  + COALESCE((SELECT SUM(CASE WHEN t.kind IN ('income', 'debt_in', 'loan_in')
		                              THEN t.amount_minor ELSE -t.amount_minor END)
		              FROM transactions t
		              WHERE t.family_id = $1 AND t.wallet_id = $2 AND t.occurred_on <= $3), 0)
		  + COALESCE((SELECT SUM(t.amount_in_minor) FROM transactions t
		              WHERE t.family_id = $1 AND t.to_wallet_id = $2 AND t.occurred_on <= $3), 0),
		  COALESCE((SELECT SUM(t.amount_in_minor) FROM transactions t
		            WHERE t.family_id = $1 AND t.to_wallet_id = $2 AND t.occurred_on > $3), 0)
		  + COALESCE((SELECT SUM(t.amount_minor) FROM transactions t
		             WHERE t.family_id = $1 AND t.wallet_id = $2 AND t.occurred_on > $3
		               AND t.kind IN ('income', 'debt_in', 'loan_in')), 0)`,
		familyID, walletID, settlement).Scan(&atSettlement, &creditsSince)
	return atSettlement, creditsSince, err
}
