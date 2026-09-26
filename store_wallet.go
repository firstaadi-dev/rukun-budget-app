package main

import (
	"context"
	"fmt"
	sqlcdb "github.com/firsta/rukun/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

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
	// CicilanMendatangMinor: angsuran cicilan bertanggal maju yang belum masuk
	// saldo. Saldo berhenti di hari ini, tapi bank menahan limit sebesar harga
	// penuh sejak transaksinya — jadi angka ini ikut dihitung sebagai terpakai.
	CicilanMendatangMinor int64
}

func (w Wallet) HasCycle() bool { return w.SettlementDay > 0 && w.PaymentDay > 0 }
func (w Wallet) HasLimit() bool { return w.LimitMinor > 0 }

// IsCredit: akun yang saldonya bergerak ke arah negatif saat dipakai, dan
// dilunasi belakangan. Kartu kredit dan PayLater berperilaku sama persis di
// seluruh aplikasi — perbedaannya cuma nama dan daftar penyedianya.
func (w Wallet) IsCredit() bool { return w.Type == "credit" || w.Type == "paylater" }

// TerpakaiMinor: berapa yang sedang terpakai dari limit. Saldo akun kredit
// negatif saat dipakai, jadi ini kebalikan tandanya, ditambah sisa angsuran
// cicilan yang belum jatuh tempo.
func (w Wallet) TerpakaiMinor() int64 { return max(0, w.CicilanMendatangMinor-w.BalanceMinor) }

// SisaLimitMinor: berapa lagi yang boleh dipakai.
func (w Wallet) SisaLimitMinor() int64 { return max(0, w.LimitMinor-w.TerpakaiMinor()) }

var walletTypeLabel = map[string]string{
	"cash": "Tunai", "bank": "Bank", "credit": "Kartu Kredit",
	"ewallet": "E-Wallet", "paylater": "PayLater", "broker": "Broker",
}

func (w Wallet) TypeLabel() string { return walletTypeLabel[w.Type] }

// Subtitle: "BCA · Bank" untuk bank/kartu kredit, "GoPay" untuk e-wallet.
func (w Wallet) Subtitle() string {
	if w.Provider == "" {
		return w.TypeLabel()
	}
	if w.Type == "bank" || w.Type == "broker" || w.IsCredit() {
		return w.Provider + " · " + w.TypeLabel()
	}
	return w.Provider
}

// Saldo dompet diturunkan dari transaksi sampai hari ini; query-nya ada di sqlc/queries/wallets.sql.
func (s *Store) Wallets(ctx context.Context, familyID int64) ([]Wallet, error) {
	rows, err := sqlcdb.New(s.db).GetWallets(ctx, sqlcdb.GetWalletsParams{
		Today: pgtype.Date{Time: s.today(), Valid: true}, FamilyID: familyID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Wallet, len(rows))
	for i, row := range rows {
		out[i] = Wallet{ID: row.ID, Name: row.Name, Type: row.Type, Provider: row.Provider,
			Currency: row.Currency, InitialMinor: row.InitialBalanceMinor,
			SettlementDay: int(row.SettlementDay), PaymentDay: int(row.PaymentDay),
			LimitMinor: row.CreditLimitMinor, BalanceMinor: row.BalanceMinor,
			CicilanMendatangMinor: row.CicilanMendatangMinor}
	}
	return out, nil
}

func (s *Store) Wallet(ctx context.Context, familyID, id int64) (Wallet, error) {
	rows, err := sqlcdb.New(s.db).GetWallets(ctx, sqlcdb.GetWalletsParams{
		Today: pgtype.Date{Time: s.today(), Valid: true}, FamilyID: familyID, WalletID: id,
	})
	if err != nil {
		return Wallet{}, err
	}
	if len(rows) == 0 {
		return Wallet{}, ErrNotFound
	}
	row := rows[0]
	return Wallet{ID: row.ID, Name: row.Name, Type: row.Type, Provider: row.Provider,
		Currency: row.Currency, InitialMinor: row.InitialBalanceMinor,
		SettlementDay: int(row.SettlementDay), PaymentDay: int(row.PaymentDay),
		LimitMinor: row.CreditLimitMinor, BalanceMinor: row.BalanceMinor,
		CicilanMendatangMinor: row.CicilanMendatangMinor}, nil
}

func (s *Store) CreateWallet(ctx context.Context, familyID int64, w Wallet) (int64, error) {
	return sqlcdb.New(s.db).CreateWallet(ctx, sqlcdb.CreateWalletParams{
		FamilyID: familyID, Name: w.Name, Type: w.Type, Provider: w.Provider,
		Currency: w.Currency, InitialBalanceMinor: w.InitialMinor,
		SettlementDay: int16(w.SettlementDay), PaymentDay: int16(w.PaymentDay), CreditLimitMinor: w.LimitMinor,
	})
}

func (s *Store) UpdateWallet(ctx context.Context, familyID int64, w Wallet) error {
	rows, err := sqlcdb.New(s.db).UpdateWallet(ctx, sqlcdb.UpdateWalletParams{
		ID: w.ID, FamilyID: familyID, Name: w.Name, Type: w.Type, Provider: w.Provider,
		Currency: w.Currency, InitialBalanceMinor: w.InitialMinor,
		SettlementDay: int16(w.SettlementDay), PaymentDay: int16(w.PaymentDay), CreditLimitMinor: w.LimitMinor,
	})
	if err == nil && rows == 0 {
		return ErrNotFound
	}
	return err
}

// DeleteWallet menolak menghapus dompet yang masih dipakai transaksi —
// menghapusnya diam-diam akan mengubah saldo dompet lain tanpa jejak.
func (s *Store) DeleteWallet(ctx context.Context, familyID, id int64) error {
	q := sqlcdb.New(s.db)
	n, err := q.CountWalletTransactions(ctx, sqlcdb.CountWalletTransactionsParams{
		FamilyID: familyID, WalletID: pgtype.Int8{Int64: id, Valid: true},
	})
	if err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("dompet ini masih dipakai %d transaksi, hapus transaksinya dulu", n)
	}
	rows, err := q.DeleteWallet(ctx, sqlcdb.DeleteWalletParams{ID: id, FamilyID: familyID})
	if err == nil && rows == 0 {
		return ErrNotFound
	}
	return err
}
