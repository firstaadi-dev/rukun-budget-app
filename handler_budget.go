package main

import (
	"errors"
	"net/http"
	"strings"
)

type budgetItem struct {
	Category
	Used, Remaining, OverAmount string
	Percent                     int
	HasLimit, Over              bool
}

func (a *App) budgetList(w http.ResponseWriter, r *http.Request) {
	id := family(r)
	cats, err := a.store.Categories(r.Context(), id, "expense")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	f, err := a.store.FamilyByID(r.Context(), id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	p := bacaPeriodeKustom(r.URL.Query().Get("periode"), "", "", a.today(), f.PeriodStartDay)
	spend, err := a.store.CategorySpending(r.Context(), id, p.From, p.To)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	rates, err := a.rates(r.Context(), id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	amounts := map[string]int64{}
	for _, s := range spend {
		if s.Kind == "expense" {
			if n, ok := categoryAmount(s, rates, a.base); ok {
				amounts[s.Category] += n
			}
		}
	}
	var totalUsed, totalLimit int64
	unbudgeted := 0
	items := make([]budgetItem, len(cats))
	for i := range cats {
		cats[i].BudgetText = FormatPlain(cats[i].BudgetMinor, a.base)
		spent := amounts[cats[i].Name]
		items[i] = budgetItem{Category: cats[i], Used: Format(spent, a.base), HasLimit: cats[i].BudgetMinor > 0,
			Remaining:  Format(max(0, cats[i].BudgetMinor-spent), a.base),
			OverAmount: Format(max(0, spent-cats[i].BudgetMinor), a.base),
			Percent:    int(float64(spent) / float64(max(1, cats[i].BudgetMinor)) * 100), Over: spent > cats[i].BudgetMinor}
		totalUsed += spent
		totalLimit += cats[i].BudgetMinor
		if cats[i].BudgetMinor == 0 {
			unbudgeted++
		}
	}
	a.render(w, r, "anggaran.html", map[string]any{"Title": "Anggaran", "Nav": "anggaran", "Base": a.base, "Period": p.Label,
		"Categories": items, "TotalUsed": Format(totalUsed, a.base), "TotalLimit": Format(totalLimit, a.base),
		"Available": Format(max(0, totalLimit-totalUsed), a.base), "Unbudgeted": unbudgeted})
}

func (a *App) categoryBudget(w http.ResponseWriter, r *http.Request) {
	value := strings.TrimSpace(r.FormValue("anggaran"))
	var amount int64
	if value != "" {
		var err error
		amount, err = ParseAmount(value, a.base)
		if err != nil || amount < 0 {
			http.Error(w, "Anggaran tidak valid.", http.StatusUnprocessableEntity)
			return
		}
	}
	err := a.store.SetCategoryBudget(r.Context(), family(r), pathID(r), amount)
	if errors.Is(err, ErrNotFound) {
		a.notFound(w)
		return
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/anggaran", http.StatusSeeOther)
}
