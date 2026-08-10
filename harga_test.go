package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Inti perubahan ini: host utama yang membalas 429 tidak lagi berarti tidak
// ada harga. Yang dijaga di sini dua hal — host cadangannya benar-benar dipakai,
// dan sesudah 429 sumber utama berhenti dihubungi untuk sementara supaya
// penolakannya tidak diperpanjang sendiri.
func TestHargaJatuhKeCadangan(t *testing.T) {
	var yahooHit int
	yahoo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		yahooHit++
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"finance":{"error":{"code":"Too Many Requests"}}}`))
	}))
	defer yahoo.Close()

	cadangan := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"chart":{"result":[{"meta":{"currency":"USD","symbol":"VOO",` +
			`"shortName":"Vanguard S&P 500 ETF","regularMarketPrice":611.34,` +
			`"regularMarketTime":1786132800}}]}}`))
	}))
	defer cadangan.Close()

	s := newHargaSource(yahoo.URL+"/", cadangan.URL+"/")
	out := s.harga(context.Background(), []string{"VOO"})
	if k := out["VOO"]; !k.Ada() || k.PriceE4 != 611_34*priceScale {
		t.Fatalf("harga = %+v, mau terisi dari cadangan", k)
	}
	if err := s.status(); err != nil {
		t.Errorf("status = %v, mau bersih karena cadangannya berhasil", err)
	}
	if yahooHit != 1 {
		t.Fatalf("sumber utama dihubungi %d kali, mau 1", yahooHit)
	}

	// Simbol kedua: sumber utama sedang dijeda, jadi ia tidak dihubungi lagi.
	if !s.sedangDiam() {
		t.Fatal("429 tidak menjeda sumber utama")
	}
	s.harga(context.Background(), []string{"QQQ"})
	if yahooHit != 1 {
		t.Errorf("sumber utama dihubungi %d kali, mau tetap 1 selama jeda", yahooHit)
	}

	// Tombol "Ambil Ulang Harga" mengabaikan jeda itu: permintaannya datang
	// dari orangnya sendiri, bukan dari halaman yang kebetulan dibuka.
	s.segarkan(context.Background(), []string{"VOO"})
	if yahooHit != 2 {
		t.Errorf("sumber utama dihubungi %d kali sesudah ambil ulang, mau 2", yahooHit)
	}
}

// Kalau dua-duanya gagal, pesannya harus menyebut keduanya — "status 429" saja
// tidak memberi tahu apakah cadangannya sudah dicoba.
func TestHargaDuaSumberGagal(t *testing.T) {
	mati := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer mati.Close()

	s := newHargaSource(mati.URL+"/", mati.URL+"/")
	ctx, batal := context.WithTimeout(context.Background(), 5*time.Second)
	defer batal()
	if out := s.harga(ctx, []string{"VOO"}); len(out) != 0 {
		t.Errorf("harga = %+v, mau kosong", out)
	}
	err := s.status()
	if err == nil {
		t.Fatal("status bersih padahal dua sumber gagal")
	}
	for _, sebut := range []string{"utama", "cadangan", "500"} {
		if !strings.Contains(err.Error(), sebut) {
			t.Errorf("pesan %q tidak menyebut %q", err, sebut)
		}
	}
}

// Twelve Data adalah sumber ketiga, dipakai saat kedua host Yahoo menolak.
// Bentuk balasannya berbeda dari Yahoo dalam dua hal yang mudah terlewat:
// harganya dikirim sebagai teks, dan galatnya dijawab di dalam badan 200.
func TestFetchTwelve(t *testing.T) {
	var diminta string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		diminta = r.URL.Query().Get("symbol")
		if r.URL.Query().Get("apikey") != "rahasia" {
			w.Write([]byte(`{"code":401,"message":"apikey salah","status":"error"}`))
			return
		}
		w.Write([]byte(`{"symbol":"SPUS","name":"SP Funds S&P 500 Sharia ETF",` +
			`"currency":"USD","datetime":"2026-08-08","timestamp":1786132800,` +
			`"open":"48.10","high":"48.55","low":"47.90","close":"48.42"}`))
	}))
	defer srv.Close()

	s := newHargaSource("", "")
	s.twelveURL, s.twelveKey = srv.URL, "rahasia"

	k, err := s.fetchTwelve(context.Background(), "SPUS")
	if err != nil {
		t.Fatalf("SPUS: %v", err)
	}
	if k.PriceE4 != 48_42*priceScale || k.Currency != "USD" {
		t.Errorf("kuotasi = %d %s, mau %d USD", k.PriceE4, k.Currency, 48_42*priceScale)
	}
	if k.Nama != "SP Funds S&P 500 Sharia ETF" {
		t.Errorf("nama = %q", k.Nama)
	}
	if k.Diambil || k.At.IsZero() {
		t.Errorf("waktu = %v (diambil %v), mau waktu bursa dari sumbernya", k.At, k.Diambil)
	}

	// Emas: kontrak berjangka COMEX tidak ada di sini, jadi diterjemahkan ke
	// emas spot. Tanpa itu, posisi emas kehilangan sumber ketiganya diam-diam.
	if _, err := s.fetchTwelve(context.Background(), simbolEmas); err != nil {
		t.Fatalf("emas: %v", err)
	}
	if diminta != "XAU/USD" {
		t.Errorf("simbol emas diminta sebagai %q, mau XAU/USD", diminta)
	}

	// Galat dijawab 200 dengan status "error" di badan. Diperlakukan sebagai
	// berhasil, ia akan tersimpan sebagai harga nol.
	s.twelveKey = "salah"
	if _, err := s.fetchTwelve(context.Background(), "SPUS"); err == nil {
		t.Error("galat di badan 200 diterima, mau ditolak")
	}
}

// Kunci ada di URL, jadi ia tidak boleh ikut ke pesan galat — pesan itu tampil
// di layar dan tercatat di log server.
func TestTwelveKunciTidakBocor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Unauthorized: "+r.URL.String(), http.StatusUnauthorized)
	}))
	defer srv.Close()

	s := newHargaSource("", "")
	s.twelveURL, s.twelveKey = srv.URL, "rahasia-sekali"

	_, err := s.fetchTwelve(context.Background(), "SPUS")
	if err == nil {
		t.Fatal("401 diterima, mau ditolak")
	}
	if strings.Contains(err.Error(), "rahasia-sekali") {
		t.Errorf("kunci ikut di pesan galat: %v", err)
	}
}

// Penyegar harian dijadwalkan tepat ke pukul 00:00 berikutnya, di zona waktu
// aplikasi. Salah di sini berarti pengambilannya jatuh di jam sibuk sumbernya.
func TestSampaiTengahMalam(t *testing.T) {
	jkt := time.FixedZone("WIB", 7*3600)
	cases := []struct {
		dari string
		mau  time.Duration
	}{
		{"2026-08-10 00:00:00", 24 * time.Hour},
		{"2026-08-10 23:30:00", 30 * time.Minute},
		{"2026-08-10 12:00:00", 12 * time.Hour},
	}
	for _, c := range cases {
		now, err := time.ParseInLocation("2006-01-02 15:04:05", c.dari, jkt)
		if err != nil {
			t.Fatal(err)
		}
		if got := sampaiTengahMalam(now); got != c.mau {
			t.Errorf("dari %s = %v, mau %v", c.dari, got, c.mau)
		}
		// Ke mana pun ia jatuh, hasilnya harus tepat tengah malam.
		if tiba := now.Add(sampaiTengahMalam(now)); tiba.Hour() != 0 || tiba.Minute() != 0 {
			t.Errorf("dari %s tiba di %s, mau tepat 00:00", c.dari, tiba)
		}
	}
}

// Pengambilan borongan: sepuluh posisi cukup satu permintaan, bukan sepuluh.
// Yang paling mudah salah di sini adalah mata uangnya — balasan spark tidak
// membawanya sama sekali, jadi ia harus datang dari yang sudah diketahui, dan
// simbol yang belum pernah terlihat harus dilewati alih-alih ditebak.
func TestFetchSparkBorongan(t *testing.T) {
	var minta int
	var simbolDiminta string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		minta++
		simbolDiminta = r.URL.Query().Get("symbols")
		w.Write([]byte(`{
			"VOO":{"symbol":"VOO","timestamp":[1786023000,1786109400],"close":[706.4,710.71]},
			"SPUS":{"symbol":"SPUS","timestamp":[1786023000,1786109400],"close":[58.68,59.16]},
			"BARU":{"symbol":"BARU","timestamp":[1786109400],"close":[10.0]}
		}`))
	}))
	defer srv.Close()

	s := newHargaSource(srv.URL+"/chart/", "")
	// VOO dan SPUS pernah terlihat, BARU belum.
	s.seed(map[string]Kuotasi{
		"VOO":  {PriceE4: 700_00 * priceScale, Currency: "USD", Nama: "Vanguard S&P 500 ETF"},
		"SPUS": {PriceE4: 58_00 * priceScale, Currency: "USD"},
	})

	out, err := s.fetchSpark(context.Background(), s.base, []string{"VOO", "SPUS", "BARU"})
	if err != nil {
		t.Fatalf("borongan: %v", err)
	}
	if minta != 1 {
		t.Errorf("permintaan = %d, mau 1 untuk tiga simbol", minta)
	}
	if simbolDiminta != "VOO,SPUS,BARU" {
		t.Errorf("simbol diminta = %q", simbolDiminta)
	}
	if k := out["VOO"]; k.PriceE4 != 710_71*priceScale || k.Currency != "USD" {
		t.Errorf("VOO = %d %s, mau %d USD", k.PriceE4, k.Currency, 710_71*priceScale)
	}
	// Nama yang sudah diketahui tidak boleh hilang hanya karena spark tidak
	// menyebutkannya.
	if out["VOO"].Nama != "Vanguard S&P 500 ETF" {
		t.Errorf("nama VOO = %q, mau tetap terisi", out["VOO"].Nama)
	}
	if _, ada := out["BARU"]; ada {
		t.Error("simbol tanpa mata uang diketahui ikut terbawa, mau dilewati")
	}
}

// Deret penutupan bisa berakhir dengan nol untuk hari bursa yang belum tutup.
// Memakai elemen terakhir apa adanya membuat harga jatuh ke nol tiap pagi.
func TestPenutupanTerakhir(t *testing.T) {
	tutup, waktu, ok := penutupanTerakhir([]float64{700, 710.71, 0}, []int64{1, 1786109400, 3})
	if !ok || tutup != 710.71 {
		t.Errorf("penutupan = %v (%v), mau 710,71", tutup, ok)
	}
	if waktu.Unix() != 1786109400 {
		t.Errorf("waktu = %v, mau mengikuti harga yang dipakai", waktu)
	}
	if _, _, ok := penutupanTerakhir([]float64{0, 0}, []int64{1, 2}); ok {
		t.Error("deret tanpa harga diterima, mau ditolak")
	}
	if _, _, ok := penutupanTerakhir(nil, nil); ok {
		t.Error("deret kosong diterima, mau ditolak")
	}
}

// harga() menyegarkan yang basi di latar belakang. Yang dijaga: penyegaran itu
// satu permintaan borongan, bukan satu per simbol.
func TestHargaBasiDisegarkanSekaliJalan(t *testing.T) {
	var spark, chart int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "spark") {
			spark++
			w.Write([]byte(`{"VOO":{"symbol":"VOO","timestamp":[1786109400],"close":[710.71]},
				"SPUS":{"symbol":"SPUS","timestamp":[1786109400],"close":[59.16]}}`))
			return
		}
		chart++
		w.Write([]byte(`{"chart":{"result":[{"meta":{"currency":"USD","symbol":"X",` +
			`"regularMarketPrice":1.0,"regularMarketTime":1786109400}}]}}`))
	}))
	defer srv.Close()

	s := newHargaSource(srv.URL+"/v8/finance/chart/", "")
	s.seed(map[string]Kuotasi{
		"VOO":  {PriceE4: 700_00 * priceScale, Currency: "USD"},
		"SPUS": {PriceE4: 58_00 * priceScale, Currency: "USD"},
	})

	// Keduanya ada di cache tapi tanpa waktu pengambilan, jadi terhitung basi.
	out := s.harga(context.Background(), []string{"VOO", "SPUS"})
	if len(out) != 2 {
		t.Fatalf("harga = %+v, mau dua simbol langsung dari cache", out)
	}

	// Penyegarannya berjalan di goroutine sendiri.
	tunggu := time.Now().Add(3 * time.Second)
	for time.Now().Before(tunggu) {
		if s.harga(context.Background(), []string{"VOO"})["VOO"].PriceE4 == 710_71*priceScale {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := s.harga(context.Background(), []string{"VOO"})["VOO"].PriceE4; got != 710_71*priceScale {
		t.Fatalf("VOO sesudah disegarkan = %d, mau %d", got, 710_71*priceScale)
	}
	if spark != 1 {
		t.Errorf("permintaan spark = %d, mau 1 untuk dua simbol basi", spark)
	}
	if chart != 0 {
		t.Errorf("permintaan chart = %d, mau 0 — keduanya sudah terjawab borongan", chart)
	}
}
