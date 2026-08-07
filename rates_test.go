package main

import (
	"testing"
	"time"
)

// Kurs dari API datang sebagai pecahan; yang disimpan aplikasi adalah rasio dua
// bilangan bulat. Test ini menjaga agar perubahan bentuk itu tidak menggeser
// nominal, termasuk untuk pasangan mata uang yang selisih besarannya ekstrem
// seperti IDR dan USD.
func TestRateFromPrice(t *testing.T) {
	const usdToIDR = 17936.304774

	r, ok := rateFromPrice("USD", "IDR", usdToIDR)
	if !ok {
		t.Fatal("kurs USD->IDR ditolak")
	}
	// $100 senilai Rp1.793.630,48
	if got := Format(r.Convert(10000), "IDR"); got != "Rp1.793.630,48" {
		t.Errorf("$100 = %s, mau Rp1.793.630,48", got)
	}
	if got := r.String(); got != "1 USD = Rp17.936,30" {
		t.Errorf("tampilan kurs = %q", got)
	}

	// Arah sebaliknya harus setara, bukan hasil pembalikan yang kehilangan digit.
	inv, ok := rateFromPrice("IDR", "USD", 1/usdToIDR)
	if !ok {
		t.Fatal("kurs IDR->USD ditolak")
	}
	rp, _ := ParseAmount("1.793.630,48", "IDR")
	if got := inv.Convert(rp); got != 10000 {
		t.Errorf("Rp1.793.630,48 = %s, mau $100,00", Format(got, "USD"))
	}

	// Mata uang tanpa desimal ikut tertangani.
	jpy, ok := rateFromPrice("USD", "JPY", 158.205205)
	if !ok {
		t.Fatal("kurs USD->JPY ditolak")
	}
	if got := Format(jpy.Convert(10000), "JPY"); got != "¥15.821" {
		t.Errorf("$100 = %s, mau ¥15.821", got)
	}
}

func TestRateFromPriceMenolakNilaiTidakMasukAkal(t *testing.T) {
	for _, price := range []float64{0, -1} {
		if _, ok := rateFromPrice("USD", "IDR", price); ok {
			t.Errorf("kurs %v seharusnya ditolak", price)
		}
	}
}

// pairs membangun semua pasangan dari kurs ber-basis USD. Yang diperiksa di
// sini adalah pasangan silang yang tidak melibatkan USD sama sekali.
func TestRatePairs(t *testing.T) {
	s := newRateSource("x")
	s.perUSD = map[string]float64{"USD": 1, "IDR": 17936.304774, "SGD": 1.282913}
	s.fetched = time.Now()

	pairs := s.pairs([]string{"IDR", "USD", "SGD"})
	if len(pairs) != 6 {
		t.Fatalf("jumlah pasangan = %d, mau 6", len(pairs))
	}

	// 1 SGD = 17936,304774 / 1,282913 = Rp13.980,92
	sgd, ok := pairs["SGD>IDR"]
	if !ok {
		t.Fatal("pasangan SGD>IDR tidak dibuat")
	}
	seribu, _ := ParseAmount("1.000,00", "SGD")
	if got := Format(sgd.Convert(seribu), "IDR"); got != "Rp13.980.920,59" {
		t.Errorf("S$1.000 = %s, mau Rp13.980.920,59", got)
	}

	// Mata uang yang tidak ada di respons API dilewati, bukan bikin panik.
	if got := s.pairs([]string{"IDR", "XYZ"}); len(got) != 0 {
		t.Errorf("mata uang tak dikenal menghasilkan %d pasangan", len(got))
	}
}

func TestRateSourceKosong(t *testing.T) {
	// Sebelum API sempat dihubungi, tidak ada kurs sama sekali — bukan kurs nol.
	if got := newRateSource("x").pairs([]string{"IDR", "USD"}); got != nil {
		t.Errorf("sumber kosong menghasilkan %v", got)
	}
	if newRateSource("").enabled() {
		t.Error("URL kosong seharusnya berarti pengambilan kurs dimatikan")
	}
}
