package main

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"
)

// ---------- render & error ----------

func (a *App) render(w http.ResponseWriter, r *http.Request, page string, data map[string]any) {
	t, ok := a.pages[page]
	if !ok {
		a.fail(w, r, errors.New("template tidak ada: "+page))
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	u := userFrom(r.Context())
	data["User"] = u
	data["Family"] = u.FamilyName
	data["Path"] = r.URL.Path
	data["V"] = a.ver

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout.html", data); err != nil {
		// Header sudah terkirim di titik ini; cuma bisa dicatat.
		log.Printf("render %s: %v", page, err)
	}
}

func (a *App) fail(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
	http.Error(w, "Terjadi kesalahan di server.", http.StatusInternalServerError)
}

func (a *App) notFound(w http.ResponseWriter) {
	http.Error(w, "Halaman tidak ditemukan.", http.StatusNotFound)
}

func (a *App) today() time.Time {
	n := time.Now().In(a.loc)
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, a.loc)
}

// family mengambil id keluarga dari sesi. Ini satu-satunya sumbernya: tidak
// pernah dari parameter URL atau isian form, yang bisa dikarang siapa saja.
func family(r *http.Request) int64 {
	return userFrom(r.Context()).FamilyID
}

func pathID(r *http.Request) int64 {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id
}
