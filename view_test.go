package main

import (
	"testing"
	"time"
)

// Ringkasan per kategori harus mengurutkan dari yang terbesar, mengubah semua
// mata uang ke mata uang dasar, dan menyebutkan mata uang yang kursnya belum
// diketahui alih-alih menghitungnya sebagai nol.
func TestBreakdown(t *testing.T) {
	rates := map[string]Rate{
		"USD>IDR": {FromMinor: 100000, From: "USD", ToMinor: 1_550_000_000, To: "IDR"},
	}
	spend := []CategorySpend{
		{Kind: "expense", Category: "Belanja", Currency: "IDR", Minor: 45_000_000}, // Rp450.000
		{Kind: "expense", Category: "Tagihan", Currency: "IDR", Minor: 90_000_000}, // Rp900.000
		{Kind: "expense", Category: "Belanja", Currency: "USD", Minor: 1000},       // $10 = Rp155.000
		{Kind: "expense", Category: "Liburan", Currency: "SGD", Minor: 50000},      // kurs belum ada
		{Kind: "income", Category: "Gaji", Currency: "IDR", Minor: 1_500_000_000},  // Rp15.000.000
	}

	b := breakdown(spend, rates, "IDR", "Agustus 2026")

	if len(b.Expense) != 2 {
		t.Fatalf("kategori pengeluaran = %d, mau 2 (Liburan dilewati)", len(b.Expense))
	}
	if b.Expense[0].Name != "Tagihan" {
		t.Errorf("urutan pertama = %s, mau Tagihan (nominal terbesar)", b.Expense[0].Name)
	}
	if b.Expense[0].Percent != 100 {
		t.Errorf("porsi terbesar = %d, mau 100", b.Expense[0].Percent)
	}
	// Belanja: Rp450.000 + $10 dikonversi Rp155.000 = Rp605.000
	if b.Expense[1].Name != "Belanja" || b.Expense[1].Amount != "Rp605.000" {
		t.Errorf("Belanja = %s %s, mau Belanja Rp605.000", b.Expense[1].Name, b.Expense[1].Amount)
	}
	if b.TotalOut != "Rp1.505.000" {
		t.Errorf("total pengeluaran = %s, mau Rp1.505.000", b.TotalOut)
	}
	if b.TotalIn != "Rp15.000.000" {
		t.Errorf("total pemasukan = %s, mau Rp15.000.000", b.TotalIn)
	}
	if len(b.Unconverted) != 1 || b.Unconverted[0] != "SGD" {
		t.Errorf("mata uang tanpa kurs = %v, mau [SGD]", b.Unconverted)
	}
}

// Ringkasan hutang piutang menjumlahkan seluruh pihak ke mata uang dasar, dan
// menyebutkan mata uang yang kursnya belum diketahui alih-alih diam.
func TestSummarizeDebts(t *testing.T) {
	rates := map[string]Rate{
		"USD>IDR": {FromMinor: 100000, From: "USD", ToMinor: 1_550_000_000, To: "IDR"},
	}
	parties := []Party{
		{ID: 1, Name: "Pak Budi", Saldo: []PartyBalance{
			{Currency: "IDR", HutangMinor: 500_000_000, PiutangMinor: 200_000_000}, // Rp5jt / Rp2jt
		}},
		{ID: 2, Name: "Bu Sari", Saldo: []PartyBalance{
			{Currency: "IDR", HutangMinor: 100_000_000}, // Rp1jt
			{Currency: "USD", PiutangMinor: 10000},      // $100 = Rp1.550.000
			{Currency: "SGD", PiutangMinor: 50000},      // kurs belum ada
		}},
		{ID: 3, Name: "Sudah Lunas"},
	}

	s := summarizeDebts(parties, rates, "IDR")

	if s.TotalHutang != "Rp6.000.000" {
		t.Errorf("total hutang = %s, mau Rp6.000.000", s.TotalHutang)
	}
	// Rp2jt ditambah $100 yang dikonversi jadi Rp1.550.000.
	if s.TotalPiutang != "Rp3.550.000" {
		t.Errorf("total piutang = %s, mau Rp3.550.000", s.TotalPiutang)
	}
	if s.Net != "-Rp2.450.000" || s.NetTone != "out" {
		t.Errorf("selisih = %s (%s), mau -Rp2.450.000 (out)", s.Net, s.NetTone)
	}
	if len(s.Unconverted) != 1 || s.Unconverted[0] != "SGD" {
		t.Errorf("mata uang tanpa kurs = %v, mau [SGD]", s.Unconverted)
	}
	if len(s.Parties) != 3 {
		t.Fatalf("jumlah pihak = %d, mau 3", len(s.Parties))
	}
	// Pihak yang saldonya nol tetap ditampilkan, ditandai sebagai lunas.
	if !s.Parties[2].Kosong {
		t.Error("pihak tanpa saldo seharusnya ditandai Kosong")
	}
	if s.Parties[0].Baris[0].NetTone != "out" {
		t.Errorf("Pak Budi net tone = %s, mau out", s.Parties[0].Baris[0].NetTone)
	}
}

// Jenis hutang piutang harus tampil dengan tanda dan label yang benar,
// termasuk saat tidak menyentuh dompet mana pun.
func TestViewTxHutang(t *testing.T) {
	today := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)

	pinjam := viewTx(Tx{
		Kind: "debt_in", Date: today, WalletID: 1, WalletName: "Rekening Utama",
		WalletCur: "IDR", AmountMinor: 500_000_000, PartyID: 1, PartyName: "Pak Budi",
	}, today)
	if pinjam.Amount != "+Rp5.000.000" || pinjam.Tone != "in" {
		t.Errorf("terima pinjaman = %s (%s), mau +Rp5.000.000 (in)", pinjam.Amount, pinjam.Tone)
	}
	if pinjam.ShortDesc != "Hutang · Pak Budi" {
		t.Errorf("judul = %q", pinjam.ShortDesc)
	}

	tanpaDompet := viewTx(Tx{
		Kind: "debt_in", Date: today, WalletCur: "IDR", AmountMinor: 100_000_000,
		PartyID: 2, PartyName: "Bu Sari",
	}, today)
	if tanpaDompet.WalletLabel != "tanpa dompet" {
		t.Errorf("label dompet = %q, mau \"tanpa dompet\"", tanpaDompet.WalletLabel)
	}

	bayar := viewTx(Tx{
		Kind: "debt_pay", Date: today, WalletID: 1, WalletName: "Rekening Utama",
		WalletCur: "IDR", AmountMinor: 200_000_000, PartyID: 1, PartyName: "Pak Budi",
	}, today)
	if bayar.Amount != "-Rp2.000.000" || bayar.Tone != "out" {
		t.Errorf("bayar hutang = %s (%s), mau -Rp2.000.000 (out)", bayar.Amount, bayar.Tone)
	}
}
