package main

import (
	"errors"
	"log"
	"net/http"
	"time"
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
		"Members":       members,
		"InviteCode":    f.SignupCode,
		"InviteURL":     inviteURL(r, f.SignupCode),
		"InviteExpires": tanggalPendek(f.SignupCodeExpiresAt.In(a.loc)),
		"InviteExpired": !f.SignupCodeExpiresAt.After(time.Now()),
		"Sukses":        pesanSukses[r.URL.Query().Get("ok")],
		"Error":         errMsg,
	})
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

	if err := a.firebase.updatePassword(r.Context(), firebaseToken.IDToken, baru); err != nil {
		a.renderSettings(w, r, "Kata sandi Firebase gagal diperbarui. Coba lagi.", http.StatusBadGateway)
		return
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
