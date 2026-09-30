package main

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

var subscriptionIntervals = []struct {
	Value int
	Label string
}{{1, "Bulanan"}, {2, "Tiap 2 bulan"}, {3, "Tiap 3 bulan"}, {6, "Tiap 6 bulan"}, {12, "Tahunan"}}

func (a *App) subscriptionList(w http.ResponseWriter, r *http.Request) {
	items, err := a.store.Subscriptions(r.Context(), family(r), false)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	for i := range items {
		items[i].Amount = Format(items[i].AmountMinor, items[i].Currency)
		items[i].NextLabel = tanggalPendek(items[i].NextDate.Time)
	}
	a.render(w, r, "subscription.html", map[string]any{"Title": "Langganan & Tagihan Rutin", "Nav": "subscription", "Items": items, "Today": a.today()})
}

func (a *App) subscriptionForm(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	f := map[string]string{"nominal": "", "interval": "1", "tanggal": a.today().Format(formatTanggal)}
	active := true
	if id > 0 {
		items, err := a.store.Subscriptions(r.Context(), family(r), false)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		found := false
		for _, v := range items {
			if v.ID == id {
				found = true
				active = v.Active
				f = map[string]string{"nama": v.Name, "dompet": strconv.FormatInt(v.WalletID, 10), "kategori": v.Category, "nominal": FormatPlain(v.AmountMinor, v.Currency), "tanggal": v.NextDate.Time.Format(formatTanggal), "interval": strconv.Itoa(int(v.IntervalMonths))}
				break
			}
		}
		if !found {
			a.notFound(w)
			return
		}
	}
	a.subscriptionFormPage(w, r, id, f, active, "")
}

func (a *App) subscriptionFormPage(w http.ResponseWriter, r *http.Request, id int64, f map[string]string, active bool, msg string) {
	wallets, err := a.store.Wallets(r.Context(), family(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	cats, err := a.store.CategoryNames(r.Context(), family(r), "expense")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if msg != "" {
		w.WriteHeader(http.StatusUnprocessableEntity)
	}
	a.render(w, r, "subscription_form.html", map[string]any{"Title": "Langganan", "Nav": "subscription", "Back": "/subscription", "ID": id, "Form": f, "Active": active, "Wallets": wallets, "Categories": cats, "Intervals": subscriptionIntervals, "Error": msg})
}

func (a *App) subscriptionCreate(w http.ResponseWriter, r *http.Request) { a.saveSubscription(w, r, 0) }
func (a *App) subscriptionUpdate(w http.ResponseWriter, r *http.Request) {
	a.saveSubscription(w, r, pathID(r))
}

func (a *App) saveSubscription(w http.ResponseWriter, r *http.Request, id int64) {
	f := map[string]string{"nama": strings.TrimSpace(r.FormValue("nama")), "dompet": r.FormValue("dompet"), "kategori": r.FormValue("kategori"), "nominal": r.FormValue("nominal"), "tanggal": r.FormValue("tanggal"), "interval": r.FormValue("interval")}
	v := Subscription{ID: id, Name: f["nama"], Category: f["kategori"], Active: r.FormValue("aktif") == "1" || id == 0}
	bad := func(s string) { a.subscriptionFormPage(w, r, id, f, v.Active, s) }
	if len(v.Name) < 2 || len(v.Name) > 60 {
		bad("Nama langganan harus 2–60 karakter.")
		return
	}
	var err error
	v.WalletID, err = strconv.ParseInt(f["dompet"], 10, 64)
	if err != nil || v.WalletID < 1 {
		bad("Dompet tidak valid.")
		return
	}
	wl, err := a.store.Wallet(r.Context(), family(r), v.WalletID)
	if err != nil {
		bad("Dompet tidak valid.")
		return
	}
	known, err := a.store.CategoryNames(r.Context(), family(r), "expense")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if !slicesContains(known, v.Category) {
		bad("Kategori pengeluaran tidak valid.")
		return
	}
	v.AmountMinor, err = ParseAmount(f["nominal"], wl.Currency)
	if err != nil || v.AmountMinor <= 0 {
		bad("Nominal harus lebih dari nol.")
		return
	}
	d, err := time.ParseInLocation(formatTanggal, f["tanggal"], a.loc)
	if err != nil {
		bad("Tanggal berikutnya tidak valid.")
		return
	}
	v.NextDate = pgtype.Date{Time: d, Valid: true}
	n, _ := strconv.Atoi(f["interval"])
	valid := false
	for _, it := range subscriptionIntervals {
		if n == it.Value {
			valid = true
		}
	}
	if !valid {
		bad("Interval tidak valid.")
		return
	}
	v.IntervalMonths = int16(n)
	_, err = a.store.SaveSubscription(r.Context(), family(r), v)
	if errors.Is(err, ErrNotFound) {
		a.notFound(w)
		return
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/subscription", http.StatusSeeOther)
}

func (a *App) subscriptionDelete(w http.ResponseWriter, r *http.Request) {
	err := a.store.DeleteSubscription(r.Context(), family(r), pathID(r))
	if errors.Is(err, ErrNotFound) {
		a.notFound(w)
		return
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/subscription", http.StatusSeeOther)
}

func (a *App) subscriptionPay(w http.ResponseWriter, r *http.Request) {
	err := a.store.PaySubscription(r.Context(), family(r), pathID(r), userFrom(r.Context()).ID, pgtype.Date{Time: a.today(), Valid: true})
	if errors.Is(err, ErrNotFound) {
		a.notFound(w)
		return
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/subscription", http.StatusSeeOther)
}
