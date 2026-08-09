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

// kartuTempo: satu akun kredit yang jatuh temponya sekian hari dari hari ini.
// hariLagi negatif berarti sudah lewat.
func kartuTempo(id int64, nama string, hariLagi int, payable, limit int64, today time.Time) CardView {
	wl := Wallet{ID: id, Name: nama, Type: "credit", Currency: "IDR",
		BalanceMinor: -payable, SettlementDay: 25, PaymentDay: 15, LimitMinor: limit}
	due := today.AddDate(0, 0, hariLagi)
	return viewCard(CardStatus{
		Wallet: wl, HasCycle: true, PayableMinor: payable, OutstandingMinor: wl.BalanceMinor,
		Settlement: due.AddDate(0, 0, -20), Due: due,
	}, today)
}

// Kartu yang paling mendesak harus berada paling depan: deret kartu di
// dashboard menggulung ke samping, dan yang di ujung praktis tidak terbaca.
func TestUrutkanKartu(t *testing.T) {
	today := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)

	// Sengaja dibuat dalam urutan terbalik dari yang seharusnya.
	limitTipis := kartuTempo(4, "Limit Tipis", 20, 95_000_000, 100_000_000, today)
	cards := []CardView{
		kartuTempo(1, "Santai", 25, 10_000_000, 500_000_000, today),
		limitTipis,
		kartuTempo(3, "Besok", 1, 200_000_000, 0, today),
		kartuTempo(2, "Telat", -3, 300_000_000, 0, today),
	}
	urutkanKartu(cards)

	mau := []string{"Telat", "Besok", "Limit Tipis", "Santai"}
	for i, nama := range mau {
		if cards[i].Name != nama {
			t.Errorf("urutan ke-%d = %s, mau %s", i, cards[i].Name, nama)
		}
	}
}

// Peringatan hanya untuk kartu yang benar-benar butuh tindakan, dan kalimatnya
// harus bisa dibaca tanpa menghitung sendiri sisa harinya.
func TestPerhatian(t *testing.T) {
	today := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)

	lunas := kartuTempo(9, "Lunas", 2, 0, 0, today)
	rows := perhatian([]CardView{
		kartuTempo(1, "Telat", -3, 300_000_000, 0, today),
		kartuTempo(2, "Besok", 1, 200_000_000, 0, today),
		kartuTempo(3, "Hari Ini", 0, 150_000_000, 0, today),
		kartuTempo(4, "Limit Tipis", 25, 95_000_000, 100_000_000, today),
		kartuTempo(5, "Santai", 25, 10_000_000, 500_000_000, today),
		lunas,
	})

	if len(rows) != 4 {
		t.Fatalf("jumlah peringatan = %d, mau 4 (yang santai dan yang lunas tidak ikut)", len(rows))
	}
	mau := []struct{ nama, pesan, nominal string }{
		{"Telat", "Tagihan telat 3 hari", "Rp3.000.000"},
		{"Besok", "Tagihan jatuh tempo besok", "Rp2.000.000"},
		{"Hari Ini", "Tagihan jatuh tempo hari ini", "Rp1.500.000"},
		{"Limit Tipis", "Limit terpakai 95%", "sisa Rp50.000"},
	}
	for i, w := range mau {
		if rows[i].Nama != w.nama || rows[i].Pesan != w.pesan || rows[i].Nominal != w.nominal {
			t.Errorf("baris ke-%d = %q / %q / %q, mau %q / %q / %q",
				i, rows[i].Nama, rows[i].Pesan, rows[i].Nominal, w.nama, w.pesan, w.nominal)
		}
	}

	// Baris limit menipis tidak boleh menawarkan tombol bayar: yang jadi soal
	// di situ pemakaiannya, bukan tagihan yang jatuh tempo.
	if rows[3].PayablePlain != "" {
		t.Errorf("baris limit menipis menawarkan pembayaran %q", rows[3].PayablePlain)
	}
	if rows[0].PayablePlain == "" {
		t.Error("baris tagihan telat tidak membawa nominal untuk form pembayaran")
	}
}
