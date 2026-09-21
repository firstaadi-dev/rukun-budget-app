package main

import (
	"testing"
	"time"
)

func TestPaymentTxsPiutangLebih(t *testing.T) {
	p := Party{ID: 3, Name: "Budi", Saldo: []PartyBalance{{Currency: "IDR", PiutangMinor: 100_000}}}
	payment := Tx{
		Kind: "loan_in", Date: time.Now(), PartyID: p.ID,
		WalletID: 7, WalletCur: "IDR", AmountMinor: 125_000,
	}

	txs, err := paymentTxs(p, payment, "piutang")
	if err != nil || len(txs) != 2 {
		t.Fatalf("paymentTxs() = %+v, %v", txs, err)
	}
	adjustment := txs[0]
	if adjustment.Kind != "loan_out" || adjustment.AmountMinor != 25_000 || adjustment.WalletID != 0 {
		t.Fatalf("penyesuaian = %+v", adjustment)
	}
	if txs[1].Kind != "loan_in" || txs[1].AmountMinor != 125_000 || txs[1].WalletID != 7 {
		t.Fatalf("penerimaan = %+v", txs[1])
	}
	if final := p.Saldo[0].PiutangMinor + adjustment.AmountMinor - txs[1].AmountMinor; final != 0 {
		t.Fatalf("saldo piutang akhir = %d, mau 0", final)
	}
}
