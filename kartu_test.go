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
