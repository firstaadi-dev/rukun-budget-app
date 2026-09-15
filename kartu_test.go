package main

import (
	"testing"
	"time"
)

var wib = time.FixedZone("WIB", 7*3600)

func tgl(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, wib)
}

func TestSettlementTerakhir(t *testing.T) {
	cases := []struct {
		nama  string
		today time.Time
		hari  int
		mau   time.Time
	}{
		{"sebelum tanggal cetak, ambil bulan lalu", tgl(2026, 8, 10), 25, tgl(2026, 7, 25)},
		{"sesudah tanggal cetak, ambil bulan ini", tgl(2026, 8, 28), 25, tgl(2026, 8, 25)},
		{"tepat pada tanggal cetak", tgl(2026, 8, 25), 25, tgl(2026, 8, 25)},
		{"lewat tahun", tgl(2026, 1, 3), 25, tgl(2025, 12, 25)},
		// Kartu dengan tanggal cetak 31 tidak boleh melompat ke 3 Maret.
		{"tanggal 31 di bulan Februari", tgl(2026, 2, 20), 31, tgl(2026, 1, 31)},
		{"tanggal 31 sesudah akhir Februari", tgl(2026, 3, 1), 31, tgl(2026, 2, 28)},
	}
	for _, c := range cases {
		got := SettlementTerakhir(c.today, c.hari, wib)
		if !got.Equal(c.mau) {
			t.Errorf("%s: dapat %s, mau %s", c.nama,
				got.Format("2006-01-02"), c.mau.Format("2006-01-02"))
		}
	}
}

func TestJatuhTempo(t *testing.T) {
	cases := []struct {
		nama       string
		settlement time.Time
		hariBayar  int
		mau        time.Time
	}{
		{"bayar sesudah cetak, bulan sama", tgl(2026, 8, 5), 20, tgl(2026, 8, 20)},
		{"bayar sebelum cetak, bulan berikutnya", tgl(2026, 8, 25), 15, tgl(2026, 9, 15)},
		{"bayar sama dengan cetak, bulan berikutnya", tgl(2026, 8, 25), 25, tgl(2026, 9, 25)},
		{"lewat tahun", tgl(2026, 12, 25), 15, tgl(2027, 1, 15)},
		// Tanggal bayar 31 masih di bulan yang sama selama lebih besar dari
		// tanggal cetak, walaupun tenggangnya cuma beberapa hari.
		{"tanggal 31 sesudah cetak tanggal 25", tgl(2026, 1, 25), 31, tgl(2026, 1, 31)},
		// Dan dijepit ke akhir bulan saat bulannya pendek.
		{"tanggal 31 dijepit di Februari", tgl(2026, 2, 1), 31, tgl(2026, 2, 28)},
	}
	for _, c := range cases {
		got := JatuhTempo(c.settlement, c.hariBayar, wib)
		if !got.Equal(c.mau) {
			t.Errorf("%s: dapat %s, mau %s", c.nama,
				got.Format("2006-01-02"), c.mau.Format("2006-01-02"))
		}
	}
}

func TestTagihanKartu(t *testing.T) {
	// Saldo kartu negatif saat terpakai, mengikuti konvensi saldo dompet.
	rp := func(juta float64) int64 { return int64(juta * 100_000_000) }

	cases := []struct {
		nama                       string
		atSettlement, creditsSince int64
		mau                        int64
	}{
		{"belum dibayar sama sekali", -rp(2), 0, rp(2)},
		{"sudah dibayar sebagian", -rp(2), rp(0.5), rp(1.5)},
		{"sudah lunas", -rp(2), rp(2), 0},
		{"lebih bayar tidak jadi tagihan negatif", -rp(2), rp(3), 0},
		{"kartu belum terpakai", 0, 0, 0},
	}
	for _, c := range cases {
		if got := TagihanKartu(c.atSettlement, c.creditsSince); got != c.mau {
			t.Errorf("%s: dapat %s, mau %s", c.nama,
				Format(got, "IDR"), Format(c.mau, "IDR"))
		}
	}
}

// Limit menentukan berapa lagi yang boleh dipakai. Saldo akun kredit negatif
// saat dipakai, jadi arah tandanya mudah tertukar — dikunci di sini.
func TestSisaLimit(t *testing.T) {
	rp := func(juta float64) int64 { return int64(juta * 100_000_000) }

	cases := []struct {
		nama           string
		saldo, limit   int64
		terpakai, sisa int64
		persen         int
	}{
		{"belum dipakai", 0, rp(10), 0, rp(10), 0},
		{"terpakai sebagian", -rp(3), rp(10), rp(3), rp(7), 30},
		{"terpakai penuh", -rp(10), rp(10), rp(10), 0, 100},
		{"lebih bayar jadi saldo positif", rp(1), rp(10), 0, rp(10), 0},
		// Bisa terjadi kalau limit diturunkan setelah kartu terlanjur terpakai.
		{"terpakai melebihi limit", -rp(12), rp(10), rp(12), 0, 100},
	}
	for _, c := range cases {
		w := Wallet{Type: "credit", Currency: "IDR", BalanceMinor: c.saldo, LimitMinor: c.limit}
		if got := w.TerpakaiMinor(); got != c.terpakai {
			t.Errorf("%s: terpakai = %s, mau %s", c.nama, Format(got, "IDR"), Format(c.terpakai, "IDR"))
		}
		if got := w.SisaLimitMinor(); got != c.sisa {
			t.Errorf("%s: sisa limit = %s, mau %s", c.nama, Format(got, "IDR"), Format(c.sisa, "IDR"))
		}
		v := viewCard(CardStatus{Wallet: w, OutstandingMinor: c.saldo}, tgl(2026, 8, 7))
		if v.TerpakaiPersen != c.persen {
			t.Errorf("%s: persen = %d, mau %d", c.nama, v.TerpakaiPersen, c.persen)
		}
	}
}

// PayLater berperilaku sama persis dengan kartu kredit di seluruh aplikasi.
func TestPayLaterSamaDenganKartuKredit(t *testing.T) {
	pl := Wallet{Type: "paylater", Provider: "SPayLater", Currency: "IDR",
		BalanceMinor: -50_000_000, LimitMinor: 200_000_000}
	if !pl.IsCredit() {
		t.Error("PayLater seharusnya dihitung sebagai akun kredit")
	}
	if pl.TypeLabel() != "PayLater" {
		t.Errorf("label = %q", pl.TypeLabel())
	}
	if pl.Subtitle() != "SPayLater · PayLater" {
		t.Errorf("subjudul = %q", pl.Subtitle())
	}
	if got := pl.SisaLimitMinor(); got != 150_000_000 {
		t.Errorf("sisa limit = %s, mau Rp1.500.000", Format(got, "IDR"))
	}

	// Tanpa siklus tagihan, bagian tagihannya disembunyikan — bukan ditebak.
	v := viewCard(CardStatus{Wallet: pl, OutstandingMinor: pl.BalanceMinor}, tgl(2026, 8, 7))
	if v.HasCycle || v.Payable != "" {
		t.Errorf("akun tanpa siklus seharusnya tanpa tagihan, dapat %q", v.Payable)
	}
	if v.Outstanding != "Rp500.000" {
		t.Errorf("sisa pemakaian = %s", v.Outstanding)
	}
}

// Tagihan adalah bagian di dalam nominal terpakai, bukan angka terpisah.
// Selisihnya ditampilkan supaya keduanya bisa dicek silang.
func TestBelumDitagih(t *testing.T) {
	w := Wallet{Type: "credit", Currency: "IDR", BalanceMinor: -123_232_300, LimitMinor: 1_000_000_000}
	st := CardStatus{
		Wallet: w, OutstandingMinor: w.BalanceMinor, HasCycle: true,
		PayableMinor: 43_232_300, // Rp432.323
		Settlement:   tgl(2026, 7, 25), Due: tgl(2026, 8, 15),
	}
	v := viewCard(st, tgl(2026, 8, 7))

	// Rp1.232.323 terpakai, Rp432.323 sudah tertagih -> Rp800.000 belum.
	if v.BelumDitagih != "Rp800.000" {
		t.Errorf("belum ditagih = %q, mau Rp800.000", v.BelumDitagih)
	}
	// Dan ketiganya harus konsisten dengan limitnya.
	if v.Outstanding != "Rp1.232.323" || v.SisaLimit != "Rp8.767.677" {
		t.Errorf("terpakai %s, sisa limit %s", v.Outstanding, v.SisaLimit)
	}

	// Saat seluruh pemakaian sudah tertagih, tidak ada rincian yang perlu
	// ditampilkan — barisnya dikosongkan, bukan ditulis Rp0.
	st.PayableMinor = 123_232_300
	if got := viewCard(st, tgl(2026, 8, 7)).BelumDitagih; got != "" {
		t.Errorf("semua sudah tertagih seharusnya tanpa rincian, dapat %q", got)
	}
}

// Cicilan 6jt x6: bulan ini baru 1jt masuk saldo, tapi limit tertahan 6jt penuh.
func TestCicilanMemotongLimitPenuh(t *testing.T) {
	w := Wallet{Type: "credit", Currency: "IDR", LimitMinor: 1_000_000_000,
		BalanceMinor: -100_000_000, CicilanMendatangMinor: 500_000_000}
	st := CardStatus{Wallet: w, OutstandingMinor: w.BalanceMinor, HasCycle: true,
		PayableMinor: 0, Settlement: tgl(2026, 7, 25), Due: tgl(2026, 8, 15)}
	v := viewCard(st, tgl(2026, 8, 7))
	if v.Outstanding != "Rp6.000.000" || v.SisaLimit != "Rp4.000.000" || v.TerpakaiPersen != 60 {
		t.Errorf("terpakai %s, sisa %s, persen %d", v.Outstanding, v.SisaLimit, v.TerpakaiPersen)
	}
	if v.CicilanMendatang != "Rp5.000.000" || v.BelumDitagih != "Rp1.000.000" {
		t.Errorf("cicilan %q, belum ditagih %q", v.CicilanMendatang, v.BelumDitagih)
	}
}
