package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Run with TEST_DATABASE_URL pointing to a disposable Postgres database.
func TestDatabaseMoneyFlows(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	s := &Store{db: pool, loc: time.UTC}
	f, err := s.CreateFamily(ctx, "Integration", fmt.Sprintf("integration-%d", time.Now().UnixNano()), "Tester", "unused-hash")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `DELETE FROM families WHERE id = $1`, f.ID)
	u, _, err := s.UserByName(ctx, f.ID, "Tester")
	if err != nil {
		t.Fatal(err)
	}
	wid, err := s.CreateWallet(ctx, f.ID, Wallet{Name: "Broker", Type: "broker", Currency: "IDR", InitialMinor: 1000})
	if err != nil {
		t.Fatal(err)
	}
	vid, err := s.CreateInvestment(ctx, f.ID, Investment{Kind: "stock", Name: "Test Asset", Currency: "IDR", WalletID: wid})
	if err != nil {
		t.Fatal(err)
	}
	today := s.today()
	for _, buy := range []Tx{
		{Kind: "invest_buy", Date: today, WalletID: wid, WalletCur: "IDR", AmountMinor: 100, InvestmentID: vid, QtyE8: qtyScale},
		{Kind: "invest_buy", Date: today, WalletID: wid, WalletCur: "IDR", AmountMinor: 200, InvestmentID: vid, QtyE8: 2 * qtyScale},
	} {
		if err := s.CreateInvestmentBuy(ctx, f.ID, buy, u.ID); err != nil {
			t.Fatal(err)
		}
	}
	sale := Tx{Kind: "invest_sell", Date: today, WalletID: wid, WalletCur: "IDR", AmountMinor: 260, InvestmentID: vid, QtyE8: 2 * qtyScale}
	if err := s.CreateInvestmentSale(ctx, f.ID, sale, u.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateInvestmentSale(ctx, f.ID, sale, u.ID); !errors.Is(err, ErrInsufficientQty) {
		t.Fatalf("oversell: %v", err)
	}
	v, err := s.Investment(ctx, f.ID, vid)
	if err != nil {
		t.Fatal(err)
	}
	if v.QtyE8 != qtyScale || v.ModalMinor != 100 || v.RealizedMinor != 60 || v.Sales != 1 {
		t.Fatalf("position after sale: %+v", v)
	}
	w, err := s.Wallet(ctx, f.ID, wid)
	if err != nil {
		t.Fatal(err)
	}
	if w.BalanceMinor != 960 {
		t.Fatalf("wallet balance %d, want 960", w.BalanceMinor)
	}
	trades, err := s.Transactions(ctx, f.ID, TxFilter{Investment: vid})
	if err != nil {
		t.Fatal(err)
	}
	if len(trades) != 3 {
		t.Fatalf("trades %d, want 3", len(trades))
	}
	for _, trade := range trades {
		if trade.Kind == "invest_buy" && trade.ID < trades[0].ID {
			if err := s.DeleteInvestmentTrade(ctx, f.ID, trade); !errors.Is(err, ErrTradeLocked) {
				t.Fatalf("delete old buy: %v", err)
			}
			break
		}
	}
	if err := s.DeleteInvestmentTrade(ctx, f.ID, trades[0]); err != nil {
		t.Fatal(err)
	}
	v, err = s.Investment(ctx, f.ID, vid)
	if err != nil {
		t.Fatal(err)
	}
	if v.QtyE8 != 3*qtyScale || v.ModalMinor != 300 || v.RealizedMinor != 0 {
		t.Fatalf("undo sale: %+v", v)
	}

	for i := 0; i < 3; i++ {
		_, err = s.CreateTx(ctx, f.ID, Tx{Kind: "expense", Date: today, WalletID: wid, WalletCur: "IDR", AmountMinor: 1, Category: "Test"}, u.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	page1, err := s.Transactions(ctx, f.ID, TxFilter{Kinds: []string{"expense"}, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	page2, err := s.Transactions(ctx, f.ID, TxFilter{Kinds: []string{"expense"}, Limit: 2, BeforeDate: page1[1].Date, BeforeID: page1[1].ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 2 || len(page2) != 1 || page2[0].ID >= page1[1].ID {
		t.Fatalf("pagination: %v, %v", page1, page2)
	}

	_, err = s.CreateTx(ctx, f.ID, Tx{Kind: "expense", Date: today, WalletID: wid, WalletCur: "IDR", AmountMinor: 7, Category: walletAdjustmentCategory, Note: walletAdjustmentCategory, IsAdjustment: true}, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateTx(ctx, f.ID, Tx{Kind: "expense", Date: today, WalletID: wid, WalletCur: "IDR", AmountMinor: 9, Category: walletAdjustmentCategory}, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	spend, err := s.CategorySpending(ctx, f.ID, today, today.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	var manual int64
	for _, row := range spend {
		if row.Category == walletAdjustmentCategory {
			manual += row.Minor
		}
	}
	if manual != 9 {
		t.Fatalf("adjustment in spending: %d, want 9", manual)
	}
}
