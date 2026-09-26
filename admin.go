package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// API pengelolaan keluarga sengaja tidak punya UI. Membuat keluarga baru berarti
// membuat penyimpanan data terpisah, jadi hanya developer yang melakukannya atas
// permintaan manual. Migrasi username lama punya halaman terpisah yang dilindungi
// token admin dan tidak ditautkan dari UI publik.
//
// Aksesnya lewat header: Authorization: Bearer <ADMIN_TOKEN>.
// Tanpa ADMIN_TOKEN di environment, endpoint JSON membalas 404 —
// bukan 401 — supaya keberadaannya pun tidak ketahuan.

func (a *App) requireAdmin(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.admin == "" {
			a.notFound(w)
			return
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(token)), []byte(a.admin)) != 1 {
			log.Printf("admin ditolak: %s %s dari %s", r.Method, r.URL.Path, r.RemoteAddr)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "token admin tidak cocok"})
			return
		}
		next(w, r)
	})
}

// requireAdminPage protects the unlinked account-migration screen with the
// existing admin token. Basic auth lets an administrator open its URL directly.
func (a *App) requireAdminPage(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.admin == "" {
			a.notFound(w)
			return
		}
		user, token, ok := r.BasicAuth()
		if !ok || user != "admin" || subtle.ConstantTimeCompare([]byte(token), []byte(a.admin)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="Rukun admin", charset="UTF-8"`)
			http.Error(w, "Akses admin diperlukan.", http.StatusUnauthorized)
			return
		}
		next(w, r)
	})
}

func (a *App) adminLegacyAccounts(w http.ResponseWriter, r *http.Request) {
	message := ""
	if r.Method == http.MethodPost {
		id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
		username := strings.ToLower(strings.TrimSpace(r.FormValue("username")))
		if err != nil || id < 1 || !usernamePattern.MatchString(username) {
			message = "ID atau nama akun tidak valid."
			w.WriteHeader(http.StatusUnprocessableEntity)
		} else {
			updated, err := a.store.AdminSetLegacyUsername(r.Context(), id, username)
			if err != nil {
				message = "Nama akun sudah dipakai atau perubahan gagal."
				w.WriteHeader(http.StatusConflict)
			} else if !updated {
				message = "Akun itu sudah dimigrasikan atau tidak ditemukan."
				w.WriteHeader(http.StatusConflict)
			} else {
				http.Redirect(w, r, "/admin/migrasi-akun-lama", http.StatusSeeOther)
				return
			}
		}
	}
	accounts, err := a.store.LegacyAccounts(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.render(w, r, "admin_migrasi.html", map[string]any{
		"Title": "Migrasi akun lama", "NoChrome": true, "Accounts": accounts, "Error": message,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		log.Printf("tulis json: %v", err)
	}
}

// newSignupCode: tiga kelompok agar enak didiktekan lewat telepon, dan tetap
// punya entropi cukup untuk tidak bisa ditebak lewat halaman login.
func newSignupCode() string {
	b := make([]byte, 12)
	rand.Read(b)
	s := strings.ToLower(base64.RawURLEncoding.EncodeToString(b))
	s = strings.NewReplacer("-", "", "_", "").Replace(s)
	for len(s) < 15 {
		s += "x"
	}
	return s[0:5] + "-" + s[5:10] + "-" + s[10:15]
}

type familyJSON struct {
	ID                int64  `json:"id"`
	Nama              string `json:"nama"`
	KodeDaftar        string `json:"kode_daftar"`
	KodeBerlakuSampai string `json:"kode_berlaku_sampai"`
	Anggota           int    `json:"anggota,omitempty"`
	Dompet            int    `json:"dompet,omitempty"`
	Transaksi         int    `json:"transaksi,omitempty"`
	DibuatPada        string `json:"dibuat_pada,omitempty"`
}

func (a *App) toFamilyJSON(f Family) familyJSON {
	out := familyJSON{
		ID: f.ID, Nama: f.Name, KodeDaftar: f.SignupCode,
		KodeBerlakuSampai: f.SignupCodeExpiresAt.Format("2006-01-02 15:04 MST"),
		Anggota:           f.Members, Dompet: f.Wallets, Transaksi: f.Txs,
	}
	if !f.CreatedAt.IsZero() {
		out.DibuatPada = f.CreatedAt.In(a.loc).Format("2006-01-02 15:04")
	}
	return out
}

// GET /admin/keluarga
func (a *App) adminListFamilies(w http.ResponseWriter, r *http.Request) {
	families, err := a.store.Families(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := make([]familyJSON, len(families))
	for i, f := range families {
		out[i] = a.toFamilyJSON(f)
	}
	writeJSON(w, http.StatusOK, map[string]any{"keluarga": out})
}

type createFamilyRequest struct {
	Nama   string `json:"nama"`
	Kepala struct {
		Nama  string `json:"nama"`
		Sandi string `json:"sandi"`
	} `json:"kepala"`
	KodeDaftar string `json:"kode_daftar"` // ditolak bila diisi; kode harus acak
}

// POST /admin/keluarga
func (a *App) adminCreateFamily(w http.ResponseWriter, r *http.Request) {
	var req createFamilyRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "body bukan JSON yang valid"})
		return
	}

	req.Nama = strings.TrimSpace(req.Nama)
	req.Kepala.Nama = strings.TrimSpace(req.Kepala.Nama)
	req.KodeDaftar = strings.TrimSpace(req.KodeDaftar)

	switch {
	case len(req.Nama) < 2:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "nama keluarga minimal 2 karakter"})
		return
	case len(req.Kepala.Nama) < 2:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "nama kepala keluarga minimal 2 karakter"})
		return
	case len(req.Kepala.Sandi) < 8:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "kata sandi kepala keluarga minimal 8 karakter"})
		return
	}
	if req.KodeDaftar != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "kode daftar dibuat otomatis; jangan isi kode_daftar"})
		return
	}
	req.KodeDaftar = newSignupCode()

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Kepala.Sandi), bcrypt.DefaultCost)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	f, err := a.store.CreateFamily(r.Context(), req.Nama, req.KodeDaftar, req.Kepala.Nama, string(hash))
	if err != nil {
		// Penyebab yang wajar cuma kode daftar kembar, tapi sebabnya tetap
		// dicatat: pernah ada bug yang tersembunyi di balik pesan umum ini.
		log.Printf("gagal membuat keluarga %q: %v", req.Nama, err)
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "gagal membuat keluarga, kemungkinan kode daftar sudah dipakai",
		})
		return
	}
	log.Printf("keluarga baru dibuat lewat API admin: %q (id %d), kepala %q",
		f.Name, f.ID, req.Kepala.Nama)

	writeJSON(w, http.StatusCreated, map[string]any{
		"keluarga": a.toFamilyJSON(f),
		"kepala":   map[string]string{"nama": req.Kepala.Nama, "username": f.HeadUsername},
		"catatan":  "Anggota membuat akun di /daftar lalu bergabung dengan kode_daftar di /mulai.",
	})
}

type patchFamilyRequest struct {
	Nama      string `json:"nama"`
	PutarKode bool   `json:"putar_kode"`
}

// PATCH /admin/keluarga/{id} — ganti nama dan/atau putar kode undangan.
// Memutar kode adalah cara menutup pendaftaran setelah semua anggota masuk;
// anggota yang sudah punya akun tidak terpengaruh karena kode hanya dipakai
// saat mendaftar. Tapi kode itu juga dipakai saat login untuk menentukan
// keluarga, jadi kode barunya perlu dibagikan ulang.
func (a *App) adminPatchFamily(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id keluarga tidak valid"})
		return
	}
	var req patchFamilyRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "body bukan JSON yang valid"})
		return
	}
	req.Nama = strings.TrimSpace(req.Nama)
	if req.Nama == "" && !req.PutarKode {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "tidak ada yang diubah: isi nama, putar_kode, atau keduanya",
		})
		return
	}
	if req.Nama != "" && len(req.Nama) < 2 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "nama keluarga minimal 2 karakter"})
		return
	}

	code := ""
	if req.PutarKode {
		code = newSignupCode()
	}
	f, err := a.store.UpdateFamily(r.Context(), id, req.Nama, code)
	if errors.Is(err, ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "keluarga tidak ditemukan"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	log.Printf("keluarga %q (id %d) diubah lewat API admin (putar kode: %v)", f.Name, f.ID, req.PutarKode)
	writeJSON(w, http.StatusOK, map[string]any{"keluarga": a.toFamilyJSON(f)})
}
