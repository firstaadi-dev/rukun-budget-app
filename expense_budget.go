package main

import (
	"encoding/json"
	"math/big"
	"net/http"
	"strconv"
	"time"
)

type expenseBudgetEvaluation struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

func evaluateExpenseBudget(category string, limit, projected int64, base string) expenseBudgetEvaluation {
	if limit <= 0 {
		return expenseBudgetEvaluation{"NO_BUDGET", "Kategori belum dialokasikan."}
	}
	if projected >= limit {
		return expenseBudgetEvaluation{"OVERBUDGET", "Melebihi anggaran " + category + " sebesar " + Format(max(0, projected-limit), base)}
	}
	// Compare without truncating percentages or overflowing minor units.
	used := new(big.Int).Mul(big.NewInt(projected), big.NewInt(100))
	warning := new(big.Int).Mul(big.NewInt(limit), big.NewInt(80))
	if used.Cmp(warning) >= 0 {
		return expenseBudgetEvaluation{"WARNING", "Mendekati limit • Sisa: " + Format(limit-projected, base)}
	}
	return expenseBudgetEvaluation{"SAFE", "Sesuai pos anggaran • Sisa kuota: " + Format(limit-projected, base)}
}

func (a *App) expenseBudgetStatus(w http.ResponseWriter, r *http.Request) {
	ctx, familyID := r.Context(), family(r)
	q := r.URL.Query()
	date, err := time.ParseInLocation(formatTanggal, q.Get("tanggal"), a.loc)
	if err != nil {
		http.Error(w, "Tanggal tidak valid", http.StatusUnprocessableEntity)
		return
	}
	f, err := a.store.FamilyByID(ctx, familyID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	p := bacaPeriodeKustom("", "", "", date, f.PeriodStartDay)
	cats, err := a.store.Categories(ctx, familyID, "expense")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var limit int64
	for _, c := range cats {
		if c.Name == q.Get("kategori") {
			limit = c.BudgetMinor
			break
		}
	}
	result := evaluateExpenseBudget(q.Get("kategori"), limit, 0, a.base)
	if limit > 0 {
		walletID, _ := strconv.ParseInt(q.Get("dompet"), 10, 64)
		wallet, err := a.store.Wallet(ctx, familyID, walletID)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		amount, err := ParseAmount(q.Get("nominal"), wallet.Currency)
		if err != nil || amount < 0 {
			http.Error(w, "Nominal tidak valid", http.StatusUnprocessableEntity)
			return
		}
		n, _ := strconv.Atoi(q.Get("cicilan"))
		if q.Get("mode") == "cicil" && n >= 2 && n <= cicilanMaks {
			amount = amount/int64(n) + amount%int64(n)
		}
		rates, err := a.rates(ctx, familyID)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		charge, known := categoryAmount(CategorySpend{Currency: wallet.Currency, Minor: amount}, rates, a.base)
		if !known {
			http.Error(w, "Kurs belum tersedia untuk evaluasi anggaran", http.StatusUnprocessableEntity)
			return
		}
		spend, err := a.store.CategorySpending(ctx, familyID, p.From, p.To)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		var used int64
		for _, s := range spend {
			if s.Kind != "expense" || s.Category != q.Get("kategori") {
				continue
			}
			value, ok := categoryAmount(s, rates, a.base)
			if !ok {
				http.Error(w, "Kurs belum tersedia untuk evaluasi anggaran", http.StatusUnprocessableEntity)
				return
			}
			used += value
		}
		editID, _ := strconv.ParseInt(q.Get("id"), 10, 64)
		if editID > 0 {
			old, err := a.store.Transaction(ctx, familyID, editID)
			if err != nil {
				a.fail(w, r, err)
				return
			}
			if old.Kind == "expense" && old.Category == q.Get("kategori") && !old.Date.Before(p.From) && old.Date.Before(p.To) {
				value, ok := categoryAmount(CategorySpend{Currency: old.WalletCur, Minor: old.AmountMinor}, rates, a.base)
				if ok {
					used -= value
				}
			}
		}
		result = evaluateExpenseBudget(q.Get("kategori"), limit, max(0, used)+charge, a.base)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(result)
}
