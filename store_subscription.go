package main

import (
	"context"
	"errors"

	db "github.com/firsta/rukun/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type Subscription struct {
	ID, WalletID                         int64
	Name, WalletName, Currency, Category string
	AmountMinor                          int64
	NextDate                             pgtype.Date
	IntervalMonths                       int16
	Active                               bool
	Amount, NextLabel                    string
}

func (s *Store) Subscriptions(ctx context.Context, familyID int64, activeOnly bool) ([]Subscription, error) {
	rows, err := db.New(s.db).ListSubscriptions(ctx, db.ListSubscriptionsParams{FamilyID: familyID, ActiveOnly: activeOnly})
	if err != nil {
		return nil, err
	}
	out := make([]Subscription, len(rows))
	for i, v := range rows {
		out[i] = Subscription{ID: v.ID, Name: v.Name, WalletID: v.WalletID, WalletName: v.WalletName, Currency: v.Currency, Category: v.Category, AmountMinor: v.AmountMinor, NextDate: v.NextDate, IntervalMonths: v.IntervalMonths, Active: v.Active}
	}
	return out, nil
}

func (s *Store) SaveSubscription(ctx context.Context, familyID int64, v Subscription) (int64, error) {
	q := db.New(s.db)
	date := pgtype.Date{Time: v.NextDate.Time, Valid: true}
	if v.ID == 0 {
		return q.CreateSubscription(ctx, db.CreateSubscriptionParams{FamilyID: familyID, WalletID: v.WalletID, Name: v.Name, Category: v.Category, AmountMinor: v.AmountMinor, NextDate: date, IntervalMonths: v.IntervalMonths})
	}
	n, err := q.UpdateSubscription(ctx, db.UpdateSubscriptionParams{WalletID: v.WalletID, Name: v.Name, Category: v.Category, AmountMinor: v.AmountMinor, NextDate: date, IntervalMonths: v.IntervalMonths, Active: v.Active, FamilyID: familyID, ID: v.ID})
	if err == nil && n == 0 {
		return 0, ErrNotFound
	}
	return v.ID, err
}

func (s *Store) DeleteSubscription(ctx context.Context, familyID, id int64) error {
	n, err := db.New(s.db).DeleteSubscription(ctx, db.DeleteSubscriptionParams{FamilyID: familyID, ID: id})
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) PaySubscription(ctx context.Context, familyID, id, userID int64, today pgtype.Date) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	v, err := db.New(tx).GetSubscription(ctx, db.GetSubscriptionParams{FamilyID: familyID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	// Pembayaran menambah transaksi biasa dan memajukan jatuh tempo secara atomik.
	n, err := db.New(tx).AdvanceSubscription(ctx, db.AdvanceSubscriptionParams{FamilyID: familyID, ID: id, NextDate: v.NextDate})
	if err != nil {
		return err
	}
	if n == 0 {
		return errors.New("subscription berubah; muat ulang halaman")
	}
	if _, err := insertTx(ctx, tx, familyID, Tx{Kind: "expense", Date: today.Time, WalletID: v.WalletID, AmountMinor: v.AmountMinor, Category: v.Category, Note: v.Name}, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
