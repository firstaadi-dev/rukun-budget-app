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
const walletSelect = `
SELECT w.id, w.name, w.type, COALESCE(w.provider, ''), w.currency, w.initial_balance_minor,
       w.initial_balance_minor
       + COALESCE((SELECT SUM(CASE WHEN t.kind = 'income' THEN t.amount_minor ELSE -t.amount_minor END)
                   FROM transactions t WHERE t.wallet_id = w.id), 0)
       + COALESCE((SELECT SUM(t.amount_in_minor)
                   FROM transactions t WHERE t.to_wallet_id = w.id), 0) AS balance_minor
FROM wallets w`

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

func (s *Store) Wallets(ctx context.Context) ([]Wallet, error) {
	rows, err := s.db.Query(ctx, walletSelect+" ORDER BY w.type, w.id")
	if err != nil {
		return nil, err
	}
	return scanWallets(rows)
}

func (s *Store) Wallet(ctx context.Context, id int64) (Wallet, error) {
	rows, err := s.db.Query(ctx, walletSelect+" WHERE w.id = $1", id)
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

func (s *Store) CreateWallet(ctx context.Context, w Wallet) (int64, error) {
	var id int64
	err := s.db.QueryRow(ctx,
		`INSERT INTO wallets (name, type, provider, currency, initial_balance_minor)
		 VALUES ($1, $2, NULLIF($3, ''), $4, $5) RETURNING id`,
		w.Name, w.Type, w.Provider, w.Currency, w.InitialMinor).Scan(&id)
	return id, err
}

func (s *Store) UpdateWallet(ctx context.Context, w Wallet) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE wallets SET name = $1, type = $2, provider = NULLIF($3, ''),
		        currency = $4, initial_balance_minor = $5 WHERE id = $6`,
		w.Name, w.Type, w.Provider, w.Currency, w.InitialMinor, w.ID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// DeleteWallet menolak menghapus dompet yang masih dipakai transaksi —
// menghapusnya diam-diam akan mengubah saldo dompet lain tanpa jejak.
func (s *Store) DeleteWallet(ctx context.Context, id int64) error {
	var n int
	if err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM transactions WHERE wallet_id = $1 OR to_wallet_id = $1`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("dompet ini masih dipakai %d transaksi, hapus transaksinya dulu", n)
	}
	tag, err := s.db.Exec(ctx, `DELETE FROM wallets WHERE id = $1`, id)
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

const txSelect = `
SELECT t.id, t.kind, t.occurred_on, t.wallet_id, w.name, w.currency,
       t.amount_minor, COALESCE(t.category, ''),
       COALESCE(t.to_wallet_id, 0), COALESCE(w2.name, ''), COALESCE(w2.currency, ''),
       COALESCE(t.amount_in_minor, 0), t.admin_fee_minor, t.note,
       COALESCE(u.name, ''), t.created_at
FROM transactions t
JOIN wallets w ON w.id = t.wallet_id
LEFT JOIN wallets w2 ON w2.id = t.to_wallet_id
LEFT JOIN users u ON u.id = t.created_by`

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

// Transactions: kind kosong berarti semua. limit 0 berarti tanpa batas.
func (s *Store) Transactions(ctx context.Context, kind string, limit int) ([]Tx, error) {
	q := txSelect + ` WHERE ($1 = '' OR t.kind = $1) ORDER BY t.occurred_on DESC, t.id DESC`
	args := []any{kind}
	if limit > 0 {
		q += " LIMIT $2"
		args = append(args, limit)
	}
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return scanTxs(rows)
}

func (s *Store) Transaction(ctx context.Context, id int64) (Tx, error) {
	rows, err := s.db.Query(ctx, txSelect+" WHERE t.id = $1", id)
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

func (s *Store) CreateTx(ctx context.Context, t Tx, userID int64) (int64, error) {
	var id int64
	err := s.db.QueryRow(ctx,
		`INSERT INTO transactions
		   (kind, occurred_on, wallet_id, amount_minor, category,
		    to_wallet_id, amount_in_minor, admin_fee_minor, note, created_by)
		 VALUES ($1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, 0)::BIGINT, NULLIF($7, 0)::BIGINT, $8, $9, $10)
		 RETURNING id`,
		t.Kind, t.Date, t.WalletID, t.AmountMinor, t.Category,
		t.ToWalletID, t.AmountInMino, t.AdminFee, t.Note, userID).Scan(&id)
	return id, err
}

func (s *Store) UpdateTx(ctx context.Context, t Tx) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE transactions SET occurred_on = $1, wallet_id = $2, amount_minor = $3,
		        category = NULLIF($4, ''), to_wallet_id = NULLIF($5, 0)::BIGINT,
		        amount_in_minor = NULLIF($6, 0)::BIGINT, admin_fee_minor = $7, note = $8
		 WHERE id = $9 AND kind = $10`,
		t.Date, t.WalletID, t.AmountMinor, t.Category, t.ToWalletID,
		t.AmountInMino, t.AdminFee, t.Note, t.ID, t.Kind)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) DeleteTx(ctx context.Context, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM transactions WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// ---------- Kurs ----------

// Rates mengembalikan kurs terakhir untuk tiap pasangan mata uang, diambil
// dari transfer lintas mata uang yang sudah tercatat. Ini sekaligus jadi
// "kurs default penyedia" di form transfer dan dasar konversi total di
// dashboard — tanpa API kurs eksternal.
// ponytail: kalau nanti butuh kurs harian tanpa harus transfer dulu, tambahkan
// tabel rates dan pakai nilai itu sebagai fallback di sini saja.
func (s *Store) Rates(ctx context.Context) (map[string]Rate, error) {
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT ON (w.currency, w2.currency)
		       w.currency, w2.currency, t.amount_minor - t.admin_fee_minor, t.amount_in_minor
		FROM transactions t
		JOIN wallets w ON w.id = t.wallet_id
		JOIN wallets w2 ON w2.id = t.to_wallet_id
		WHERE t.kind = 'transfer' AND w.currency <> w2.currency
		  AND t.amount_minor > t.admin_fee_minor
		ORDER BY w.currency, w2.currency, t.occurred_on DESC, t.id DESC`)
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
	ID   int64
	Name string
}

func (s *Store) CreateUser(ctx context.Context, name, hash string) (int64, error) {
	var id int64
	err := s.db.QueryRow(ctx,
		`INSERT INTO users (name, password_hash) VALUES ($1, $2) RETURNING id`, name, hash).Scan(&id)
	return id, err
}

func (s *Store) UserByName(ctx context.Context, name string) (User, string, error) {
	var u User
	var hash string
	err := s.db.QueryRow(ctx,
		`SELECT id, name, password_hash FROM users WHERE lower(name) = lower($1)`, name).
		Scan(&u.ID, &u.Name, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, "", ErrNotFound
	}
	return u, hash, err
}

func (s *Store) UserCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) CreateSession(ctx context.Context, token string, userID int64, until time.Time) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO sessions (token, user_id, expires_at) VALUES ($1, $2, $3)`, token, userID, until)
	return err
}

func (s *Store) SessionUser(ctx context.Context, token string) (User, error) {
	var u User
	err := s.db.QueryRow(ctx,
		`SELECT u.id, u.name FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE s.token = $1 AND s.expires_at > now()`, token).Scan(&u.ID, &u.Name)
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
