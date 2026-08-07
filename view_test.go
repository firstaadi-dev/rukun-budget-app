package main

import "testing"

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
