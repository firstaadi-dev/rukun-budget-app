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
}

func (w Wallet) IsCredit() bool { return w.Type == "credit" }

var walletTypeLabel = map[string]string{
	"cash": "Tunai", "bank": "Bank", "credit": "Kartu Kredit", "ewallet": "E-Wallet",
}

func (w Wallet) TypeLabel() string { return walletTypeLabel[w.Type] }

// Subtitle: "BCA · Bank" untuk bank/kartu kredit, "GoPay" untuk e-wallet.
func (w Wallet) Subtitle() string {
	if w.Provider == "" {
		return w.TypeLabel()
	}
	if w.Type == "bank" || w.Type == "credit" {
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
       w.initial_balance_minor
       + COALESCE((SELECT SUM(CASE WHEN t.kind = 'income' THEN t.amount_minor ELSE -t.amount_minor END)
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
		if err := rows.Scan(&w.ID, &w.Name, &w.Type, &w.Provider, &w.Currency, &w.InitialMinor, &w.BalanceMinor); err != nil {
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
		`INSERT INTO wallets (family_id, name, type, provider, currency, initial_balance_minor)
		 VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6) RETURNING id`,
		familyID, w.Name, w.Type, w.Provider, w.Currency, w.InitialMinor).Scan(&id)
	return id, err
}

func (s *Store) UpdateWallet(ctx context.Context, familyID int64, w Wallet) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE wallets SET name = $1, type = $2, provider = NULLIF($3, ''),
		        currency = $4, initial_balance_minor = $5
		 WHERE id = $6 AND family_id = $7`,
		w.Name, w.Type, w.Provider, w.Currency, w.InitialMinor, w.ID, familyID)
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
	ID           int64
	Kind         string // expense | income | transfer
	Date         time.Time
	WalletID     int64
	WalletName   string
	WalletCur    string
	AmountMinor  int64 // sisi sumber, sudah termasuk biaya admin
	Category     string
	ToWalletID   int64
	ToWalletName string
	ToWalletCur  string
	AmountInMino int64
	AdminFee     int64
	Note         string
	CreatedBy    string
	CreatedAt    time.Time
}

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

// Sama seperti walletSelect: penyaring keluarga menempel di potongannya,
// pemanggil menambah syarat lain dengan AND mulai dari $2.
const txSelect = `
SELECT t.id, t.kind, t.occurred_on, t.wallet_id, w.name, w.currency,
       t.amount_minor, COALESCE(t.category, ''),
       COALESCE(t.to_wallet_id, 0), COALESCE(w2.name, ''), COALESCE(w2.currency, ''),
       COALESCE(t.amount_in_minor, 0), t.admin_fee_minor, t.note,
       COALESCE(u.name, ''), t.created_at
FROM transactions t
JOIN wallets w ON w.id = t.wallet_id
LEFT JOIN wallets w2 ON w2.id = t.to_wallet_id
LEFT JOIN users u ON u.id = t.created_by
WHERE t.family_id = $1`

func scanTxs(rows pgx.Rows) ([]Tx, error) {
	defer rows.Close()
	var out []Tx
	for rows.Next() {
		var t Tx
		if err := rows.Scan(&t.ID, &t.Kind, &t.Date, &t.WalletID, &t.WalletName, &t.WalletCur,
			&t.AmountMinor, &t.Category, &t.ToWalletID, &t.ToWalletName, &t.ToWalletCur,
			&t.AmountInMino, &t.AdminFee, &t.Note, &t.CreatedBy, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Transactions: kind dan category kosong berarti semua. limit 0 berarti tanpa
// batas. Transfer tidak punya kategori, jadi menyaring per kategori dengan
// sendirinya menyisakan pengeluaran dan pemasukan saja.
func (s *Store) Transactions(ctx context.Context, familyID int64, kind, category string, limit int) ([]Tx, error) {
	q := txSelect + ` AND ($2 = '' OR t.kind = $2) AND ($3 = '' OR t.category = $3)
	                  ORDER BY t.occurred_on DESC, t.id DESC`
	args := []any{familyID, kind, category}
	if limit > 0 {
		q += " LIMIT $4"
		args = append(args, limit)
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
		   (family_id, kind, occurred_on, wallet_id, amount_minor, category,
		    to_wallet_id, amount_in_minor, admin_fee_minor, note, created_by)
		 VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), NULLIF($7, 0)::BIGINT, NULLIF($8, 0)::BIGINT, $9, $10, $11)
		 RETURNING id`,
		familyID, t.Kind, t.Date, t.WalletID, t.AmountMinor, t.Category,
		t.ToWalletID, t.AmountInMino, t.AdminFee, t.Note, userID).Scan(&id)
	return id, err
}

func (s *Store) UpdateTx(ctx context.Context, familyID int64, t Tx) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE transactions SET occurred_on = $1, wallet_id = $2, amount_minor = $3,
		        category = NULLIF($4, ''), to_wallet_id = NULLIF($5, 0)::BIGINT,
		        amount_in_minor = NULLIF($6, 0)::BIGINT, admin_fee_minor = $7, note = $8
		 WHERE id = $9 AND kind = $10 AND family_id = $11`,
		t.Date, t.WalletID, t.AmountMinor, t.Category, t.ToWalletID,
		t.AmountInMino, t.AdminFee, t.Note, t.ID, t.Kind, familyID)
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
func (s *Store) Categories(ctx context.Context, familyID int64, kind string) ([]Category, error) {
	rows, err := s.db.Query(ctx, `
		SELECT c.id, c.kind, c.name,
		       (SELECT count(*) FROM transactions t
		         WHERE t.family_id = c.family_id AND t.kind = c.kind AND t.category = c.name)
		FROM categories c
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
		SELECT u.id, u.name, u.family_id, f.name, u.password_hash
		FROM users u JOIN families f ON f.id = u.family_id
		WHERE u.family_id = $1 AND lower(u.name) = lower($2)`, familyID, name).
		Scan(&u.ID, &u.Name, &u.FamilyID, &u.FamilyName, &hash)
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
func (s *Store) SessionUser(ctx context.Context, token string) (User, error) {
	var u User
	err := s.db.QueryRow(ctx, `
		SELECT u.id, u.name, u.family_id, f.name
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		JOIN families f ON f.id = u.family_id
		WHERE s.token = $1 AND s.expires_at > now()`, token).
		Scan(&u.ID, &u.Name, &u.FamilyID, &u.FamilyName)
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
