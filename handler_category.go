package main

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// ---------- kategori ----------

func (a *App) categoryList(w http.ResponseWriter, r *http.Request) {
	a.renderCategories(w, r, "", 0)
}

// renderCategories menampilkan daftar kategori. errMsg kosong berarti tidak ada
// masalah; status dipakai supaya kegagalan hapus tidak dijawab 200.
func (a *App) renderCategories(w http.ResponseWriter, r *http.Request, errMsg string, status int) {
	cats, err := a.store.Categories(r.Context(), family(r), "")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var expense, income []Category
	for _, c := range cats {
		if c.Kind == "income" {
			income = append(income, c)
		} else {
			c.BudgetText = FormatPlain(c.BudgetMinor, a.base)
			expense = append(expense, c)
		}
	}
	if status != 0 {
		w.WriteHeader(status)
	}
	a.render(w, r, "kategori.html", map[string]any{
		"Title": "Kategori", "Nav": "kategori",
		"Expense": expense, "Income": income, "Error": errMsg, "Base": a.base,
	})
}

func (a *App) categoryBudget(w http.ResponseWriter, r *http.Request) {
	value := strings.TrimSpace(r.FormValue("anggaran"))
	var amount int64
	if value != "" {
		var err error
		amount, err = ParseAmount(value, a.base)
		if err != nil || amount < 0 {
			a.renderCategories(w, r, "Anggaran harus nominal positif atau kosong untuk menghapus.", http.StatusUnprocessableEntity)
			return
		}
	}
	if err := a.store.SetCategoryBudget(r.Context(), family(r), pathID(r), amount); errors.Is(err, ErrNotFound) {
		a.notFound(w)
		return
	} else if err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/kategori", http.StatusSeeOther)
}

func (a *App) categoryForm(w http.ResponseWriter, r *http.Request) {
	f := map[string]string{"jenis": r.URL.Query().Get("jenis")}
	if _, ok := kindLabel[f["jenis"]]; !ok || f["jenis"] == "transfer" {
		f["jenis"] = "expense"
	}
	var id int64

	if r.PathValue("id") != "" {
		id = pathID(r)
		c, err := a.store.Category(r.Context(), family(r), id)
		if errors.Is(err, ErrNotFound) {
			a.notFound(w)
			return
		} else if err != nil {
			a.fail(w, r, err)
			return
		}
		f["jenis"], f["nama"] = c.Kind, c.Name
	}
	a.renderCategoryForm(w, r, id, f, "")
}

func (a *App) renderCategoryForm(w http.ResponseWriter, r *http.Request, id int64, f map[string]string, errMsg string) {
	action, title := "/kategori/baru", "Kategori Baru"
	if id != 0 {
		action = "/kategori/" + strconv.FormatInt(id, 10) + "/ubah"
		title = "Ubah Kategori"
	}
	if errMsg != "" {
		w.WriteHeader(http.StatusUnprocessableEntity)
	}
	a.render(w, r, "kategori_form.html", map[string]any{
		"Title": title, "Nav": "kategori", "Back": "/kategori",
		"Action": action, "ID": id, "Form": f, "Error": errMsg,
		"Kinds": []struct{ Value, Label string }{
			{"expense", "Pengeluaran"}, {"income", "Pemasukan"},
		},
	})
}

func readCategory(r *http.Request) (kind, name string, f map[string]string, err error) {
	f = map[string]string{
		"jenis": r.FormValue("jenis"),
		"nama":  strings.TrimSpace(r.FormValue("nama")),
	}
	if f["jenis"] != "expense" && f["jenis"] != "income" {
		return "", "", f, errors.New("Jenis kategori tidak dikenal.")
	}
	if len(f["nama"]) < 2 {
		return "", "", f, errors.New("Nama kategori minimal 2 karakter.")
	}
	if len(f["nama"]) > 40 {
		return "", "", f, errors.New("Nama kategori terlalu panjang.")
	}
	return f["jenis"], f["nama"], f, nil
}

func (a *App) categoryCreate(w http.ResponseWriter, r *http.Request) {
	kind, name, f, err := readCategory(r)
	if err != nil {
		a.renderCategoryForm(w, r, 0, f, err.Error())
		return
	}
	if err := a.store.CreateCategory(r.Context(), family(r), kind, name); err != nil {
		// Satu-satunya kegagalan yang wajar di sini adalah nama kembar.
		a.renderCategoryForm(w, r, 0, f, "Kategori dengan nama itu sudah ada.")
		return
	}
	http.Redirect(w, r, "/kategori", http.StatusSeeOther)
}

func (a *App) categoryUpdate(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	_, name, f, err := readCategory(r)
	if err != nil {
		a.renderCategoryForm(w, r, id, f, err.Error())
		return
	}
	// Jenis kategori tidak bisa diubah: transaksi lama dicocokkan lewat pasangan
	// jenis dan nama, jadi memindahkan kategori antar jenis akan memutus
	// kaitannya dengan transaksi yang sudah ada.
	if err := a.store.RenameCategory(r.Context(), family(r), id, name); errors.Is(err, ErrNotFound) {
		a.notFound(w)
		return
	} else if err != nil {
		a.renderCategoryForm(w, r, id, f, "Kategori dengan nama itu sudah ada.")
		return
	}
	http.Redirect(w, r, "/kategori", http.StatusSeeOther)
}

func (a *App) categoryDelete(w http.ResponseWriter, r *http.Request) {
	err := a.store.DeleteCategory(r.Context(), family(r), pathID(r))
	if errors.Is(err, ErrNotFound) {
		a.notFound(w)
		return
	}
	if err != nil {
		a.renderCategories(w, r, err.Error(), http.StatusConflict)
		return
	}
	http.Redirect(w, r, "/kategori", http.StatusSeeOther)
}
