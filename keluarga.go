package main

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
)

func (a *App) startForm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	a.renderStart(w, r, "", http.StatusOK)
}

func (a *App) renderStart(w http.ResponseWriter, r *http.Request, message string, status int) {
	if userFrom(r.Context()).FamilyID != 0 {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	code := strings.TrimSpace(r.FormValue("kode"))
	if code == "" {
		if c, err := r.Cookie(pendingInviteCookie); err == nil {
			code = c.Value
		}
	}
	a.render(w, r, "mulai.html", map[string]any{
		"Title": "Keluarga", "NoChrome": true, "Error": message,
		"Kode": code,
	})
}

func (a *App) startCreate(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("nama"))
	if len(name) < 2 || len(name) > 80 {
		a.renderStart(w, r, "Nama keluarga harus 2–80 karakter.", http.StatusUnprocessableEntity)
		return
	}
	if _, err := a.store.CreateFamilyForUser(r.Context(), userFrom(r.Context()).ID, name, newSignupCode()); err != nil {
		if errors.Is(err, ErrAlreadyLinked) {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		a.fail(w, r, err)
		return
	}
	a.clearCookie(w, r, pendingInviteCookie)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) startJoin(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.FormValue("kode"))
	name := strings.TrimSpace(r.FormValue("nama"))
	if code == "" || len(code) > 128 {
		a.renderStart(w, r, "Masukkan kode keluarga yang valid.", http.StatusUnprocessableEntity)
		return
	}
	if len(name) < 2 || len(name) > 128 {
		a.renderStart(w, r, "Nama tampilan harus 2–128 karakter.", http.StatusUnprocessableEntity)
		return
	}
	if err := a.store.JoinFamily(r.Context(), userFrom(r.Context()).ID, code, name); err != nil {
		if errors.Is(err, ErrAlreadyLinked) {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		if errors.Is(err, ErrNotFound) {
			a.renderStart(w, r, "Kode keluarga tidak ditemukan atau sudah kedaluwarsa. Minta kode baru kepada kepala keluarga.", http.StatusUnprocessableEntity)
			return
		}
		// Pelanggaran nama unik juga berarti akun belum bergabung; tampilkan
		// pesan yang bisa diselesaikan pengguna tanpa mengekspos rincian DB.
		a.renderStart(w, r, "Tidak bisa bergabung. Nama tampilan mungkin sudah dipakai di keluarga itu.", http.StatusConflict)
		return
	}
	a.clearCookie(w, r, pendingInviteCookie)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func inviteURL(r *http.Request, code string) string {
	scheme := "http"
	if https(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host + "/mulai?kode=" + url.QueryEscape(code)
}
