package main

import (
	"errors"
	"net/http"
	"strings"
)

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
	p := bacaPeriodeKustom("", "", "", a.today(), f.PeriodStartDay)
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
	used := map[string]string{}
	amounts := map[string]int64{}
	for _, s := range spend {
		if s.Kind == "expense" {
			if n, ok := categoryAmount(s, rates, a.base); ok {
				amounts[s.Category] += n
			}
		}
	}
	for k, n := range amounts {
		used[k] = Format(n, a.base)
	}
	for i := range cats {
		cats[i].BudgetText = FormatPlain(cats[i].BudgetMinor, a.base)
		if _, ok := used[cats[i].Name]; !ok {
			used[cats[i].Name] = Format(0, a.base)
		}
	}
	a.render(w, r, "anggaran.html", map[string]any{"Title": "Anggaran", "Nav": "anggaran", "Base": a.base, "Period": p.Label, "Categories": cats, "Used": used})
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
