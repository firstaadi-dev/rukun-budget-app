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

// ---------- Kategori ----------

type Category struct {
	ID          int64
	Kind        string // expense | income
	Name        string
	Usage       int // jumlah transaksi yang memakainya
	BudgetMinor int64
	BudgetText  string
}

// Categories mengembalikan kategori beserta jumlah pemakaiannya. kind kosong
// berarti semua jenis.
//
// Jumlah pemakaian dihitung lewat satu agregat yang di-join, bukan subquery
// berkorelasi per baris: bentuk yang kedua menjalankan satu query terpisah untuk
// setiap kategori, sehingga membuka halaman Kategori berarti belasan pemindaian
// tabel transaksi sekaligus.
func (s *Store) Categories(ctx context.Context, familyID int64, kind string) ([]Category, error) {
	rows, err := sqlcdb.New(s.db).GetCategories(ctx, sqlcdb.GetCategoriesParams{FamilyID: familyID, Kind: kind})
	if err != nil {
		return nil, err
	}
	out := make([]Category, len(rows))
	for i, row := range rows {
		out[i] = Category{ID: row.ID, Kind: row.Kind, Name: row.Name,
			Usage: int(row.Usage), BudgetMinor: row.BudgetMinor}
	}
	return out, nil
}

func (s *Store) SetCategoryBudget(ctx context.Context, familyID, id, amount int64) error {
	rows, err := sqlcdb.New(s.db).UpdateCategoryBudget(ctx, sqlcdb.UpdateCategoryBudgetParams{
		BudgetMinor: amount, FamilyID: familyID, ID: id,
	})
	if err == nil && rows == 0 {
		return ErrNotFound
	}
	return err
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
	row, err := sqlcdb.New(s.db).GetCategory(ctx, sqlcdb.GetCategoryParams{ID: id, FamilyID: familyID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Category{}, ErrNotFound
	}
	return Category{ID: row.ID, Kind: row.Kind, Name: row.Name}, err
}

func (s *Store) CreateCategory(ctx context.Context, familyID int64, kind, name string) error {
	return sqlcdb.New(s.db).InsertCategory(ctx, sqlcdb.InsertCategoryParams{
		FamilyID: familyID, Kind: kind, Name: name,
	})
}

func (s *Store) EnsureCategory(ctx context.Context, familyID int64, kind, name string) error {
	return sqlcdb.New(s.db).EnsureCategory(ctx, sqlcdb.EnsureCategoryParams{
		FamilyID: familyID, Kind: kind, Name: name,
	})
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

	q := sqlcdb.New(tx)
	if err := q.RenameCategoryName(ctx, sqlcdb.RenameCategoryNameParams{
		Name: name, ID: id, FamilyID: familyID,
	}); err != nil {
		return err
	}
	if err := q.RenameTransactionCategory(ctx, sqlcdb.RenameTransactionCategoryParams{
		NewName: pgtype.Text{String: name, Valid: true}, FamilyID: familyID,
		Kind: old.Kind, OldName: pgtype.Text{String: old.Name, Valid: true},
	}); err != nil {
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
	q := sqlcdb.New(s.db)
	n, err := q.CountCategoryTransactions(ctx, sqlcdb.CountCategoryTransactionsParams{
		FamilyID: familyID, Kind: c.Kind, Category: pgtype.Text{String: c.Name, Valid: true},
	})
	if err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("kategori %q masih dipakai %d transaksi, ubah kategori transaksinya dulu", c.Name, n)
	}
	return q.DeleteCategory(ctx, sqlcdb.DeleteCategoryParams{ID: id, FamilyID: familyID})
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
	rows, err := sqlcdb.New(s.db).GetCategorySpending(ctx, sqlcdb.GetCategorySpendingParams{
		FamilyID: familyID, FromDate: pgtype.Date{Time: from, Valid: true},
		ToDate: pgtype.Date{Time: to, Valid: true}, Today: pgtype.Date{Time: s.today(), Valid: true},
	})
	if err != nil {
		return nil, err
	}
	out := make([]CategorySpend, len(rows))
	for i, row := range rows {
		out[i] = CategorySpend{Kind: row.Kind, Category: row.Category.String,
			Currency: row.Currency, Minor: row.Minor}
	}
	return out, nil
}

// ---------- Kurs ----------

// Rates mengembalikan kurs terakhir untuk tiap pasangan mata uang, diambil
// dari transfer lintas mata uang yang sudah tercatat. Ini sekaligus jadi
// "kurs default penyedia" di form transfer dan dasar konversi total di
// dashboard — tanpa API kurs eksternal.
// ponytail: kalau nanti butuh kurs harian tanpa harus transfer dulu, tambahkan
// tabel rates dan pakai nilai itu sebagai fallback di sini saja.
func (s *Store) Rates(ctx context.Context, familyID int64) (map[string]Rate, error) {
	rows, err := sqlcdb.New(s.db).GetTransferRates(ctx, familyID)
	if err != nil {
		return nil, err
	}

	out := map[string]Rate{}
	for _, row := range rows {
		from, to := row.FromCurrency, row.ToCurrency
		r := Rate{FromMinor: row.FromMinor, From: from, ToMinor: row.ToMinor.Int64, To: to}
		out[from+">"+to] = r
		// Arah sebaliknya hanya dipakai kalau belum ada catatan aslinya.
		if _, ok := out[to+">"+from]; !ok {
			out[to+">"+from] = r.Invert()
		}
	}
	return out, nil
}
