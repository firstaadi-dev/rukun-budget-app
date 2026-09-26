package main

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Halaman pengaturan: akun sendiri dan daftar anggota keluarga.
//
// Sebelum ini tidak ada satu pun jalan mengurus akun dari dalam aplikasi. Sandi
// yang bocor hanya bisa diganti lewat akses langsung ke database, dan anggota
// yang sudah keluar dari keluarga tetap bisa masuk selamanya — memutar kode
// undangan cuma menutup pendaftaran baru, tidak menyentuh sesi yang sudah ada.
//
// Halaman ini juga satu-satunya tempat tombol "Keluar" bisa dijangkau dari
// ponsel: sidebar yang memuatnya hanya tampil mulai lebar 900px.

// pesanSukses: konfirmasi setelah aksi yang berhasil. Aksi POST selalu dibalas
// redirect supaya tidak terkirim ulang saat halaman disegarkan, dan penanda
// singkat di URL adalah cara paling murah membawa satu kalimat menyeberanginya.
var pesanSukses = map[string]string{
	"sandi":    "Kata sandi diganti. Perangkat lain yang masih login diminta masuk ulang.",
	"nonaktif": "Akses anggota itu dicabut. Catatan yang pernah dibuatnya tetap utuh.",
	"aktif":    "Akses anggota itu dipulihkan.",
	"kode":     "Kode undangan baru berlaku tujuh hari. Kode lama tidak bisa dipakai lagi.",
	"username": "Nama akun diganti.",
	"tautkan":  "Akun Firebase tertaut. Verifikasi email sebelum masuk menggunakan email; jika tautannya belum tiba, coba masuk dengan email untuk mengirim ulang.",
}

func (a *App) settings(w http.ResponseWriter, r *http.Request) {
	a.renderSettings(w, r, "", http.StatusOK)
}

func (a *App) renderSettings(w http.ResponseWriter, r *http.Request, errMsg string, status int) {
	members, err := a.store.Members(r.Context(), family(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	f, err := a.store.FamilyByID(r.Context(), family(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	a.render(w, r, "pengaturan.html", map[string]any{
		"Title": "Akun", "Nav": "akun",
		"FirebaseEnabled": a.firebase != nil,
		"Members":         members,
		"InviteCode":      f.SignupCode,
		"InviteURL":       inviteURL(r, f.SignupCode),
		"InviteExpires":   tanggalPendek(f.SignupCodeExpiresAt.In(a.loc)),
		"InviteExpired":   !f.SignupCodeExpiresAt.After(time.Now()),
		"Sukses":          pesanSukses[r.URL.Query().Get("ok")],
		"Error":           errMsg,
	})
}

func (a *App) changeUsername(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Username == "rukun:"+strconv.FormatInt(u.ID, 10) {
		a.renderSettings(w, r, "Akun lama harus dimigrasikan admin sebelum nama akun bisa diganti.", http.StatusForbidden)
		return
	}
	username := strings.ToLower(strings.TrimSpace(r.FormValue("username")))
	if !usernamePattern.MatchString(username) {
		a.renderSettings(w, r, "Nama akun harus 3–40 karakter: huruf kecil, angka, titik, garis bawah, atau tanda hubung.", http.StatusUnprocessableEntity)
		return
	}
	if ok, _ := a.verifyCurrentPassword(r.Context(), u, r.FormValue("sandi")); !ok {
		a.renderSettings(w, r, "Kata sandi salah.", http.StatusUnauthorized)
		return
	}
	if err := a.store.SetUsername(r.Context(), userFrom(r.Context()).ID, username); err != nil {
		a.renderSettings(w, r, "Nama akun sudah dipakai.", http.StatusConflict)
		return
	}
	http.Redirect(w, r, "/pengaturan?ok=username", http.StatusSeeOther)
}

func (a *App) linkFirebase(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	fail := func(message string, status int) { a.renderSettings(w, r, message, status) }
	if u.FirebaseUID != "" {
		fail("Akun ini sudah ditautkan ke Firebase.", http.StatusConflict)
		return
	}
	if a.firebase == nil {
		fail("Firebase Auth belum dikonfigurasi.", http.StatusServiceUnavailable)
		return
	}
	email, err := normalizeEmail(r.FormValue("email"))
	if err != nil {
		fail("Masukkan alamat email yang valid.", http.StatusBadRequest)
		return
	}
	password := r.FormValue("sandi_firebase")
	if len(password) < 8 || len(password) > 128 {
		fail("Kata sandi Firebase harus 8–128 karakter.", http.StatusBadRequest)
		return
	}
	if hash, err := a.store.PasswordHash(r.Context(), family(r), u.ID); err != nil ||
		bcrypt.CompareHashAndPassword([]byte(hash), []byte(r.FormValue("sandi_lama"))) != nil {
		fail("Kata sandi akun lama salah.", http.StatusUnauthorized)
		return
	}
	continueURL := a.firebaseContinueURL(r)
	if continueURL == "" {
		fail("APP_URL belum dikonfigurasi.", http.StatusServiceUnavailable)
		return
	}

	token, err := a.firebase.signIn(r.Context(), email, password)
	created := false
	if err != nil {
		fe, ok := err.(firebaseError)
		if !ok || (fe.Code != "INVALID_LOGIN_CREDENTIALS" && fe.Code != "EMAIL_NOT_FOUND" && fe.Code != "USER_NOT_FOUND") {
			fail("Tidak bisa menghubungkan akun Firebase. Periksa email dan kata sandi Firebase.", http.StatusBadGateway)
			return
		}
		token, err = a.firebase.signUp(r.Context(), email, password)
		if err != nil {
			if fe, ok := err.(firebaseError); ok && fe.Code == "EMAIL_EXISTS" {
				fail("Email sudah terdaftar. Masukkan kata sandi Firebase yang benar.", http.StatusConflict)
			} else {
				fail("Akun Firebase tidak bisa dibuat. Periksa email dan kata sandi.", http.StatusBadGateway)
			}
			return
		}
		created = true
	}
	account, err := a.firebase.account(r.Context(), token.IDToken)
	if err != nil || account.LocalID != token.LocalID || !strings.EqualFold(account.Email, email) {
		if created {
			_ = a.firebase.call(r.Context(), "delete", map[string]string{"idToken": token.IDToken}, nil)
		}
		fail("Identitas Firebase tidak dapat diverifikasi.", http.StatusBadGateway)
		return
	}
	if err := a.store.LinkFirebaseUser(r.Context(), u.ID, account.LocalID, email, account.EmailVerified); err != nil {
		if created {
			_ = a.firebase.call(r.Context(), "delete", map[string]string{"idToken": token.IDToken}, nil)
		}
		fail("Email atau akun Firebase sudah terhubung ke akun Rukun lain.", http.StatusConflict)
		return
	}
	if !account.EmailVerified {
		if err := a.firebase.sendVerification(r.Context(), token.IDToken, continueURL); err != nil {
			log.Printf("kirim verifikasi tautan akun gagal untuk user id %d: %v", u.ID, err)
		}
	}
	http.Redirect(w, r, "/pengaturan?ok=tautkan", http.StatusSeeOther)
}

func (a *App) rotateFamilyCode(w http.ResponseWriter, r *http.Request) {
	if !userFrom(r.Context()).Kepala {
		a.renderSettings(w, r, "Hanya kepala keluarga yang bisa mengganti kode.", http.StatusForbidden)
		return
	}
	if ok, _ := a.verifyCurrentPassword(r.Context(), userFrom(r.Context()), r.FormValue("sandi")); !ok {
		a.renderSettings(w, r, "Kata sandi salah.", http.StatusUnauthorized)
		return
	}
	if _, err := a.store.UpdateFamily(r.Context(), family(r), "", newSignupCode()); err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/pengaturan?ok=kode", http.StatusSeeOther)
}

// changePassword mengganti sandi sendiri. Sandi lama tetap diminta meski yang
// meminta sudah terbukti punya sesi: perangkat yang tertinggal dalam keadaan
// login jangan sampai bisa mengunci pemiliknya keluar dari akunnya sendiri.
func (a *App) changePassword(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	lama := r.FormValue("sandi_lama")
	baru := r.FormValue("sandi_baru")
	ulang := r.FormValue("sandi_ulang")

	valid, firebaseToken := a.verifyCurrentPassword(r.Context(), u, lama)
	switch {
	case !valid:
		a.renderSettings(w, r, "Kata sandi lama salah.", http.StatusUnauthorized)
		return
	case len(baru) < 8:
		a.renderSettings(w, r, "Kata sandi baru minimal 8 karakter.", http.StatusBadRequest)
		return
	case baru != ulang:
		a.renderSettings(w, r, "Ulangan kata sandi tidak sama.", http.StatusBadRequest)
		return
	}

	if u.FirebaseUID != "" {
		if err := a.firebase.updatePassword(r.Context(), firebaseToken.IDToken, baru); err != nil {
			a.renderSettings(w, r, "Kata sandi Firebase gagal diperbarui. Coba lagi.", http.StatusBadGateway)
			return
		}
	} else {
		baruHash, err := bcrypt.GenerateFromPassword([]byte(baru), bcrypt.DefaultCost)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		if err := a.store.SetPassword(r.Context(), family(r), u.ID, string(baruHash)); err != nil {
			a.fail(w, r, err)
			return
		}
	}

	// Sandi diganti biasanya justru karena yang lama diduga bocor. Sesi yang
	// sudah berjalan tidak ikut mati sendiri kalau tidak dihapus, jadi sandi
	// baru tanpa langkah ini tidak mengusir siapa pun.
	if err := a.store.DeleteUserSessions(r.Context(), family(r), u.ID); err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.newSession(w, r, u.ID); err != nil {
		a.fail(w, r, err)
		return
	}
	log.Printf("kata sandi diganti: %q (id %d) di keluarga %q", u.Name, u.ID, u.FamilyName)
	http.Redirect(w, r, "/pengaturan?ok=sandi", http.StatusSeeOther)
}

func (a *App) memberDisable(w http.ResponseWriter, r *http.Request) { a.setMember(w, r, false) }
func (a *App) memberEnable(w http.ResponseWriter, r *http.Request)  { a.setMember(w, r, true) }

// setMember mencabut atau memulihkan akses seorang anggota. Hanya kepala
// keluarga yang boleh, dan tidak atas dirinya sendiri — keluarga yang kepalanya
// ikut nonaktif tidak menyisakan siapa pun yang bisa memulihkan yang lain.
func (a *App) setMember(w http.ResponseWriter, r *http.Request, aktif bool) {
	u := userFrom(r.Context())
	if !u.Kepala {
		a.renderSettings(w, r, "Hanya kepala keluarga yang bisa mengubah akses anggota.",
			http.StatusForbidden)
		return
	}
	id := pathID(r)
	if id == u.ID {
		a.renderSettings(w, r, "Akses sendiri tidak bisa dicabut dari sini.", http.StatusBadRequest)
		return
	}

	err := a.store.SetMemberActive(r.Context(), family(r), id, aktif)
	if errors.Is(err, ErrNotFound) {
		a.notFound(w)
		return
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}

	pesan := "aktif"
	if !aktif {
		// Sesi yang sedang berjalan tetap sah sampai dihapus. Tanpa langkah ini
		// pencabutan baru terasa sebulan kemudian, saat sesinya kedaluwarsa.
		if err := a.store.DeleteUserSessions(r.Context(), family(r), id); err != nil {
			a.fail(w, r, err)
			return
		}
		pesan = "nonaktif"
	}
	log.Printf("akses anggota diubah: id %d jadi %s oleh %q di keluarga %q",
		id, pesan, u.Name, u.FamilyName)
	http.Redirect(w, r, "/pengaturan?ok="+pesan, http.StatusSeeOther)
}
