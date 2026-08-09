package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Potongan tabel yang bentuknya sama dengan yang dikirim sumbernya: baris
// header ber-<th>, baris data ber-<td> dengan nama di kolom ketiga dan NAB di
// keempat, plus baris cacat yang harus dilewati tanpa menjatuhkan sisanya.
const contohNAB = `
<table><tbody>
<tr><th>No</th><th>Pilih</th><th>Reksa Dana</th><th>NAB/UP</th></tr>
<tr><td>&nbsp;</td><td></td><td>Sucorinvest  Money Market Fund</td><td>1980.69</td><td>0.01</td></tr>
<tr><td></td><td></td><td>Sucorinvest Money Market USD</td><td>1.0774</td><td>0.02</td></tr>
<tr><td></td><td></td><td><a href="#">Alpha Dana Ekuitas</a></td><td>1032.7551</td><td>0.03</td></tr>
<tr><td></td><td></td><td>Reksadana Tanpa NAB</td><td>-</td><td>0.04</td></tr>
<tr><td></td><td></td><td></td><td>123.45</td><td>0.05</td></tr>
<tr><td></td><td>kolomnya kurang</td></tr>
</tbody></table>`

func nabPalsu(t *testing.T, isi string) *nabSource {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(isi))
	}))
	t.Cleanup(srv.Close)
	s := newNABSource(srv.URL + "/")
	if err := s.refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	return s
}

// Tabelnya dirakit orang lain dan bisa berubah sewaktu-waktu. Yang dijaga di
// sini: baris cacat dilewati diam-diam, dan yang utuh tetap terbaca — sumber
// yang setengah rusak jangan sampai menghapus seluruh harga.
func TestNABUrai(t *testing.T) {
	s := nabPalsu(t, contohNAB)

	// Enam kategori mengambil isi yang sama, jadi tiap nama muncul sekali di
	// indeksnya dan enam kali di daftarnya. Yang diperiksa indeksnya.
	k, ok := s.nab("Sucorinvest Money Market Fund")
	if !ok {
		t.Fatal("nama dengan spasi ganda tidak ketemu")
	}
	if k.Currency != "IDR" || k.PriceE4 != 1_980_690_000 {
		t.Errorf("NAB = %d %s, mau 1980690000 IDR", k.PriceE4, k.Currency)
	}
	if got := FormatPriceE4(k.PriceE4, k.Currency); got != "Rp1.980,69" {
		t.Errorf("tulis NAB = %q, mau Rp1.980,69", got)
	}

	// Huruf besar-kecil dan spasi berlebih tidak boleh membuat posisi
	// kehilangan harganya.
	if _, ok := s.nab("  sucorinvest   MONEY market fund "); !ok {
		t.Error("pencocokan nama terlalu ketat")
	}

	// Yang berdenominasi dolar dikenali dari namanya. Salah tebak di sini
	// tertahan lapis berikutnya, tapi lebih baik tidak sampai ke sana.
	if k, ok := s.nab("Sucorinvest Money Market USD"); !ok || k.Currency != "USD" {
		t.Errorf("reksadana USD: ketemu %v, mata uang %q", ok, k.Currency)
	}

	// Nama yang terbungkus tautan tetap terbaca bersih.
	if k, ok := s.nab("Alpha Dana Ekuitas"); !ok || k.PriceE4 != 1_032_755_100 {
		t.Errorf("nama dalam <a>: ketemu %v, NAB %d", ok, k.PriceE4)
	}

	// Baris cacat tidak ikut, dan tidak menjatuhkan baris lain.
	if _, ok := s.nab("Reksadana Tanpa NAB"); ok {
		t.Error("baris tanpa NAB ikut tersimpan")
	}
	if _, ok := s.nab(""); ok {
		t.Error("baris tanpa nama ikut tersimpan")
	}
}

func TestNABCari(t *testing.T) {
	s := nabPalsu(t, contohNAB)

	hasil := s.cari("sucorinvest", 5)
	if len(hasil) == 0 {
		t.Fatal("pencarian kosong")
	}
	for _, r := range hasil {
		if !strings.Contains(strings.ToLower(r.Nama), "sucorinvest") {
			t.Errorf("hasil tidak relevan: %q", r.Nama)
		}
	}
	// Batas dihormati: daftar pilihan yang terlalu panjang tidak terbaca sekali
	// lihat.
	if got := len(s.cari("a", 3)); got > 3 {
		t.Errorf("hasil = %d, mau paling banyak 3", got)
	}
	if got := s.cari("", 5); got != nil {
		t.Errorf("ketikan kosong = %v, mau nil", got)
	}
	if got := s.cari("tidak-ada-ini", 5); len(got) != 0 {
		t.Errorf("ketikan ngawur = %v, mau kosong", got)
	}
}

// Sumber yang mati tidak boleh menjatuhkan apa pun: yang hilang cuma harganya.
func TestNABMati(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "mati", http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := newNABSource(srv.URL + "/")
	if err := s.refresh(context.Background()); err == nil {
		t.Error("sumber mati tidak melaporkan error")
	}
	if _, ok := s.nab("apa saja"); ok {
		t.Error("sumber mati malah mengembalikan NAB")
	}
	if got := s.cari("apa", 5); len(got) != 0 {
		t.Errorf("sumber mati malah punya hasil pencarian: %v", got)
	}

	// Dimatikan lewat konfigurasi: tidak ada permintaan sama sekali, dan
	// pemanggilnya tidak perlu memeriksa apa pun lebih dulu.
	off := newNABSource("")
	if _, ok := off.nab("apa saja"); ok {
		t.Error("sumber yang dimatikan malah mengembalikan NAB")
	}
	if off.cari("apa", 5) != nil {
		t.Error("sumber yang dimatikan malah mencari")
	}
}
