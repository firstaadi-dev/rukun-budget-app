package main

import (
	"net/url"
	"testing"
	"time"
)

// Daftar transaksi dibatasi bulan berjalan secara bawaan, dan seluruh
// perpindahannya lewat satu terjemahan ini. Salah di sini berarti daftar yang
// menampilkan bulan yang bukan diminta — kesalahan yang terbaca sebagai data
// hilang, bukan sebagai penyaring yang meleset.
func TestBacaPeriode(t *testing.T) {
	jakarta := time.FixedZone("WIB", 7*3600)
	today := time.Date(2026, 8, 20, 0, 0, 0, 0, jakarta)

	p := bacaPeriode("", today)
	if p.Nilai != "2026-08" || p.Label != "Agustus 2026" || !p.BulanIni {
		t.Errorf("bawaan = %s / %s / bulan ini %v, mau 2026-08 / Agustus 2026 / true",
			p.Nilai, p.Label, p.BulanIni)
	}
	// From inklusif, To eksklusif: transaksi tanggal 1 September tidak boleh
	// ikut terhitung sebagai Agustus.
	if got := p.From.Format("2006-01-02"); got != "2026-08-01" {
		t.Errorf("From = %s, mau 2026-08-01", got)
	}
	if got := p.To.Format("2006-01-02"); got != "2026-09-01" {
		t.Errorf("To = %s, mau 2026-09-01", got)
	}
	if p.Prev != "2026-07" || p.Next != "2026-09" {
		t.Errorf("tetangga = %s dan %s, mau 2026-07 dan 2026-09", p.Prev, p.Next)
	}

	// Nilai yang tidak terbaca jatuh ke bulan berjalan. Penyaring salah ketik
	// sebaiknya menampilkan tampilan bawaan, bukan daftar kosong yang terlihat
	// persis seperti catatan yang hilang.
	if got := bacaPeriode("bulan-lalu", today).Nilai; got != "2026-08" {
		t.Errorf("periode ngawur = %s, mau jatuh ke 2026-08", got)
	}

	// Batas tahun: mundur dari Januari harus menyeberang ke Desember.
	jan := bacaPeriode("2026-01", today)
	if jan.Prev != "2025-12" || jan.Next != "2026-02" || jan.BulanIni {
		t.Errorf("Januari = %s dan %s (bulan ini %v), mau 2025-12 dan 2026-02 (false)",
			jan.Prev, jan.Next, jan.BulanIni)
	}

	semua := bacaPeriode(periodeSemua, today)
	if !semua.Semua || !semua.From.IsZero() || !semua.To.IsZero() {
		t.Errorf("seluruh waktu masih membawa batas: %v–%v", semua.From, semua.To)
	}
}

// Pindah penyaring tidak boleh diam-diam melepas penyaring lain yang sedang
// dipakai: itu membuat daftar berubah karena hal yang tidak diminta.
func TestTxURL(t *testing.T) {
	q := url.Values{
		"jenis": {"expense"}, "kategori": {"Belanja"},
		"cari": {"listrik"}, "periode": {"2026-07"},
		"asing": {"jangan-ikut"},
	}

	got := txURL(q, "periode", "2026-06")
	want := "/transaksi?cari=listrik&jenis=expense&kategori=Belanja&periode=2026-06"
	if got != want {
		t.Errorf("ganti periode = %s, mau %s", got, want)
	}

	// Nilai kosong berarti melepas penyaring itu, bukan mengirimnya kosong.
	if got := txURL(url.Values{"cari": {"listrik"}}, "cari", ""); got != "/transaksi" {
		t.Errorf("hapus pencarian = %s, mau /transaksi", got)
	}
}
