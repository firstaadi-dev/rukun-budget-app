package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPaymentTxsPiutangLebih(t *testing.T) {
	p := Party{ID: 3, Name: "Budi", Saldo: []PartyBalance{{Currency: "IDR", PiutangMinor: 58_000_000}}}
	payment := Tx{Kind: "loan_in", Date: time.Now(), PartyID: p.ID, WalletID: 7, WalletCur: "IDR", AmountMinor: 60_000_000, Note: "Terima kasih"}
	txs, err := paymentTxs(p, payment, "piutang")
	if err != nil || len(txs) != 2 {
		t.Fatalf("paymentTxs() = %+v, %v; mau pelunasan dan profit", txs, err)
	}
	repaid, profit := txs[0], txs[1]
	if repaid.Kind != "loan_in" || repaid.AmountMinor != 58_000_000 || repaid.PartyID != p.ID || repaid.Note != payment.Note {
		t.Fatalf("pelunasan = %+v", repaid)
	}
	if profit.Kind != "income" || profit.AmountMinor != 2_000_000 || profit.Category != "Profit Piutang" || profit.PartyID != 0 || !strings.Contains(profit.Note, p.Name) || !strings.Contains(profit.Note, payment.Note) {
		t.Fatalf("profit = %+v", profit)
	}
	if repaid.WalletID != payment.WalletID || profit.WalletID != payment.WalletID || profit.WalletCur != payment.WalletCur || !profit.Date.Equal(payment.Date) {
		t.Fatalf("dompet atau tanggal berubah: %+v", txs)
	}
	if repaid.AmountMinor+profit.AmountMinor != payment.AmountMinor || p.Saldo[0].PiutangMinor-repaid.AmountMinor != 0 {
		t.Fatalf("saldo akhir piutang atau pemasukan dompet salah: %+v", txs)
	}
}

func TestPaymentTxsOverpaymentWithoutWallet(t *testing.T) {
	p := Party{Name: "Budi", Saldo: []PartyBalance{{Currency: "IDR", PiutangMinor: 100_000}}}
	_, err := paymentTxs(p, Tx{Kind: "loan_in", WalletCur: "IDR", AmountMinor: 125_000}, "piutang")
	if err == nil || !strings.Contains(err.Error(), "dompet") {
		t.Fatalf("mau validasi dompet, dapat %v", err)
	}
}

func TestPaymentTxsNonPositiveBalance(t *testing.T) {
	for _, balance := range []int64{0, -25_000} {
		p := Party{Name: "Budi", Saldo: []PartyBalance{{Currency: "IDR", PiutangMinor: balance}}}
		if txs, err := paymentTxs(p, Tx{Kind: "loan_in", WalletID: 7, WalletCur: "IDR", AmountMinor: 125_000}, "piutang"); err == nil {
			t.Fatalf("saldo %d masih dapat dilunasi: %+v", balance, txs)
		}
	}
}

func TestPaymentTxsBalances(t *testing.T) {
	p := Party{ID: 3, Name: "Budi", Saldo: []PartyBalance{{Currency: "IDR", HutangMinor: 100_000, PiutangMinor: 100_000}}}
	for _, tc := range []struct {
		name, direction, currency string
		amount                    int64
		wantError                 bool
	}{
		{"cicilan piutang", "piutang", "IDR", 50_000, false},
		{"piutang lunas", "piutang", "IDR", 100_000, false},
		{"hutang lunas", "hutang", "IDR", 100_000, false},
		{"hutang lebih", "hutang", "IDR", 125_000, true},
		{"mata uang berbeda", "piutang", "USD", 100, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := Tx{Kind: arahPembayaran[tc.direction], WalletCur: tc.currency, AmountMinor: tc.amount}
			got, err := paymentTxs(p, tx, tc.direction)
			if (err != nil) != tc.wantError {
				t.Fatalf("paymentTxs() = %+v, %v", got, err)
			}
			if !tc.wantError && (len(got) != 1 || !reflect.DeepEqual(got[0], tx)) {
				t.Fatalf("pembayaran berubah: %+v", got)
			}
		})
	}
}

func TestDebtCardsKeepNegativeBalances(t *testing.T) {
	parties := []Party{
		{ID: 1, Name: "Lebih Bayar", Saldo: []PartyBalance{{Currency: "IDR", PiutangMinor: -25_000}, {Currency: "USD", HutangMinor: 500, PiutangMinor: 1000}}},
		{ID: 2, Name: "Lunas", Saldo: []PartyBalance{{Currency: "IDR"}}},
		{ID: 3, Name: "Hutang Lebih Bayar", Saldo: []PartyBalance{{Currency: "IDR", HutangMinor: -10_000}}},
	}
	summary := summarizeDebts(parties, nil, "IDR")
	cards := viewDebtCards(summary.Parties, []Tx{{PartyID: 1, WalletCur: "IDR", Kind: "loan_out", AmountMinor: 100_000}, {PartyID: 1, WalletCur: "IDR", Kind: "loan_in", AmountMinor: 125_000}})
	if len(cards) != 5 {
		t.Fatalf("kartu = %+v, mau empat saldo nonzero dan satu pihak lunas", cards)
	}
	if cards[0].Direction != "piutang" || !cards[0].Overpaid || cards[0].Remaining != Format(-25_000, "IDR") || cards[0].PaidPercent != 100 {
		t.Fatalf("kartu lebih bayar = %+v", cards[0])
	}
	if summary.Parties[0].Kosong || !summary.Parties[1].Kosong || summary.HutangParties != 2 || summary.PiutangParties != 1 {
		t.Fatalf("ringkasan = %+v", summary)
	}
	if summary.TotalPiutang != Format(-25_000, "IDR") {
		t.Fatalf("total piutang = %s", summary.TotalPiutang)
	}
	var output bytes.Buffer
	err := parsePages()["hutang.html"].ExecuteTemplate(&output, "layout.html", map[string]any{
		"Summary": summary, "DebtCards": cards, "DebtCounts": map[string]int{"hutang": 2, "piutang": 2, "lunas": 1}, "Nav": "hutang", "Family": "Keluarga",
	})
	if err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, want := range []string{"Lebih bayar", cards[0].Remaining, `data-filter-value="piutang"`, `href="/hutang/pihak/1"`, "Semua (5)", "Orang Berhutang (2)"} {
		if !strings.Contains(html, want) {
			t.Errorf("halaman tidak memuat %q", want)
		}
	}
	if strings.Contains(html, `href="/hutang/pihak/1/bayar?arah=piutang" aria-label="Lihat riwayat`) {
		t.Error("lebih bayar masih menawarkan penerimaan dana")
	}
}

func TestSettledPartyVisibility(t *testing.T) {
	parties := []Party{
		{ID: 1, Name: "Lunas Terlihat", Saldo: []PartyBalance{{Currency: "IDR"}, {Currency: "USD"}}},
		{ID: 2, Name: "Lunas Tersembunyi", Hidden: true},
		{ID: 3, Name: "Saldo Baru", Hidden: true, Saldo: []PartyBalance{{Currency: "USD", PiutangMinor: 100}}},
		{ID: 4, Name: "Dua Arah", Saldo: []PartyBalance{{Currency: "IDR", HutangMinor: 100, PiutangMinor: 100}}},
	}
	summary := summarizeDebts(parties, nil, "IDR")
	cards := viewDebtCards(summary.Parties, nil)
	if len(cards) != 4 || cards[0].ID != 1 || cards[0].Direction != "lunas" || !cards[0].Kosong || cards[0].Remaining != "0" {
		t.Fatalf("kartu terlihat = %+v", cards)
	}
	if cards[1].ID != 3 || cards[1].Kosong || len(summary.HiddenParties) != 1 || summary.HiddenParties[0].ID != 2 {
		t.Fatalf("saldo aktif tersembunyi atau daftar tersembunyi salah: %+v, %+v", cards, summary.HiddenParties)
	}
	var out bytes.Buffer
	err := parsePages()["hutang.html"].ExecuteTemplate(&out, "layout.html", map[string]any{
		"Summary": summary, "DebtCards": cards, "DebtCounts": map[string]int{"lunas": 1, "hutang": 1, "piutang": 2}, "Family": "Keluarga", "Nav": "hutang",
	})
	if err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, want := range []string{`data-filter-value="lunas"`, "Lunas (1)", `action="/hutang/pihak/1/sembunyikan"`, "Sembunyikan dari daftar", "Pihak disembunyikan (1)", `action="/hutang/pihak/2/tampilkan"`, "Tampilkan kembali"} {
		if !strings.Contains(html, want) {
			t.Errorf("halaman tidak memuat %q", want)
		}
	}
	for _, unwanted := range []string{`/hutang/pihak/1/bayar`, `action="/hutang/pihak/3/sembunyikan"`, `action="/hutang/pihak/4/sembunyikan"`} {
		if strings.Contains(html, unwanted) {
			t.Errorf("aksi yang tidak sesuai status: %s", unwanted)
		}
	}
}
