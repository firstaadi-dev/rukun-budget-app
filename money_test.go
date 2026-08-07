package main

import "testing"

func TestParseAmount(t *testing.T) {
	cases := []struct {
		in, cur string
		want    int64
	}{
		{"1.600.000", "IDR", 160_000_000},
		{"1234,56", "IDR", 123456},
		{"5.000.000", "IDR", 500_000_000},
		{"100.00", "USD", 10000},
		{"1,000.00", "USD", 100000},
		{"12000", "JPY", 12000},
		{"Rp450.000", "IDR", 45_000_000},
		{"-2.100.000", "IDR", -210_000_000},
	}
	for _, c := range cases {
		got, err := ParseAmount(c.in, c.cur)
		if err != nil || got != c.want {
			t.Errorf("ParseAmount(%q,%s) = %d, %v; mau %d", c.in, c.cur, got, err, c.want)
		}
	}
	for _, bad := range []string{"abc", "", "  ", "1.2.x"} {
		if _, err := ParseAmount(bad, "IDR"); err == nil {
			t.Errorf("ParseAmount(%q) harus error", bad)
		}
	}
}

func TestFormat(t *testing.T) {
	cases := []struct {
		minor     int64
		cur, want string
	}{
		{160_000_000, "IDR", "Rp1.600.000"},
		{123456, "IDR", "Rp1.234,56"},
		{-210_000_000, "IDR", "-Rp2.100.000"},
		{100000, "USD", "$1.000,00"},
		{12000, "JPY", "¥12.000"},
		{0, "IDR", "Rp0"},
	}
	for _, c := range cases {
		if got := Format(c.minor, c.cur); got != c.want {
			t.Errorf("Format(%d,%s) = %q; mau %q", c.minor, c.cur, got, c.want)
		}
	}
}

// Skenario handoff: keluar Rp15.550.000 dari BCA, diterima $1.000 di Mandiri
// USD, kurs efektif Rp15.500/USD, biaya admin Rp50.000.
func TestTransferHandoffScenario(t *testing.T) {
	out, _ := ParseAmount("15.550.000", "IDR")
	in, _ := ParseAmount("1,000.00", "USD")

	rate, err := ParseUnitRate("15.500", "IDR", "USD", "IDR", "USD")
	if err != nil {
		t.Fatal(err)
	}

	fee := AdminFee(out, in, "IDR", "USD", rate)
	if fee != 5_000_000 { // Rp50.000
		t.Fatalf("AdminFee = %s, mau Rp50.000", Format(fee, "IDR"))
	}

	eff := EffectiveRate(out, fee, in, "IDR", "USD")
	if got := eff.String(); got != "1 USD = Rp15.500" {
		t.Fatalf("EffectiveRate = %q", got)
	}

	// Kurs efektif harus bolak-balik eksak — ini yang dulu bocor waktu kurs
	// disimpan sebagai angka ter-skala.
	if back := eff.Convert(out - fee); back != in {
		t.Fatalf("round-trip = %d, mau %d", back, in)
	}
	if fwd := eff.Invert().Convert(in); fwd != out-fee {
		t.Fatalf("round-trip balik = %d, mau %d", fwd, out-fee)
	}
}

// Nominal diterima hanya bisa dicatat sampai satuan terkecil mata uangnya,
// jadi hasil bagi yang tidak bulat selalu menyisakan selisih. Test ini mengunci
// besarnya: di bawah nilai satu sen tujuan, dan bertanda positif kalau nominal
// diterima dibulatkan ke bawah seperti yang dilakukan skrip form.
func TestPembulatanNominalDiterima(t *testing.T) {
	out, _ := ParseAmount("15.500.000", "IDR")
	rate, err := ParseUnitRate("17.936,31", "IDR", "USD", "IDR", "USD")
	if err != nil {
		t.Fatal(err)
	}

	// Rp15.500.000 / 17.936,31 = $864,1697…
	pas := rate.Convert(out)
	if got := Format(pas, "USD"); got != "$864,17" {
		t.Fatalf("konversi langsung = %s, mau $864,17 (pembulatan terdekat)", got)
	}

	// Dibulatkan ke bawah ke sen terdekat: biaya admin wajib tidak negatif.
	bawah, _ := ParseAmount("864,16", "USD")
	if fee := AdminFee(out, bawah, "IDR", "USD", rate); fee < 0 {
		t.Fatalf("pembulatan ke bawah menghasilkan biaya admin negatif: %d", fee)
	}

	// Dibulatkan ke atas: sedikit negatif, tapi harus tetap di dalam toleransi
	// sebesar nilai satu sen tujuan — inilah yang dinolkan oleh readTx.
	atas, _ := ParseAmount("864,17", "USD")
	fee := AdminFee(out, atas, "IDR", "USD", rate)
	slack := rate.Invert().Convert(1)
	if fee >= 0 {
		t.Fatalf("pembulatan ke atas seharusnya sedikit negatif, dapat %d", fee)
	}
	if -fee > slack {
		t.Fatalf("selisih %d melebihi nilai satu sen (%d)", -fee, slack)
	}
}

func TestRateSameCurrencyAndFee(t *testing.T) {
	// Transfer sesama IDR: admin = selisih murni. Rp100.000 - Rp99.000.
	if fee := AdminFee(10_000_000, 9_900_000, "IDR", "IDR", Rate{}); fee != 100_000 {
		t.Fatalf("AdminFee sama-mata-uang = %d, mau 100000", fee)
	}
	if r := (Rate{}); r.String() != "—" {
		t.Fatalf("Rate kosong = %q", r.String())
	}
}

func TestRateUnitDirection(t *testing.T) {
	// JPY punya 0 desimal — arah tampilan harus tetap benar.
	r := Rate{FromMinor: 1_000_000, From: "IDR", ToMinor: 95, To: "JPY"} // Rp10.000 = ¥95
	if got := r.String(); got != "1 JPY = Rp105,26" {
		t.Fatalf("Rate.String() = %q", got)
	}
	if !r.SameAs(Rate{FromMinor: 2_000_000, From: "IDR", ToMinor: 190, To: "JPY"}) {
		t.Fatal("rasio 2x harus dianggap kurs yang sama")
	}
}

func TestNoPrecisionDrift(t *testing.T) {
	// 10.000 transaksi Rp0,01 harus persis Rp100,00 — inti alasan pakai int64.
	var sum int64
	for range 10000 {
		v, _ := ParseAmount("0,01", "IDR")
		sum += v
	}
	if sum != 10000 {
		t.Fatalf("akumulasi = %d, mau 10000", sum)
	}
}
