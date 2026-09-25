package main

import (
	"net/http/httptest"
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

	p := bacaPeriode("", "", "", today)
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
	if got := bacaPeriode("bulan-lalu", "", "", today).Nilai; got != "2026-08" {
		t.Errorf("periode ngawur = %s, mau jatuh ke 2026-08", got)
	}

	// Batas tahun: mundur dari Januari harus menyeberang ke Desember.
	jan := bacaPeriode("2026-01", "", "", today)
	if jan.Prev != "2025-12" || jan.Next != "2026-02" || jan.BulanIni {
		t.Errorf("Januari = %s dan %s (bulan ini %v), mau 2025-12 dan 2026-02 (false)",
			jan.Prev, jan.Next, jan.BulanIni)
	}

	semua := bacaPeriode(periodeSemua, "", "", today)
	if !semua.Semua || !semua.From.IsZero() || !semua.To.IsZero() {
		t.Errorf("seluruh waktu masih membawa batas: %v–%v", semua.From, semua.To)
	}
}

// Rentang tanggal sendiri. Yang paling mudah salah di sini bukan rentang yang
// lengkap, melainkan ujungnya: "sampai 15 Agustus" yang diam-diam berhenti di
// 14 Agustus akan menyembunyikan sehari penuh transaksi tanpa memberi tanda.
func TestBacaPeriodeRentang(t *testing.T) {
	jakarta := time.FixedZone("WIB", 7*3600)
	today := time.Date(2026, 8, 20, 0, 0, 0, 0, jakarta)
	iso := func(t time.Time) string { return t.Format(formatTanggal) }

	p := bacaPeriode("", "2026-07-03", "2026-08-15", today)
	if !p.Rentang || p.Semua || p.BulanIni {
		t.Errorf("rentang = %+v, mau Rentang saja yang menyala", p)
	}
	if p.Label != "3 Juli 2026 – 15 Agustus 2026" {
		t.Errorf("label = %q, mau 3 Juli 2026 – 15 Agustus 2026", p.Label)
	}
	// Sampai inklusif di layar, eksklusif di query: To harus sehari sesudahnya.
	if iso(p.From) != "2026-07-03" || iso(p.To) != "2026-08-16" {
		t.Errorf("batas = %s..%s, mau 2026-07-03..2026-08-16", iso(p.From), iso(p.To))
	}
	// Kolom tanggalnya diisi ulang dari sini, jadi keduanya harus kembali apa
	// adanya — bukan bentuk To yang sudah digeser.
	if p.Dari != "2026-07-03" || p.Sampai != "2026-08-15" {
		t.Errorf("isi kolom = %s dan %s, mau 2026-07-03 dan 2026-08-15", p.Dari, p.Sampai)
	}
	// Rentang khusus tidak punya bulan tetangga: panahnya sengaja tidak ada.
	if p.Prev != "" || p.Next != "" {
		t.Errorf("rentang punya tetangga %q dan %q, mau kosong", p.Prev, p.Next)
	}

	// Sehari saja: From dan To tidak boleh bertemu, kalau tidak daftarnya kosong.
	// Tanggalnya juga cukup disebut sekali di label.
	satu := bacaPeriode("", "2026-07-03", "2026-07-03", today)
	if iso(satu.From) != "2026-07-03" || iso(satu.To) != "2026-07-04" {
		t.Errorf("sehari = %s..%s, mau 2026-07-03..2026-07-04", iso(satu.From), iso(satu.To))
	}
	if satu.Label != "3 Juli 2026" {
		t.Errorf("label sehari = %q, mau 3 Juli 2026", satu.Label)
	}

	// Tertukar dibetulkan, bukan ditolak jadi daftar kosong.
	balik := bacaPeriode("", "2026-08-15", "2026-07-03", today)
	if balik.Label != "3 Juli 2026 – 15 Agustus 2026" || iso(balik.To) != "2026-08-16" {
		t.Errorf("tertukar = %q sampai %s, mau dibalik jadi 3 Juli–15 Agustus", balik.Label, iso(balik.To))
	}

	// Satu ujung saja: sisanya tetap terbuka, dan From/To yang kosong berarti
	// tanpa batas di sisi itu.
	sejak := bacaPeriode("", "2026-07-03", "", today)
	if !sejak.Rentang || iso(sejak.From) != "2026-07-03" || !sejak.To.IsZero() || sejak.Label != "Sejak 3 Juli 2026" {
		t.Errorf("sejak = %q, %s..%v", sejak.Label, iso(sejak.From), sejak.To)
	}
	hingga := bacaPeriode("", "", "2026-08-15", today)
	if !hingga.Rentang || !hingga.From.IsZero() || iso(hingga.To) != "2026-08-16" || hingga.Label != "Sampai 15 Agustus 2026" {
		t.Errorf("sampai = %q, %v..%s", hingga.Label, hingga.From, iso(hingga.To))
	}

	// Rentang menang atas periode bulanan: keduanya menjawab pertanyaan yang
	// sama, dan yang tanggalnya ditulis sendiri yang lebih spesifik.
	menang := bacaPeriode("2026-01", "2026-07-03", "2026-08-15", today)
	if !menang.Rentang || iso(menang.From) != "2026-07-03" {
		t.Errorf("rentang kalah oleh periode: %+v", menang)
	}

	// Tanggal yang tidak terbaca bukan rentang, dan jatuh ke bulan berjalan —
	// bukan ke daftar kosong.
	for _, ngawur := range []string{"kemarin", "2026-13-40", "03/07/2026"} {
		if p := bacaPeriode("", ngawur, "", today); p.Rentang || p.Nilai != "2026-08" {
			t.Errorf("dari=%q jadi rentang %v / periode %s, mau jatuh ke 2026-08", ngawur, p.Rentang, p.Nilai)
		}
	}
}

// Pindah penyaring tidak boleh diam-diam melepas penyaring lain yang sedang
// dipakai: itu membuat daftar berubah karena hal yang tidak diminta.
func TestTxURL(t *testing.T) {
	q := url.Values{
		"jenis": {"expense"}, "kategori": {"Belanja"},
		"dompet": {"7"},
		"cari":   {"listrik"}, "periode": {"2026-07"},
		"asing": {"jangan-ikut"},
	}

	got := txURL(q, "periode", "2026-06")
	want := "/transaksi?cari=listrik&dompet=7&jenis=expense&kategori=Belanja&periode=2026-06"
	if got != want {
		t.Errorf("ganti periode = %s, mau %s", got, want)
	}

	// Nilai kosong berarti melepas penyaring itu, bukan mengirimnya kosong.
	if got := txURL(url.Values{"cari": {"listrik"}}, "cari", ""); got != "/transaksi" {
		t.Errorf("hapus pencarian = %s, mau /transaksi", got)
	}
}

func TestTxURLCursor(t *testing.T) {
	q := url.Values{"periode": {"semua"}, "cursor": {"2026-08-01:50"}, "cari": {"gaji"}}
	if got := txURL(q, "cursor", "2026-07-01:10"); got != "/transaksi?cari=gaji&cursor=2026-07-01%3A10&periode=semua" {
		t.Errorf("next page lost filters: %s", got)
	}
	if got := txURL(q, "cari", "baru"); got != "/transaksi?cari=baru&periode=semua" {
		t.Errorf("filter change kept stale cursor: %s", got)
	}
}

// Ketiga penyaring waktu menjawab pertanyaan yang sama. Yang satu harus melepas
// dua sisanya, kalau tidak rentang yang masih menempel akan mengalahkan bulan
// yang baru dipilih — tautan yang ditekan tapi tidak mengubah apa pun.
func TestTxURLWaktuSalingLepas(t *testing.T) {
	rentang := url.Values{
		"jenis": {"expense"}, "cari": {"listrik"},
		"dari": {"2026-07-03"}, "sampai": {"2026-08-15"},
	}
	got := txURL(rentang, "periode", "2026-06")
	want := "/transaksi?cari=listrik&jenis=expense&periode=2026-06"
	if got != want {
		t.Errorf("pindah ke bulan = %s, mau %s", got, want)
	}
	if got := txURL(rentang, "periode", periodeSemua); got != "/transaksi?cari=listrik&jenis=expense&periode=semua" {
		t.Errorf("ke seluruh waktu = %s, rentangnya masih ikut", got)
	}
	// Arah sebaliknya: memasang rentang harus melepas periode bulanannya.
	bulan := url.Values{"kategori": {"Belanja"}, "periode": {"2026-06"}}
	if got := txURL(bulan, "dari", "2026-07-03"); got != "/transaksi?dari=2026-07-03&kategori=Belanja" {
		t.Errorf("pasang rentang = %s, periode bulanannya masih ikut", got)
	}
	// Melepas periode berarti melepas seluruh penyaring waktu sekaligus: itulah
	// tautan "Kembali ke bulan ini".
	if got := txURL(rentang, "periode", ""); got != "/transaksi?cari=listrik&jenis=expense" {
		t.Errorf("kembali ke bulan ini = %s, masih membawa batas waktu", got)
	}
}

func TestLocalReturnURL(t *testing.T) {
	r := httptest.NewRequest("GET", "http://example.test/transaksi/baru", nil)
	if got := localReturnURL(r, "http://example.test/transaksi?dompet=7"); got != "/transaksi?dompet=7" {
		t.Errorf("URL lokal = %s", got)
	}
	if got := localReturnURL(r, "https://evil.example/ambil-data"); got != "" {
		t.Errorf("URL eksternal = %s, mau kosong", got)
	}
}

func TestAdjustmentTx(t *testing.T) {
	w := Wallet{ID: 4, Currency: "IDR", BalanceMinor: 100_000}
	today := time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)

	in, ok := adjustmentTx(w, 125_000, today)
	if !ok || in.Kind != "income" || in.AmountMinor != 25_000 || in.WalletID != 4 {
		t.Fatalf("income adjustment = %+v, %v", in, ok)
	}
	out, ok := adjustmentTx(w, 75_000, today)
	if !ok || out.Kind != "expense" || out.AmountMinor != 25_000 {
		t.Fatalf("expense adjustment = %+v, %v", out, ok)
	}
	if _, ok := adjustmentTx(w, 100_000, today); ok {
		t.Fatal("saldo sama seharusnya tidak membuat transaksi")
	}
}
