package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"log"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	sessionCookie = "rukun_session"
	sessionTTL    = 30 * 24 * time.Hour
)

type ctxKey struct{}

func userFrom(ctx context.Context) User {
	u, _ := ctx.Value(ctxKey{}).(User)
	return u
}

func (a *App) requireUser(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			http.Redirect(w, r, "/masuk", http.StatusSeeOther)
			return
		}
		u, err := a.store.SessionUser(r.Context(), c.Value)
		if err != nil {
			a.clearCookie(w, r)
			http.Redirect(w, r, "/masuk", http.StatusSeeOther)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, u)))
	})
}

// https: Render menerminasi TLS di proxy, jadi skema asli ada di header.
func https(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

func (a *App) setCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  time.Now().Add(sessionTTL),
		HttpOnly: true,
		Secure:   https(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func (a *App) clearCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: https(r), SameSite: http.SameSiteLaxMode,
	})
}

func newToken() string {
	b := make([]byte, 32)
	rand.Read(b) // crypto/rand.Read tidak pernah gagal sejak Go 1.24
	return base64.RawURLEncoding.EncodeToString(b)
}

func (a *App) loginForm(w http.ResponseWriter, r *http.Request) {
	a.renderAuth(w, r, "masuk.html", nil, "")
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("nama"))
	u, hash, err := a.store.UserByName(r.Context(), name)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(r.FormValue("sandi"))) != nil {
		// Pesan sengaja tidak membedakan nama salah dan sandi salah.
		a.renderAuth(w, r, "masuk.html", map[string]string{"Nama": name}, "Nama atau kata sandi salah.")
		return
	}
	a.startSession(w, r, u.ID)
}

func (a *App) registerForm(w http.ResponseWriter, r *http.Request) {
	a.renderAuth(w, r, "daftar.html", nil, "")
}

func (a *App) register(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("nama"))
	sandi := r.FormValue("sandi")
	form := map[string]string{"Nama": name}

	fail := func(msg string) { a.renderAuth(w, r, "daftar.html", form, msg) }

	if subtle.ConstantTimeCompare([]byte(r.FormValue("kode")), []byte(a.code)) != 1 {
		fail("Kode undangan tidak cocok.")
		return
	}
	if len(name) < 2 {
		fail("Nama minimal 2 karakter.")
		return
	}
	if len(sandi) < 8 {
		fail("Kata sandi minimal 8 karakter.")
		return
	}
	if _, _, err := a.store.UserByName(r.Context(), name); err == nil {
		fail("Nama itu sudah dipakai anggota lain.")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(sandi), bcrypt.DefaultCost)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	id, err := a.store.CreateUser(r.Context(), name, string(hash))
	if err != nil {
		fail("Gagal mendaftar, coba nama lain.")
		return
	}
	a.startSession(w, r, id)
}

func (a *App) startSession(w http.ResponseWriter, r *http.Request, userID int64) {
	token := newToken()
	if err := a.store.CreateSession(r.Context(), token, userID, time.Now().Add(sessionTTL)); err != nil {
		a.fail(w, r, err)
		return
	}
	a.setCookie(w, r, token)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := a.store.DeleteSession(r.Context(), c.Value); err != nil {
			log.Printf("hapus sesi: %v", err)
		}
	}
	a.clearCookie(w, r)
	http.Redirect(w, r, "/masuk", http.StatusSeeOther)
}

func (a *App) renderAuth(w http.ResponseWriter, r *http.Request, page string, form map[string]string, errMsg string) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if _, err := a.store.SessionUser(r.Context(), c.Value); err == nil {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
	}
	n, err := a.store.UserCount(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if errMsg != "" {
		w.WriteHeader(http.StatusUnauthorized)
	}
	a.render(w, r, page, map[string]any{
		"Family":   a.family,
		"Form":     form,
		"Error":    errMsg,
		"Kosong":   n == 0, // pendaftar pertama: tampilkan ajakan buat akun
		"NoChrome": true,
	})
}
