package main

import (
	"net/http"
)

// ---------- dashboard ----------

func (a *App) dashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	wallets, err := a.store.Wallets(ctx, family(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	rates, err := a.rates(ctx, family(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	txs, err := a.store.Transactions(ctx, family(r), TxFilter{Limit: 5})
	if err != nil {
		a.fail(w, r, err)
		return
	}

	// Ringkasan per kategori dibatasi bulan berjalan: itu rentang yang dipakai
	// keluarga saat menilai "bulan ini boros di mana", dan membuat angkanya
	// bisa dibandingkan antar bulan.
	awal := awalBulan(a.today())
	spend, err := a.store.CategorySpending(ctx, family(r), awal, awal.AddDate(0, 1, 0))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	cats, err := a.store.Categories(ctx, family(r), "expense")
	if err != nil {
		a.fail(w, r, err)
		return
	}

	cards, err := a.cardStatuses(ctx, family(r), wallets)
	if err != nil {
		a.fail(w, r, err)
		return
	}

	a.render(w, r, "dashboard.html", map[string]any{
		"Cards":     cards,
		"Perhatian": perhatian(cards),
		"Title":     "Dashboard",
		"Nav":       "dashboard",
		"Today":     tanggalPanjang(a.today()),
		"Summary":   summarize(wallets, rates, a.base),
		"Wallets":   viewWallets(wallets),
		"Recent":    viewTxs(txs, a.today()),
		"Breakdown": breakdown(spend, rates, a.base, namaBulan(awal)),
		"Budgets":   budgetRows(cats, spend, rates, a.base),
	})
}
