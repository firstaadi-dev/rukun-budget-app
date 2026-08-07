package main

import (
	"fmt"
	"sort"
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

func awalBulan(t time.Time) time.Time {
	y, m, _ := t.Date()
	return time.Date(y, m, 1, 0, 0, 0, 0, t.Location())
}

func namaBulan(t time.Time) string {
	return fmt.Sprintf("%s %d", bulanID[int(t.Month())], t.Year())
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

// ---------- ringkasan per kategori ----------

type CategoryRow struct {
	Name    string
	Amount  string
	Percent int // porsi terhadap kategori terbesar, untuk panjang bilah
}

type CategoryBreakdown struct {
	Periode     string
	Expense     []CategoryRow
	Income      []CategoryRow
	TotalOut    string
	TotalIn     string
	Unconverted []string
}

// breakdown mengelompokkan pengeluaran dan pemasukan per kategori, dikonversi
// ke mata uang dasar. Sama seperti total dompet, kategori yang kursnya belum
// diketahui dilewati dan mata uangnya dilaporkan, bukan diam-diam dianggap nol.
func breakdown(spend []CategorySpend, rates map[string]Rate, base, periode string) CategoryBreakdown {
	out := map[string]int64{}
	in := map[string]int64{}
	seen := map[string]bool{}
	var missing []string

	for _, s := range spend {
		amount := s.Minor
		if s.Currency != base {
			r, ok := rates[s.Currency+">"+base]
			if !ok || !r.Valid() {
				if !seen[s.Currency] {
					seen[s.Currency] = true
					missing = append(missing, s.Currency)
				}
				continue
			}
			amount = r.Convert(amount)
		}
		if s.Kind == "income" {
			in[s.Category] += amount
		} else {
			out[s.Category] += amount
		}
	}

	expenseRows, totalOut := categoryRows(out, base)
	incomeRows, totalIn := categoryRows(in, base)
	return CategoryBreakdown{
		Periode:     periode,
		Expense:     expenseRows,
		Income:      incomeRows,
		TotalOut:    Format(totalOut, base),
		TotalIn:     Format(totalIn, base),
		Unconverted: missing,
	}
}

// categoryRows mengurutkan dari nominal terbesar dan menghitung porsi tiap
// kategori terhadap yang terbesar, supaya bilahnya bisa dibandingkan sekilas.
func categoryRows(sums map[string]int64, base string) ([]CategoryRow, int64) {
	rows := make([]CategoryRow, 0, len(sums))
	var total, max int64
	for name, amount := range sums {
		rows = append(rows, CategoryRow{Name: name, Amount: Format(amount, base)})
		total += amount
		if amount > max {
			max = amount
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if sums[rows[i].Name] != sums[rows[j].Name] {
			return sums[rows[i].Name] > sums[rows[j].Name]
		}
		return rows[i].Name < rows[j].Name
	})
	if max > 0 {
		for i := range rows {
			rows[i].Percent = int(sums[rows[i].Name] * 100 / max)
		}
	}
	return rows, total
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
