package main

import (
	"fmt"
	"time"
)

var (
	hariID  = [...]string{"Minggu", "Senin", "Selasa", "Rabu", "Kamis", "Jumat", "Sabtu"}
	bulanID = [...]string{"", "Januari", "Februari", "Maret", "April", "Mei", "Juni",
		"Juli", "Agustus", "September", "Oktober", "November", "Desember"}
)

func tanggalPanjang(t time.Time) string {
	return fmt.Sprintf("%s, %d %s %d", hariID[int(t.Weekday())], t.Day(), bulanID[int(t.Month())], t.Year())
}

func tanggalPendek(t time.Time) string {
	return fmt.Sprintf("%d %s %d", t.Day(), bulanID[int(t.Month())], t.Year())
}

// labelTanggal: "Hari ini" / "Kemarin" / "5 Agustus 2026".
//
// Perbandingannya harus per tanggal kalender, bukan selisih durasi: kolom DATE
// dari Postgres kembali sebagai tengah malam UTC, sedangkan "hari ini" memakai
// zona waktu keluarga. Mengurangkan keduanya sebagai waktu meleset sampai
// sehari penuh — transaksi kemarin bisa tampil sebagai hari ini.
func labelTanggal(d, today time.Time) string {
	switch days := int(hari(today).Sub(hari(d)).Hours() / 24); {
	case days == 0:
		return "Hari ini"
	case days == 1:
		return "Kemarin"
	default:
		return tanggalPendek(d)
	}
}

// hari membuang komponen jam dan zona, menyisakan tanggal kalendernya saja.
func hari(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// ---------- view model ----------

type WalletView struct {
	Wallet
	Balance  string
	Negative bool
}

func viewWallet(w Wallet) WalletView {
	return WalletView{Wallet: w, Balance: Format(w.BalanceMinor, w.Currency), Negative: w.BalanceMinor < 0}
}

func viewWallets(ws []Wallet) []WalletView {
	out := make([]WalletView, len(ws))
	for i, w := range ws {
		out[i] = viewWallet(w)
	}
	return out
}

type TxView struct {
	Tx
	Desc        string // "Belanja — Belanja mingguan" atau catatan transfer
	ShortDesc   string
	WalletLabel string // "Tunai" atau "BCA → Tabungan USD"
	DateLabel   string
	Amount      string // sudah bertanda: "+Rp15.000.000" / "-Rp450.000"
	Tone        string // in | out | neutral, dipakai sebagai kelas CSS
	RateLabel   string
}

func viewTx(t Tx, today time.Time) TxView {
	v := TxView{Tx: t, DateLabel: labelTanggal(t.Date, today)}
	switch t.Kind {
	case "transfer":
		v.Desc = t.Note
		if v.Desc == "" {
			v.Desc = "Transfer antar dompet"
		}
		v.ShortDesc = v.Desc
		v.WalletLabel = t.WalletName + " → " + t.ToWalletName
		v.Amount = "-" + Format(t.AmountMinor, t.WalletCur)
		v.Tone = "neutral"
		v.RateLabel = t.Rate().String()
	case "income":
		v.Desc = t.Category
		if t.Note != "" {
			v.Desc += " — " + t.Note
		}
		v.ShortDesc = t.Category
		v.WalletLabel = t.WalletName
		v.Amount = "+" + Format(t.AmountMinor, t.WalletCur)
		v.Tone = "in"
	default:
		v.Desc = t.Category
		if t.Note != "" {
			v.Desc += " — " + t.Note
		}
		v.ShortDesc = t.Category
		v.WalletLabel = t.WalletName
		v.Amount = "-" + Format(t.AmountMinor, t.WalletCur)
		v.Tone = "out"
	}
	return v
}

func viewTxs(ts []Tx, today time.Time) []TxView {
	out := make([]TxView, len(ts))
	for i, t := range ts {
		out[i] = viewTx(t, today)
	}
	return out
}

type TxGroup struct {
	Label string
	Items []TxView
}

func groupTxs(vs []TxView) []TxGroup {
	var out []TxGroup
	for _, v := range vs {
		if n := len(out); n > 0 && out[n-1].Label == v.DateLabel {
			out[n-1].Items = append(out[n-1].Items, v)
			continue
		}
		out = append(out, TxGroup{Label: v.DateLabel, Items: []TxView{v}})
	}
	return out
}

// ---------- ringkasan dashboard ----------

type Summary struct {
	TotalOwned  string
	TotalDebit  string
	TotalCredit string
	Base        string
	// Unconverted: mata uang yang saldonya tidak bisa ikut dijumlahkan karena
	// belum pernah ada transfer yang menetapkan kursnya.
	Unconverted []string
}

// summarize menjumlahkan semua dompet ke mata uang dasar. Dompet bermata uang
// lain dikonversi memakai kurs transfer terakhir; kalau belum ada kursnya,
// dompet itu dilewati dan mata uangnya dilaporkan ke user — lebih baik angka
// yang jujur kurang lengkap daripada total yang diam-diam salah.
func summarize(ws []Wallet, rates map[string]Rate, base string) Summary {
	var debit, credit int64
	seen := map[string]bool{}
	var missing []string

	for _, w := range ws {
		amount := w.BalanceMinor
		if w.Currency != base {
			r, ok := rates[w.Currency+">"+base]
			if !ok || !r.Valid() {
				if !seen[w.Currency] {
					seen[w.Currency] = true
					missing = append(missing, w.Currency)
				}
				continue
			}
			amount = r.Convert(amount)
		}
		if w.IsCredit() {
			credit += -amount // saldo kartu kredit negatif = terpakai
		} else {
			debit += amount
		}
	}

	return Summary{
		TotalOwned:  Format(debit-credit, base),
		TotalDebit:  Format(debit, base),
		TotalCredit: Format(credit, base),
		Base:        base,
		Unconverted: missing,
	}
}
