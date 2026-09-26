package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	sessionCookie       = "rukun_session"
	familyCookie        = "rukun_keluarga" // cookie lama; dibersihkan saat halaman autentikasi dibuka
	pendingInviteCookie = "rukun_pending_invite"
	sessionTTL          = 30 * 24 * time.Hour
)

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,39}$`)

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
		loginDest := "/masuk"
		if r.URL.Path == "/mulai" && strings.TrimSpace(r.URL.Query().Get("kode")) != "" {
			loginDest = "/daftar"
		}
		rememberInvite := func() {
			if r.URL.Path != "/mulai" {
				return
			}
			if code := strings.TrimSpace(r.URL.Query().Get("kode")); code != "" && len(code) <= 128 {
				a.setCookie(w, r, pendingInviteCookie, code, 24*time.Hour)
			}
		}
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			rememberInvite()
			http.Redirect(w, r, loginDest, http.StatusSeeOther)
			return
		}
		u, err := a.store.SessionUser(r.Context(), c.Value)
		if err != nil {
			rememberInvite()
			a.clearCookie(w, r, sessionCookie)
			http.Redirect(w, r, loginDest, http.StatusSeeOther)
			return
		}
		if u.FamilyID == 0 {
			rememberInvite()
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, u)))
	})
}

func (a *App) requireFamily(next http.HandlerFunc) http.Handler {
	return a.requireUser(func(w http.ResponseWriter, r *http.Request) {
		if userFrom(r.Context()).FamilyID == 0 {
			http.Redirect(w, r, "/mulai", http.StatusSeeOther)
			return
		}
		next(w, r)
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

func (a *App) verifyCurrentPassword(ctx context.Context, u User, password string) (bool, *firebaseToken) {
	if u.FirebaseUID != "" {
		if a.firebase == nil {
			return false, nil
		}
		token, err := a.firebase.signIn(ctx, u.Email, password)
		if err != nil {
			return false, nil
		}
		account, err := a.firebase.account(ctx, token.IDToken)
		if err != nil || !account.EmailVerified || account.LocalID != u.FirebaseUID {
			return false, nil
		}
		return true, &token
	}
	hash, err := a.store.PasswordHash(ctx, u.FamilyID, u.ID)
	return err == nil && bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil, nil
}

func (a *App) loginForm(w http.ResponseWriter, r *http.Request) {
	a.renderAuth(w, r, "masuk.html", nil, "")
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	identifier := strings.TrimSpace(r.FormValue("identifier"))
	if identifier == "" { // dukung form lama selama transisi deployment
		identifier = strings.TrimSpace(r.FormValue("username"))
	}
	form := map[string]string{"Identifier": identifier}
	if len(identifier) > 254 {
		http.Error(w, "Isian masuk terlalu panjang.", http.StatusBadRequest)
		return
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	key := ip + "|" + strings.ToLower(identifier)
	if !a.loginAllowed(key) {
		w.Header().Set("Retry-After", "900")
		http.Error(w, "Terlalu banyak percobaan masuk. Coba lagi dalam 15 menit.", http.StatusTooManyRequests)
		return
	}

	// Satu pesan untuk semua kegagalan agar akun tidak bisa ditebak.
	fail := func() {
		a.loginFailed(key)
		a.renderAuth(w, r, "masuk.html", form, "Akun atau kata sandi salah.")
	}

	var u User
	if strings.Contains(identifier, "@") {
		if a.firebase == nil {
			fail()
			return
		}
		email, err := normalizeEmail(identifier)
		if err != nil {
			fail()
			return
		}
		token, err := a.firebase.signIn(r.Context(), email, r.FormValue("sandi"))
		if err != nil {
			fail()
			return
		}
		account, err := a.firebase.account(r.Context(), token.IDToken)
		if err != nil || account.LocalID != token.LocalID || !account.EmailVerified {
			// Sandi sudah terbukti benar; kirim ulang verifikasi untuk membantu
			// pengguna yang tidak menerima email pertama. Firebase membatasi spam.
			if err == nil && account.LocalID == token.LocalID && !account.EmailVerified {
				_ = a.firebase.sendVerification(r.Context(), token.IDToken, a.firebaseContinueURL(r))
			}
			a.loginFailed(key)
			a.renderAuth(w, r, "masuk.html", form, "Email belum diverifikasi. Kami mengirim ulang tautan verifikasi; cek inbox dan folder spam.")
			return
		}
		u, err = a.store.FirebaseUser(r.Context(), token.LocalID)
		if errors.Is(err, ErrNotFound) || (err == nil && !strings.EqualFold(u.Email, account.Email)) {
			fail()
			return
		}
		if err != nil {
			a.fail(w, r, err)
			return
		}
		if err := a.store.MarkFirebaseEmailVerified(r.Context(), token.LocalID); err != nil {
			a.fail(w, r, err)
			return
		}
		u.EmailVerified = true
	} else {
		var hash string
		u, hash, err = a.store.UserByUsername(r.Context(), identifier)
		if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(r.FormValue("sandi"))) != nil {
			fail()
			return
		}
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
	dest := "/"
	if u.FamilyID == 0 {
		dest = "/mulai"
	}
	a.startSession(w, r, u.ID, dest)
}

func (a *App) registerForm(w http.ResponseWriter, r *http.Request) {
	a.renderAuth(w, r, "daftar.html", nil, "")
}

func (a *App) register(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("nama"))
	email, emailErr := normalizeEmail(r.FormValue("email"))
	sandi := r.FormValue("sandi")
	form := map[string]string{"Nama": name, "Email": email}

	fail := func(msg string) { a.renderAuth(w, r, "daftar.html", form, msg) }
	if a.firebase == nil {
		fail("Firebase Auth belum dikonfigurasi. Coba lagi nanti.")
		return
	}

	if len(name) < 2 || len(name) > 128 {
		fail("Nama harus 2–128 karakter.")
		return
	}
	if emailErr != nil {
		fail("Masukkan alamat email yang valid.")
		return
	}
	if len(sandi) < 8 || len(sandi) > 128 {
		fail("Kata sandi harus 8–128 karakter.")
		return
	}
	continueURL := a.firebaseContinueURL(r)
	if continueURL == "" {
		fail("APP_URL belum dikonfigurasi. Pendaftaran email belum bisa digunakan.")
		return
	}
	token, err := a.firebase.signUp(r.Context(), email, sandi)
	if err != nil {
		if fe, ok := err.(firebaseError); ok && fe.Code == "EMAIL_EXISTS" {
			fail("Email sudah terdaftar. Masuk menggunakan email tersebut.")
			return
		}
		log.Printf("Firebase sign up gagal: %v", err)
		fail("Tidak bisa membuat akun sekarang. Periksa email dan kata sandi, lalu coba lagi.")
		return
	}
	_, err = a.store.CreateFirebaseUser(r.Context(), token.LocalID, email, name)
	if err != nil {
		// Hapus akun Firebase jika pencatatan lokal gagal agar pendaftaran bisa diulang.
		_ = a.firebase.call(r.Context(), "delete", map[string]string{"idToken": token.IDToken}, nil)
		log.Printf("simpan akun Firebase lokal gagal: %v", err)
		fail("Email sudah terhubung ke akun Rukun lain atau akun gagal disimpan.")
		return
	}
	if err := a.firebase.sendVerification(r.Context(), token.IDToken, continueURL); err != nil {
		log.Printf("kirim verifikasi Firebase gagal: %v", err)
		a.renderAuth(w, r, "daftar.html", form, "Akun sudah dibuat, tetapi email verifikasi belum terkirim. Coba masuk kembali untuk mencoba lagi.")
		return
	}
	a.render(w, r, "verifikasi_email.html", map[string]any{
		"Title": "Verifikasi email", "NoChrome": true, "Email": email,
	})
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

func (a *App) startSession(w http.ResponseWriter, r *http.Request, userID int64, dest string) {
	if err := a.newSession(w, r, userID); err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := a.store.DeleteSession(r.Context(), c.Value); err != nil {
			log.Printf("hapus sesi: %v", err)
		}
	}
	a.clearCookie(w, r, sessionCookie)
	http.Redirect(w, r, "/masuk", http.StatusSeeOther)
}

func (a *App) renderAuth(w http.ResponseWriter, r *http.Request, page string, form map[string]string, errMsg string) {
	a.clearCookie(w, r, familyCookie)
	if c, err := r.Cookie(sessionCookie); err == nil {
		if u, err := a.store.SessionUser(r.Context(), c.Value); err == nil {
			dest := "/"
			if u.FamilyID == 0 {
				dest = "/mulai"
			}
			http.Redirect(w, r, dest, http.StatusSeeOther)
			return
		}
	}
	if form == nil {
		form = map[string]string{}
	}
	if errMsg != "" {
		w.WriteHeader(http.StatusUnauthorized)
	}
	a.render(w, r, page, map[string]any{
		"Form":          form,
		"Error":         errMsg,
		"EmailVerified": r.URL.Query().Get("verified") == "1",
		"NoChrome":      true,
	})
}
