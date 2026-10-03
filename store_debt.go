package main

import (
	"context"
	"errors"
	"fmt"
	sqlcdb "github.com/firsta/rukun/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"time"
)

// ---------- Pihak, hutang, dan piutang ----------

type Party struct {
	ID                int64
	Name              string
	Note              string
	Hidden            bool
	DueOn             pgtype.Date
	PaymentWalletID   int64
	PaymentWalletName string
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

// Parties mengembalikan seluruh pihak beserta saldo hutang piutangnya.
// Pihak yang saldonya sudah nol tetap ditampilkan: riwayatnya masih berguna,
// dan menghilangkannya membuat user mengira catatannya hilang.
func (s *Store) Parties(ctx context.Context, familyID int64) ([]Party, error) {
	rows, err := sqlcdb.New(s.db).GetParties(ctx, familyID)
	if err != nil {
		return nil, err
	}
	var out []Party
	for _, row := range rows {
		if n := len(out); n == 0 || out[n-1].ID != row.ID {
			out = append(out, Party{ID: row.ID, Name: row.Name, Note: row.Note, Hidden: row.Hidden, DueOn: row.DueOn, PaymentWalletID: row.PaymentWalletID, PaymentWalletName: row.PaymentWalletName})
		}
		if row.Cur != "" && (row.Hutang != 0 || row.Piutang != 0) {
			p := &out[len(out)-1]
			p.Saldo = append(p.Saldo, PartyBalance{
				Currency: row.Cur, HutangMinor: row.Hutang, PiutangMinor: row.Piutang,
			})
		}
	}
	return out, nil
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
	row, err := sqlcdb.New(s.db).GetPartyByName(ctx, sqlcdb.GetPartyByNameParams{FamilyID: familyID, Name: name})
	if errors.Is(err, pgx.ErrNoRows) {
		return Party{}, ErrNotFound
	}
	return Party{ID: row.ID, Name: row.Name, Note: row.Note}, err
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
	return sqlcdb.New(s.db).InsertParty(ctx, sqlcdb.InsertPartyParams{FamilyID: familyID, Name: name})
}

func (s *Store) UpdateParty(ctx context.Context, familyID, id int64, name, note string) error {
	rows, err := sqlcdb.New(s.db).UpdateParty(ctx, sqlcdb.UpdatePartyParams{
		Name: name, Note: note, ID: id, FamilyID: familyID,
	})
	if err == nil && rows == 0 {
		return ErrNotFound
	}
	return err
}

// DeleteParty menolak menghapus pihak yang masih punya catatan transaksi.
// Menghapusnya akan membuat riwayat hutang menggantung tanpa lawan transaksi.
func (s *Store) DeleteParty(ctx context.Context, familyID, id int64) error {
	q := sqlcdb.New(s.db)
	n, err := q.CountPartyTransactions(ctx, sqlcdb.CountPartyTransactionsParams{
		FamilyID: familyID, PartyID: pgtype.Int8{Int64: id, Valid: true},
	})
	if err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("pihak ini masih punya %d catatan, hapus catatannya dulu", n)
	}
	rows, err := q.DeleteParty(ctx, sqlcdb.DeletePartyParams{ID: id, FamilyID: familyID})
	if err == nil && rows == 0 {
		return ErrNotFound
	}
	return err
}

// PartyTxs: riwayat hutang piutang satu pihak, terbaru dulu.
func (s *Store) PartyTxs(ctx context.Context, familyID, partyID int64) ([]Tx, error) {
	return s.getTxs(ctx, sqlcdb.GetTransactionsParams{FamilyID: familyID, PartyID: partyID})
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
	row, err := sqlcdb.New(s.db).GetCardBalances(ctx, sqlcdb.GetCardBalancesParams{
		WalletID: walletID, FamilyID: familyID,
		Settlement: pgtype.Date{Time: settlement, Valid: true},
		Today:      pgtype.Date{Time: s.today(), Valid: true},
	})
	return row.AtSettlement, row.CreditsSince, err
}

var ErrPartyUnsettled = errors.New("Pihak yang masih memiliki saldo hutang atau piutang belum bisa disembunyikan.")

// SetPartyHidden checks every currency and direction before hiding a party.
// History remains available, and a nonzero balance is never hidden.
func (s *Store) SetPartyHidden(ctx context.Context, familyID, id int64, hidden bool) error {
	result, err := s.db.Exec(ctx, `
		UPDATE parties p SET hidden=$3
		WHERE p.family_id=$1 AND p.id=$2 AND (
			NOT $3 OR NOT EXISTS (
				SELECT COALESCE(w.currency,t.currency)
				FROM transactions t
				LEFT JOIN wallets w ON w.id=t.wallet_id AND w.family_id=t.family_id
				WHERE t.family_id=$1 AND t.party_id=$2
				GROUP BY COALESCE(w.currency,t.currency)
				HAVING SUM(CASE t.kind WHEN 'debt_in' THEN t.amount_minor WHEN 'debt_pay' THEN -t.amount_minor ELSE 0 END) <> 0
				OR SUM(CASE t.kind WHEN 'loan_out' THEN t.amount_minor WHEN 'loan_in' THEN -t.amount_minor ELSE 0 END) <> 0
			)
		)`, familyID, id, hidden)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		if _, err := s.Party(ctx, familyID, id); err != nil {
			return err
		}
		return ErrPartyUnsettled
	}
	return nil
}
