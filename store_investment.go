package main

import (
	"context"
	"errors"
	sqlcdb "github.com/firsta/rukun/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"time"
)

// ---------- Harga tersimpan ----------

// Tiga metode di bawah ini satu-satunya yang tidak mengambil familyID, dan itu
// disengaja — lihat alasannya di migrasi 009. Isinya harga pasar, bukan catatan
// siapa pun.

// Quotes membaca harga tersimpan untuk simbol yang diminta. Yang tidak ada
// tidak muncul di hasilnya, bukan muncul sebagai nol.
func (s *Store) Quotes(ctx context.Context, symbols []string) (map[string]Kuotasi, error) {
	if len(symbols) == 0 {
		return nil, nil
	}
	return s.getQuotes(ctx, symbols)
}

func (s *Store) getQuotes(ctx context.Context, symbols []string) (map[string]Kuotasi, error) {
	rows, err := sqlcdb.New(s.db).GetQuotes(ctx, symbols)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Kuotasi, len(rows))
	for _, row := range rows {
		k := Kuotasi{PriceE4: row.PriceE4, Currency: row.Currency, Nama: row.Name}
		if row.QuotedAt.Valid {
			k.At = row.QuotedAt.Time
		} else {
			k.At, k.Diambil = row.FetchedAt.Time, true
		}
		out[row.Symbol] = k
	}
	return out, nil
}

// AllQuotes mengisi cache harga saat aplikasi mulai.
func (s *Store) AllQuotes(ctx context.Context) (map[string]Kuotasi, error) {
	return s.getQuotes(ctx, []string{})
}

// SaveQuotes menyimpan harga baru; nama yang sudah diketahui tidak dikosongkan.
func (s *Store) SaveQuotes(ctx context.Context, quotes map[string]Kuotasi) error {
	var batch []sqlcdb.SaveQuoteParams
	for sym, k := range quotes {
		if sym == "" || !k.Ada() {
			continue
		}
		var quotedAt pgtype.Timestamptz
		if !k.Diambil && !k.At.IsZero() {
			quotedAt = pgtype.Timestamptz{Time: k.At, Valid: true}
		}
		batch = append(batch, sqlcdb.SaveQuoteParams{Symbol: sym, PriceE4: k.PriceE4,
			Currency: k.Currency, QuotedAt: quotedAt, Name: k.Nama})
	}
	if len(batch) == 0 {
		return nil
	}
	return sqlcdb.New(s.db).SaveQuote(ctx, batch).Close()
}

// SimbolDipakai mengambil kode dan jenis posisi untuk penyegar harga bersama.
func (s *Store) SimbolDipakai(ctx context.Context) (map[string]string, error) {
	rows, err := sqlcdb.New(s.db).GetUsedSymbols(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.Symbol] = row.Kind
	}
	return out, nil
}

// ---------- Investasi ----------

// Kuantitas dan modal posisi dihitung dari lot oleh sqlc/queries/investments.sql.
func (s *Store) Investments(ctx context.Context, familyID int64) ([]Investment, error) {
	return s.getInvestments(ctx, familyID, 0)
}

func (s *Store) Investment(ctx context.Context, familyID, id int64) (Investment, error) {
	rows, err := s.getInvestments(ctx, familyID, id)
	if err != nil {
		return Investment{}, err
	}
	if len(rows) == 0 {
		return Investment{}, ErrNotFound
	}
	return rows[0], nil
}

func (s *Store) getInvestments(ctx context.Context, familyID, id int64) ([]Investment, error) {
	rows, err := sqlcdb.New(s.db).GetInvestments(ctx, sqlcdb.GetInvestmentsParams{
		FamilyID: familyID, InvestmentID: id,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Investment, len(rows))
	for i, row := range rows {
		out[i] = Investment{ID: row.ID, Kind: row.Kind, Name: row.Name, Varian: row.Varian,
			Symbol: row.Symbol, Currency: row.Currency, WalletID: row.WalletID,
			WalletName: row.WalletName, ManualPriceE4: row.ManualPriceE4,
			ManualPriceOn: row.ManualPriceOn.Time, Note: row.Note, CreatedAt: row.CreatedAt.Time,
			QtyE8: row.QtyE8, ModalMinor: row.ModalMinor, FeeMinor: row.FeeMinor,
			Lots: int(row.Lots), LastBuy: row.LastBuy.Time, Sales: int(row.Sales),
			RealizedMinor: row.RealizedMinor}
	}
	return out, nil
}

func (s *Store) CreateInvestment(ctx context.Context, familyID int64, v Investment) (int64, error) {
	return sqlcdb.New(s.db).CreateInvestment(ctx, sqlcdb.CreateInvestmentParams{
		FamilyID: familyID, Kind: v.Kind, Name: v.Name, Varian: v.Varian,
		Symbol: v.Symbol, Currency: v.Currency, WalletID: v.WalletID,
		ManualPriceE4: v.ManualPriceE4, ManualPriceOn: nullDate(v.ManualPriceOn), Note: v.Note,
	})
}

func (s *Store) UpdateInvestment(ctx context.Context, familyID int64, v Investment) error {
	rows, err := sqlcdb.New(s.db).UpdateInvestment(ctx, sqlcdb.UpdateInvestmentParams{
		FamilyID: familyID, ID: v.ID, Name: v.Name, Varian: v.Varian,
		Symbol: v.Symbol, Currency: v.Currency, WalletID: v.WalletID,
		ManualPriceE4: v.ManualPriceE4, ManualPriceOn: nullDate(v.ManualPriceOn), Note: v.Note,
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteInvestment menolak posisi yang masih punya lot. Menghapusnya berarti
// menghapus pembelian yang sudah menggerakkan saldo dompet, dan saldo itu akan
// melompat tanpa ada transaksi yang menjelaskannya.
func (s *Store) DeleteInvestment(ctx context.Context, familyID, id int64) error {
	rows, err := sqlcdb.New(s.db).DeleteInvestment(ctx, sqlcdb.DeleteInvestmentParams{
		FamilyID: familyID, ID: id,
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// Kedua operasi ini mengunci posisi yang sama: dua anggota tidak bisa menjual
// unit yang sama bersamaan, dan pembelian yang berbarengan masuk urutan jelas.
func (s *Store) CreateInvestmentBuy(ctx context.Context, familyID int64, t Tx, userID int64) error {
	dbtx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer dbtx.Rollback(context.WithoutCancel(ctx))
	q := sqlcdb.New(dbtx)
	id, err := q.LockInvestment(ctx, sqlcdb.LockInvestmentParams{FamilyID: familyID, ID: t.InvestmentID})
	if err != nil {
		return err
	}
	lastSale, err := q.LastInvestmentSaleDate(ctx, sqlcdb.LastInvestmentSaleDateParams{
		FamilyID: familyID, InvestmentID: pgtype.Int8{Int64: id, Valid: true},
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if lastSale.Valid && hari(t.Date).Before(hari(lastSale.Time)) {
		return ErrTradeDate
	}
	if _, err = insertTx(ctx, dbtx, familyID, t, userID); err != nil {
		return err
	}
	return dbtx.Commit(ctx)
}

func (s *Store) CreateInvestmentSale(ctx context.Context, familyID int64, t Tx, userID int64) error {
	dbtx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer dbtx.Rollback(context.WithoutCancel(ctx))
	q := sqlcdb.New(dbtx)
	id, err := q.LockInvestment(ctx, sqlcdb.LockInvestmentParams{FamilyID: familyID, ID: t.InvestmentID})
	if err != nil {
		return err
	}
	position, err := q.InvestmentTradePosition(ctx, sqlcdb.InvestmentTradePositionParams{
		FamilyID: familyID, InvestmentID: pgtype.Int8{Int64: id, Valid: true},
	})
	if err != nil {
		return err
	}
	if position.LastTrade.Valid && hari(t.Date).Before(hari(position.LastTrade.Time)) {
		return ErrTradeDate
	}
	if t.QtyE8 <= 0 || t.QtyE8 > position.QtyE8 {
		return ErrInsufficientQty
	}
	t.CostBasisMinor = saleCostBasis(position.CostMinor, position.QtyE8, t.QtyE8)
	if _, err = insertTx(ctx, dbtx, familyID, t, userID); err != nil {
		return err
	}
	return dbtx.Commit(ctx)
}

func (s *Store) DeleteInvestmentTrade(ctx context.Context, familyID int64, t Tx) error {
	dbtx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer dbtx.Rollback(context.WithoutCancel(ctx))
	q := sqlcdb.New(dbtx)
	id, err := q.LockInvestment(ctx, sqlcdb.LockInvestmentParams{FamilyID: familyID, ID: t.InvestmentID})
	if err != nil {
		return err
	}
	later, err := q.CountLaterInvestmentSales(ctx, sqlcdb.CountLaterInvestmentSalesParams{
		FamilyID: familyID, InvestmentID: pgtype.Int8{Int64: id, Valid: true}, ID: t.ID,
	})
	if err != nil {
		return err
	}
	if later > 0 {
		return ErrTradeLocked
	}
	rows, err := q.DeleteInvestmentTrade(ctx, sqlcdb.DeleteInvestmentTradeParams{
		FamilyID: familyID, InvestmentID: pgtype.Int8{Int64: id, Valid: true}, ID: t.ID,
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return dbtx.Commit(ctx)
}

// nullDate mengirim tanggal kosong sebagai NULL. Tanpa ini ia tersimpan sebagai
// 1 Januari tahun 1, yang tetap terbaca sebagai tanggal dan ikut tampil.
func nullDate(t time.Time) pgtype.Date {
	if t.IsZero() {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: t, Valid: true}
}
