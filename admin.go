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
)

// API pengelolaan keluarga sengaja tidak punya UI. Akun dan keluarga baru
// dibuat sendiri oleh pengguna setelah mendaftar lewat Firebase.
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

type patchFamilyRequest struct {
	Nama      string `json:"nama"`
	PutarKode bool   `json:"putar_kode"`
}

// PATCH /admin/keluarga/{id} — ganti nama dan/atau putar kode undangan.
// Memutar kode menutup undangan lama; anggota yang sudah bergabung tetap punya akses.
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
