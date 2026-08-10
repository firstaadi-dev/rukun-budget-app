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
