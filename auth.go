package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	sessionCookie = "rukun_session"
	familyCookie  = "rukun_keluarga" // kode keluarga terakhir, agar tidak perlu diketik ulang
	sessionTTL    = 30 * 24 * time.Hour
	familyTTL     = 365 * 24 * time.Hour
)

type loginFailure struct {
	count int
	until time.Time
}

// Batasi tebakan sandi per alamat dan akun selama 15 menit. Di deployment
// beberapa instance batas ini per proses; penyimpanan bersama baru perlu jika
// penyalahgunaan nyata menembus batas itu.
func (a *App) loginAllowed(key string) bool {
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	f := a.loginFailures[key]
	if f.count == 0 && len(a.loginFailures) >= 10000 {
		for k, v := range a.loginFailures {
			if time.Now().After(v.until) {
				delete(a.loginFailures, k)
			}
		}
		if len(a.loginFailures) >= 10000 {
			return false
		}
	}
	return time.Now().After(f.until) || f.count < 10
}

func (a *App) loginFailed(key string) {
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	if a.loginFailures == nil {
		a.loginFailures = make(map[string]loginFailure)
	}
	now := time.Now()
	f := a.loginFailures[key]
	if now.After(f.until) {
		f = loginFailure{until: now.Add(15 * time.Minute)}
	}
	f.count++
	a.loginFailures[key] = f
}

func (a *App) loginSucceeded(key string) {
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	delete(a.loginFailures, key)
}

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
			a.clearCookie(w, r, sessionCookie)
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

func (a *App) setCookie(w http.ResponseWriter, r *http.Request, name, value string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		Expires:  time.Now().Add(ttl),
		HttpOnly: true,
		Secure:   https(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func (a *App) clearCookie(w http.ResponseWriter, r *http.Request, name string) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: https(r), SameSite: http.SameSiteLaxMode,
	})
}

func newToken() string {
	b := make([]byte, 32)
	rand.Read(b) // crypto/rand.Read tidak pernah gagal sejak Go 1.24
	return base64.RawURLEncoding.EncodeToString(b)
}

// kodeKeluarga membaca kode dari form, dan kalau kosong dari cookie kunjungan
// sebelumnya. Kode itu bukan kredensial login — ia hanya menentukan keluarga
// mana yang dimaksud, karena nama anggota cuma unik di dalam keluarganya.
func kodeKeluarga(r *http.Request) string {
	if v := strings.TrimSpace(r.FormValue("kode")); v != "" {
		return v
	}
	if c, err := r.Cookie(familyCookie); err == nil {
		return c.Value
	}
	return ""
}

func (a *App) loginForm(w http.ResponseWriter, r *http.Request) {
	a.renderAuth(w, r, "masuk.html", nil, "")
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("nama"))
	code := kodeKeluarga(r)
	form := map[string]string{"Nama": name, "Kode": code}
	if len(name) > 128 || len(code) > 128 {
		http.Error(w, "Isian masuk terlalu panjang.", http.StatusBadRequest)
		return
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	key := ip + "|" + strings.ToLower(code) + "|" + strings.ToLower(name)
	if !a.loginAllowed(key) {
		w.Header().Set("Retry-After", "900")
		http.Error(w, "Terlalu banyak percobaan masuk. Coba lagi dalam 15 menit.", http.StatusTooManyRequests)
		return
	}

	// Satu pesan untuk semua kegagalan. Kode keluarga yang salah tidak boleh
	// bisa dibedakan dari sandi yang salah, kalau tidak kode keluarga orang
	// lain bisa ditebak satu per satu lewat halaman login.
	fail := func() {
		a.loginFailed(key)
		a.renderAuth(w, r, "masuk.html", form, "Kode keluarga, nama, atau kata sandi salah.")
	}

	family, err := a.store.FamilyByCode(r.Context(), code)
	if err != nil {
		fail()
		return
	}
	u, hash, err := a.store.UserByName(r.Context(), family.ID, name)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(r.FormValue("sandi"))) != nil {
		fail()
		return
	}
	// Diperiksa sesudah sandinya cocok, bukan sebelum. Pesannya spesifik, dan
	// yang spesifik hanya boleh terbaca oleh orang yang memang pemilik akunnya
	// — kalau tidak, ia jadi cara menebak nama anggota keluarga lain.
	if u.Disabled {
		a.loginFailed(key)
		a.renderAuth(w, r, "masuk.html", form,
			"Akses akun ini sudah dicabut oleh kepala keluarga.")
		return
	}
	a.loginSucceeded(key)
	a.setCookie(w, r, familyCookie, family.SignupCode, familyTTL)
	a.startSession(w, r, u.ID)
}

func (a *App) registerForm(w http.ResponseWriter, r *http.Request) {
	a.renderAuth(w, r, "daftar.html", nil, "")
}

func (a *App) register(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("nama"))
	sandi := r.FormValue("sandi")
	code := strings.TrimSpace(r.FormValue("kode"))
	form := map[string]string{"Nama": name, "Kode": code}

	fail := func(msg string) { a.renderAuth(w, r, "daftar.html", form, msg) }

	family, err := a.store.FamilyByCode(r.Context(), code)
	if err != nil {
		fail("Kode undangan tidak cocok dengan keluarga mana pun.")
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
	if _, _, err := a.store.UserByName(r.Context(), family.ID, name); err == nil {
		fail("Nama itu sudah dipakai anggota lain di keluarga ini.")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(sandi), bcrypt.DefaultCost)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	id, err := a.store.CreateUser(r.Context(), family.ID, name, string(hash))
	if err != nil {
		fail("Gagal mendaftar, coba nama lain.")
		return
	}
	// Pendaftaran baru jarang terjadi dan layak terlihat di log: kalau ada nama
	// tak dikenal muncul, kode undangan keluarga itu sudah bocor.
	log.Printf("anggota baru terdaftar: %q (id %d) di keluarga %q (id %d)",
		name, id, family.Name, family.ID)

	a.setCookie(w, r, familyCookie, family.SignupCode, familyTTL)
	a.startSession(w, r, id)
}

// newSession memberi perangkat ini sesi baru. Dipisah dari startSession karena
// ganti sandi juga memakainya: seluruh sesi lama dihapus dulu, lalu perangkat
// yang sedang dipakai diberi sesi pengganti supaya tidak ikut terlempar keluar.
func (a *App) newSession(w http.ResponseWriter, r *http.Request, userID int64) error {
	token := newToken()
	if err := a.store.CreateSession(r.Context(), token, userID, time.Now().Add(sessionTTL)); err != nil {
		return err
	}
	a.setCookie(w, r, sessionCookie, token, sessionTTL)
	return nil
}

func (a *App) startSession(w http.ResponseWriter, r *http.Request, userID int64) {
	if err := a.newSession(w, r, userID); err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := a.store.DeleteSession(r.Context(), c.Value); err != nil {
			log.Printf("hapus sesi: %v", err)
		}
	}
	// Cookie kode keluarga sengaja dibiarkan: itu bukan kredensial, dan
	// menyimpannya membuat anggota tidak perlu mengetik ulang kodenya.
	a.clearCookie(w, r, sessionCookie)
	http.Redirect(w, r, "/masuk", http.StatusSeeOther)
}

func (a *App) renderAuth(w http.ResponseWriter, r *http.Request, page string, form map[string]string, errMsg string) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if _, err := a.store.SessionUser(r.Context(), c.Value); err == nil {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
	}
	if form == nil {
		form = map[string]string{}
	}
	if form["Kode"] == "" {
		if c, err := r.Cookie(familyCookie); err == nil {
			form["Kode"] = c.Value
		}
	}
	if errMsg != "" {
		w.WriteHeader(http.StatusUnauthorized)
	}
	a.render(w, r, page, map[string]any{
		"Form":     form,
		"Error":    errMsg,
		"NoChrome": true,
	})
}
