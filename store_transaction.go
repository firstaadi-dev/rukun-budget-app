package main

import (
	"context"
	"fmt"
	sqlcdb "github.com/firsta/rukun/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
	"time"
)

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
	// InvestmentID dan QtyE8 terisi untuk pembelian dan penjualan investasi.
	// Kuantitasnya berskala 1e8, lihat
	// investasi.go.
	InvestmentID   int64
	InvestmentName string
	QtyE8          int64
	CostBasisMinor int64 // harga pokok unit yang dijual; nol untuk jenis lain
	IsAdjustment   bool
	// Rangkaian cicilan atau transaksi berulang. SeriesID nol berarti transaksi
	// ini berdiri sendiri; kalau terisi, ketiganya terisi bersama.
	SeriesID   int64
	SeriesSeq  int    // urutan ke berapa, mulai 1
	SeriesN    int    // berapa seluruhnya saat rangkaian ini dibuat
	SeriesKind string // cicil | ulang
	Note       string
	CreatedBy  string
	CreatedAt  time.Time
}

// Jenis hutang piutang. debt_* menyangkut kewajiban kita, loan_* menyangkut
// tagihan kita kepada orang lain.
var kindHutangPiutang = map[string]bool{
	"debt_in": true, "debt_pay": true, "loan_out": true, "loan_in": true,
}

// kindMenambahSaldo: jenis yang membuat saldo dompet bertambah. Dipakai
// GetWallets juga; keduanya harus selalu sepakat, karena jenis yang terlewat
// di salah satunya membuat saldo bohong tanpa error apa pun.
var kindMenambahSaldo = map[string]bool{
	"income": true, "debt_in": true, "loan_in": true, "invest_sell": true,
}

var seriesKindLabel = map[string]string{"cicil": "Cicilan", "ulang": "Berulang"}

func (t Tx) InSeries() bool { return t.SeriesID != 0 }

// SeriesLabel: "Cicilan 3/12". Kosong untuk transaksi yang berdiri sendiri,
// sehingga template bisa memakainya langsung lewat {{with}}.
func (t Tx) SeriesLabel() string {
	if !t.InSeries() {
		return ""
	}
	return fmt.Sprintf("%s %d/%d", seriesKindLabel[t.SeriesKind], t.SeriesSeq, t.SeriesN)
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

// getTxs memakai satu query sqlc untuk daftar, detail, dan riwayat pihak.
func (s *Store) getTxs(ctx context.Context, params sqlcdb.GetTransactionsParams) ([]Tx, error) {
	if params.Kinds == nil {
		params.Kinds = []string{}
	}
	rows, err := sqlcdb.New(s.db).GetTransactions(ctx, params)
	if err != nil {
		return nil, err
	}
	out := make([]Tx, len(rows))
	for i, row := range rows {
		investmentName := row.InvestmentName
		if row.InvestmentVariant != "" {
			investmentName += " · " + row.InvestmentVariant
		}
		out[i] = Tx{ID: row.ID, Kind: row.Kind, Date: row.OccurredOn.Time,
			WalletID: row.WalletID, WalletName: row.WalletName, WalletCur: row.WalletCur,
			AmountMinor: row.AmountMinor, Category: row.Category,
			ToWalletID: row.ToWalletID, ToWalletName: row.ToWalletName, ToWalletCur: row.ToWalletCur,
			AmountInMino: row.AmountInMinor, AdminFee: row.AdminFeeMinor,
			PartyID: row.PartyID, PartyName: row.PartyName, InvestmentID: row.InvestmentID,
			InvestmentName: investmentName, QtyE8: row.QtyE8, CostBasisMinor: row.CostBasisMinor,
			SeriesID: row.SeriesID, SeriesSeq: int(row.SeriesSeq), SeriesN: int(row.SeriesN),
			SeriesKind: row.SeriesKind, Note: row.Note, CreatedBy: row.CreatedBy,
			CreatedAt: row.CreatedAt.Time, IsAdjustment: row.IsAdjustment}
	}
	return out, nil
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
	// WalletID menyaring transaksi yang menyentuh dompet sebagai sumber atau tujuan.
	// Nol berarti semua dompet.
	WalletID int64
	// Investment menyaring lot satu posisi investasi. Nol berarti tidak
	// menyaring apa-apa.
	Investment int64
	// Limit 0 berarti tanpa batas.
	Limit int
	// Cursor menunjuk baris terakhir halaman sebelumnya.
	BeforeDate time.Time
	BeforeID   int64
}

func (s *Store) Transactions(ctx context.Context, familyID int64, f TxFilter) ([]Tx, error) {
	params := sqlcdb.GetTransactionsParams{
		FamilyID: familyID, Kinds: f.Kinds, Category: f.Category,
		Search: f.Cari, WalletID: f.WalletID, InvestmentID: f.Investment,
		LimitCount: int32(f.Limit), BeforeID: f.BeforeID,
	}
	if !f.From.IsZero() {
		params.FromDate = pgtype.Date{Time: f.From, Valid: true}
	}
	if !f.To.IsZero() {
		params.ToDate = pgtype.Date{Time: f.To, Valid: true}
	}
	if !f.BeforeDate.IsZero() && f.BeforeID > 0 {
		params.BeforeDate = pgtype.Date{Time: f.BeforeDate, Valid: true}
	}
	return s.getTxs(ctx, params)
}

func (s *Store) Transaction(ctx context.Context, familyID, id int64) (Tx, error) {
	txs, err := s.getTxs(ctx, sqlcdb.GetTransactionsParams{FamilyID: familyID, TransactionID: id})
	if err != nil {
		return Tx{}, err
	}
	if len(txs) == 0 {
		return Tx{}, ErrNotFound
	}
	return txs[0], nil
}

func insertTx(ctx context.Context, q sqlcdb.DBTX, familyID int64, t Tx, userID int64) (int64, error) {
	costBasis := pgtype.Int8{}
	if t.Kind == "invest_sell" {
		costBasis = pgtype.Int8{Int64: t.CostBasisMinor, Valid: true}
	}
	return sqlcdb.New(q).InsertTransaction(ctx, sqlcdb.InsertTransactionParams{
		FamilyID: familyID, Kind: t.Kind, OccurredOn: pgtype.Date{Time: t.Date, Valid: true},
		WalletID: t.WalletID, Currency: t.currencyKolom(), AmountMinor: t.AmountMinor,
		Category: t.Category, ToWalletID: t.ToWalletID, AmountInMinor: t.AmountInMino,
		AdminFeeMinor: t.AdminFee, PartyID: t.PartyID, InvestmentID: t.InvestmentID,
		QtyE8: t.QtyE8, CostBasisMinor: costBasis,
		SeriesID: t.SeriesID, SeriesSeq: int16(t.SeriesSeq), SeriesN: int16(t.SeriesN),
		SeriesKind: t.SeriesKind, Note: t.Note,
		CreatedBy: pgtype.Int8{Int64: userID, Valid: true}, IsAdjustment: t.IsAdjustment,
	})
}

func (s *Store) CreateTx(ctx context.Context, familyID int64, t Tx, userID int64) (int64, error) {
	return insertTx(ctx, s.db, familyID, t, userID)
}

// CreateTxs menyimpan beberapa transaksi sekaligus dan mengembalikan id yang
// pertama. Satu transaksi database untuk semuanya: separuh rangkaian atau
// penyesuaian yang tersimpan lebih buruk daripada gagal sama sekali.
//
// Nomor rangkaiannya diambil dari sequence sendiri, bukan dari id baris
// pertama: id baris pertama baru diketahui setelah ia tersimpan, dan menambalnya
// belakangan berarti sesaat ada baris bernomor urut tanpa rangkaian — keadaan
// yang harus diizinkan constraint, lalu tidak pernah bisa dijaganya lagi.
func (s *Store) CreateTxs(ctx context.Context, familyID int64, txs []Tx, userID int64) (int64, error) {
	if len(txs) == 1 {
		return insertTx(ctx, s.db, familyID, txs[0], userID)
	}
	dbtx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer dbtx.Rollback(context.WithoutCancel(ctx))

	var seriesID int64
	if txs[0].SeriesKind != "" {
		seriesID, err = sqlcdb.New(dbtx).NextTransactionSeriesID(ctx)
		if err != nil {
			return 0, err
		}
	}

	var first int64
	for _, t := range txs {
		if seriesID != 0 {
			t.SeriesID = seriesID
		}
		id, err := insertTx(ctx, dbtx, familyID, t, userID)
		if err != nil {
			return 0, err
		}
		if first == 0 {
			first = id
		}
	}
	return first, dbtx.Commit(ctx)
}

// UpdateSeries mengubah kategori dan catatan seluruh rangkaian sekaligus.
//
// Nominal dan tanggal sengaja tidak ikut. Mengubah nominal satu cicilan berarti
// membagi ulang seluruh sisanya — pertanyaan tersendiri yang jawabannya berbeda
// untuk cicilan dan untuk yang berulang — dan tanggal tiap bulan memang harus
// berbeda satu sama lain.
func (s *Store) UpdateSeries(ctx context.Context, familyID, seriesID int64, category, note string) error {
	rows, err := sqlcdb.New(s.db).UpdateTransactionSeries(ctx, sqlcdb.UpdateTransactionSeriesParams{
		Category: category, Note: note, FamilyID: familyID,
		SeriesID: pgtype.Int8{Int64: seriesID, Valid: true},
	})
	if err == nil && rows == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) DeleteSeries(ctx context.Context, familyID, seriesID int64) error {
	rows, err := sqlcdb.New(s.db).DeleteTransactionSeries(ctx, sqlcdb.DeleteTransactionSeriesParams{
		FamilyID: familyID, SeriesID: pgtype.Int8{Int64: seriesID, Valid: true},
	})
	if err == nil && rows == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) UpdateTx(ctx context.Context, familyID int64, t Tx) error {
	rows, err := sqlcdb.New(s.db).UpdateTransaction(ctx, sqlcdb.UpdateTransactionParams{
		OccurredOn: pgtype.Date{Time: t.Date, Valid: true}, WalletID: t.WalletID,
		Currency: t.currencyKolom(), AmountMinor: t.AmountMinor, Category: t.Category,
		ToWalletID: t.ToWalletID, AmountInMinor: t.AmountInMino, AdminFeeMinor: t.AdminFee,
		PartyID: t.PartyID, Note: t.Note, ID: t.ID, Kind: t.Kind, FamilyID: familyID,
	})
	if err == nil && rows == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) DeleteTx(ctx context.Context, familyID, id int64) error {
	rows, err := sqlcdb.New(s.db).DeleteTransaction(ctx, sqlcdb.DeleteTransactionParams{ID: id, FamilyID: familyID})
	if err == nil && rows == 0 {
		return ErrNotFound
	}
	return err
}
