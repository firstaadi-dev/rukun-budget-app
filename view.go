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
	// Card: ringkasan kredit milik dompet ini, kalau ada. Nil untuk dompet
	// biasa — template memakainya untuk memilih baris biasa atau baris yang
	// bisa dibuka.
	Card *CardView
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

// withCards menempelkan ringkasan kredit ke baris dompetnya masing-masing.
// ponytail: pencarian bersarang, satu keluarga tidak akan punya cukup dompet
// untuk membuat map lebih cepat daripada dua lingkaran ini.
func withCards(ws []WalletView, cards []CardView) []WalletView {
	for i := range cards {
		for j := range ws {
			if ws[j].ID == cards[i].ID {
				ws[j].Card = &cards[i]
			}
		}
	}
	return ws
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

// debtLabel: nama jenis hutang piutang seperti yang dibaca user.
var debtLabel = map[string]string{
	"debt_in":  "Hutang",
	"debt_pay": "Bayar Hutang",
	"loan_out": "Piutang",
	"loan_in":  "Terima Piutang",
}

func viewTx(t Tx, today time.Time) TxView {
	v := TxView{Tx: t, DateLabel: labelTanggal(t.Date, today)}

	if t.IsDebt() {
		v.ShortDesc = debtLabel[t.Kind] + " · " + t.PartyName
		v.Desc = v.ShortDesc
		if t.Note != "" {
			v.Desc += " — " + t.Note
		}
		// Tanpa dompet, catatan ini hanya menggerakkan kewajiban dan tidak
		// menyentuh saldo mana pun. Itu perlu terlihat di daftar transaksi,
		// kalau tidak angkanya terbaca seperti uang yang berpindah.
		v.WalletLabel = t.WalletName
		if !t.HasWallet() {
			v.WalletLabel = "tanpa dompet"
		}
		if t.AddsBalance() {
			v.Amount, v.Tone = "+"+Format(t.AmountMinor, t.WalletCur), "in"
		} else {
			v.Amount, v.Tone = "-"+Format(t.AmountMinor, t.WalletCur), "out"
		}
		return v
	}

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

// ---------- kartu kredit ----------

type CardView struct {
	Wallet
	HasCycle bool
	Payable  string
	// PayablePlain: nominal tagihan tanpa simbol mata uang, untuk mengisi
	// otomatis kolom nominal di form pembayaran.
	PayablePlain string
	Outstanding  string
	Settlement   string
	Due          string
	// HariLagi: sisa hari menuju jatuh tempo. Negatif berarti sudah lewat.
	HariLagi int
	Lunas    bool
	// BelumDitagih: bagian dari nominal terpakai yang belum masuk tagihan mana
	// pun. Ditampilkan sebagai rincian, supaya jelas bahwa tagihan bukan angka
	// terpisah dari terpakai melainkan bagian di dalamnya.
	BelumDitagih string

	Limit     string
	SisaLimit string
	// TerpakaiPersen: porsi limit yang sudah terpakai, untuk panjang bilah.
	TerpakaiPersen int
}

func (c CardView) Telat() bool  { return c.HasCycle && !c.Lunas && c.HariLagi < 0 }
func (c CardView) Segera() bool { return c.HasCycle && !c.Lunas && c.HariLagi >= 0 && c.HariLagi <= 5 }

// LimitTipis: pemakaian sudah menyentuh 90% limit. Ditandai sebelum benar-benar
// mentok, karena transaksi yang ditolak di kasir lebih merepotkan daripada
// peringatan yang muncul kecepatan.
func (c CardView) LimitTipis() bool { return c.HasLimit() && c.TerpakaiPersen >= 90 }

func viewCard(st CardStatus, today time.Time) CardView {
	w := st.Wallet
	v := CardView{
		Wallet:      w,
		HasCycle:    st.HasCycle,
		Outstanding: Format(-st.OutstandingMinor, w.Currency),
	}
	if st.HasCycle {
		v.Payable = Format(st.PayableMinor, w.Currency)
		v.PayablePlain = FormatPlain(st.PayableMinor, w.Currency)
		v.Settlement = tanggalPendek(st.Settlement)
		v.Due = tanggalPendek(st.Due)
		v.HariLagi = int(hari(st.Due).Sub(hari(today)).Hours() / 24)
		v.Lunas = st.PayableMinor == 0

		// Tagihan sudah termasuk di dalam nominal terpakai, bukan angka
		// terpisah. Selisihnya ditampilkan supaya keduanya bisa dicek silang
		// tanpa berhitung, dan tidak ada yang keliru menjumlahkannya.
		if sisa := w.TerpakaiMinor() - st.PayableMinor; sisa > 0 {
			v.BelumDitagih = Format(sisa, w.Currency)
		}
	}
	if w.HasLimit() {
		v.Limit = Format(w.LimitMinor, w.Currency)
		v.SisaLimit = Format(w.SisaLimitMinor(), w.Currency)
		v.TerpakaiPersen = int(min(100, w.TerpakaiMinor()*100/w.LimitMinor))
	}
	return v
}

// ---------- hutang dan piutang ----------

type PartyView struct {
	Party
	Baris  []PartyBalanceView
	Kosong bool
}

type PartyBalanceView struct {
	Currency string
	Hutang   string
	Piutang  string
	Net      string
	NetTone  string // out kalau kita yang berhutang, in kalau kita yang menagih
}

func viewParty(p Party) PartyView {
	v := PartyView{Party: p, Kosong: true}
	for _, b := range p.Saldo {
		if b.HutangMinor == 0 && b.PiutangMinor == 0 {
			continue
		}
		v.Kosong = false
		net := b.NetMinor()
		tone := "neutral"
		switch {
		case net > 0:
			tone = "in"
		case net < 0:
			tone = "out"
		}
		v.Baris = append(v.Baris, PartyBalanceView{
			Currency: b.Currency,
			Hutang:   Format(b.HutangMinor, b.Currency),
			Piutang:  Format(b.PiutangMinor, b.Currency),
			Net:      Format(net, b.Currency),
			NetTone:  tone,
		})
	}
	return v
}

type DebtSummary struct {
	Base         string
	TotalHutang  string
	TotalPiutang string
	Net          string
	NetTone      string
	Parties      []PartyView
	Unconverted  []string
}

// summarizeDebts menjumlahkan hutang dan piutang seluruh pihak ke mata uang
// dasar. Sama seperti total dompet, mata uang yang kursnya belum diketahui
// dilewati dan dilaporkan — angka kurang lengkap lebih baik daripada total
// yang diam-diam salah.
func summarizeDebts(parties []Party, rates map[string]Rate, base string) DebtSummary {
	var hutang, piutang int64
	seen := map[string]bool{}
	var missing []string

	views := make([]PartyView, 0, len(parties))
	for _, p := range parties {
		views = append(views, viewParty(p))
		for _, b := range p.Saldo {
			h, pi := b.HutangMinor, b.PiutangMinor
			if b.Currency != base {
				r, ok := rates[b.Currency+">"+base]
				if !ok || !r.Valid() {
					if !seen[b.Currency] {
						seen[b.Currency] = true
						missing = append(missing, b.Currency)
					}
					continue
				}
				h, pi = r.Convert(h), r.Convert(pi)
			}
			hutang += h
			piutang += pi
		}
	}

	net := piutang - hutang
	tone := "neutral"
	switch {
	case net > 0:
		tone = "in"
	case net < 0:
		tone = "out"
	}
	return DebtSummary{
		Base: base, TotalHutang: Format(hutang, base), TotalPiutang: Format(piutang, base),
		Net: Format(net, base), NetTone: tone,
		Parties: views, Unconverted: missing,
	}
}
