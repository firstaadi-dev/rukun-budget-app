package main

import (
	"fmt"
	"sort"
	"strings"
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

// ---------- periode daftar transaksi ----------

// periodeSemua: nilai yang melepas batas waktu sama sekali.
const periodeSemua = "semua"

// Periode: rentang yang sedang dilihat di halaman Transaksi.
//
// Daftar transaksi dibatasi bulan berjalan secara bawaan. Sebelumnya daftarnya
// tak berperiode tapi dipotong di 200 baris terakhir, dan begitu sebuah keluarga
// melewati angka itu, transaksi lama menghilang dari layar tanpa tanda apa pun
// dan tanpa cara apa pun untuk sampai ke sana. Bulan adalah satuan yang dipakai
// keluarga saat mengingat pengeluaran, jadi itu yang jadi bawaannya — dan
// pindah bulan atau melepas batasnya cukup satu tautan.
type Periode struct {
	Nilai string // seperti yang dipasang di URL: "2026-08" atau "semua"
	Label string
	// Frasa: Label yang sudah siap dipakai di tengah kalimat, lengkap dengan
	// kata depannya. "di Agustus 2026" tapi "sejak 1 Juli 2026" — rentang yang
	// terbuka di satu ujung tidak bisa memakai kata depan yang sama.
	Frasa string
	From  time.Time // inklusif
	To    time.Time // eksklusif
	Semua bool
	// Bulan: satu bulan penuh, satu-satunya bentuk yang punya tetangga di kiri
	// dan kanannya. Ketiganya — Bulan, Semua, Rentang — saling meniadakan.
	Bulan bool
	// BulanIni: yang sedang dilihat memang bulan berjalan. Dipakai untuk
	// memutuskan perlu tidaknya tautan kembali.
	BulanIni bool
	// Rentang: yang sedang dilihat rentang tanggal pilihan sendiri, bukan satu
	// bulan penuh. Dari dan Sampai keduanya inklusif dan ditulis seperti yang
	// dipasang di URL, karena keduanya juga yang mengisi ulang kolom tanggalnya.
	// Salah satunya boleh kosong: rentang yang terbuka di satu ujung tetap
	// rentang.
	Rentang      bool
	Dari, Sampai string
	// Prev dan Next: nilai periode bulan tetangga. Melangkah maju tidak dibatasi
	// bulan berjalan — transaksi boleh bertanggal di depan, dan menutup jalan ke
	// sana akan menyembunyikannya persis seperti pemotongan 200 baris dulu.
	// Keduanya kosong saat yang dilihat rentang khusus: langkah sebesar apa yang
	// dimaksud "bulan berikutnya" dari 3–17 Juli tidak punya jawaban yang jelas.
	Prev string
	Next string
}

const (
	formatPeriode = "2006-01"
	formatTanggal = "2006-01-02"
)

// bacaPeriode menerjemahkan parameter URL jadi rentang tanggal. Kosong atau
// tidak terbaca berarti bulan berjalan: penyaring yang salah ketik sebaiknya
// jatuh ke tampilan bawaan, bukan ke daftar kosong yang terlihat seperti data
// hilang.
func bacaPeriode(nilai, dari, sampai string, today time.Time) Periode {
	// Rentang khusus menang atas periode bulanan. Keduanya menjawab pertanyaan
	// yang sama, dan yang tanggalnya ditulis sendiri adalah yang lebih spesifik.
	if p, ok := bacaRentang(dari, sampai, today.Location()); ok {
		return p
	}
	if nilai == periodeSemua {
		return Periode{Nilai: periodeSemua, Label: "Seluruh waktu", Frasa: "di seluruh catatan", Semua: true}
	}
	awal := awalBulan(today)
	if t, err := time.ParseInLocation(formatPeriode, nilai, today.Location()); err == nil {
		awal = t
	}
	return Periode{
		Nilai:    awal.Format(formatPeriode),
		Label:    namaBulan(awal),
		Frasa:    "di " + namaBulan(awal),
		From:     awal,
		To:       awal.AddDate(0, 1, 0),
		Bulan:    true,
		BulanIni: awal.Equal(awalBulan(today)),
		Prev:     awal.AddDate(0, -1, 0).Format(formatPeriode),
		Next:     awal.AddDate(0, 1, 0).Format(formatPeriode),
	}
}

// bacaRentang menerjemahkan sepasang tanggal jadi periode. Salah satunya boleh
// kosong; kalau keduanya kosong atau tidak terbaca, tidak ada rentang sama
// sekali dan periode bulanan yang berlaku.
//
// Sampai ditulis inklusif karena begitulah orang membacanya — "sampai 15
// Agustus" berarti tanggal 15 ikut — sedangkan To yang dikirim ke query
// eksklusif. Selisih satu hari itu diselesaikan di sini, satu kali, bukan di
// setiap pemanggil.
func bacaRentang(dari, sampai string, loc *time.Location) (Periode, bool) {
	d, adaD := bacaTanggal(dari, loc)
	s, adaS := bacaTanggal(sampai, loc)
	if !adaD && !adaS {
		return Periode{}, false
	}
	// Tanggal yang tertukar dibetulkan, bukan ditolak: urutan terbalik
	// menghasilkan daftar kosong yang terbaca sebagai catatan hilang, padahal
	// rentang yang dimaksud sudah jelas.
	if adaD && adaS && s.Before(d) {
		d, s = s, d
	}
	p := Periode{Rentang: true}
	switch {
	case adaD && adaS:
		p.From, p.To = d, s.AddDate(0, 0, 1)
		p.Dari, p.Sampai = d.Format(formatTanggal), s.Format(formatTanggal)
		// Sehari disebut sekali saja. "7 Agustus 2026 – 7 Agustus 2026" benar,
		// tapi membuat orang membaca dua kali untuk menyadari tidak ada yang
		// berbeda di antara keduanya.
		if d.Equal(s) {
			p.Label = tanggalPendek(d)
		} else {
			p.Label = tanggalPendek(d) + " – " + tanggalPendek(s)
		}
		p.Frasa = "di " + p.Label
	case adaD:
		p.From = d
		p.Dari = d.Format(formatTanggal)
		p.Label = "Sejak " + tanggalPendek(d)
		p.Frasa = "sejak " + tanggalPendek(d)
	default:
		p.To = s.AddDate(0, 0, 1)
		p.Sampai = s.Format(formatTanggal)
		p.Label = "Sampai " + tanggalPendek(s)
		p.Frasa = "sampai " + tanggalPendek(s)
	}
	return p, true
}

func bacaTanggal(s string, loc *time.Location) (time.Time, bool) {
	t, err := time.ParseInLocation(formatTanggal, strings.TrimSpace(s), loc)
	return t, err == nil
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
	case "invest_buy":
		// Nadanya netral, bukan "keluar". Uangnya memang meninggalkan dompet,
		// tapi ia tidak habis — ia berubah bentuk jadi sesuatu yang masih
		// dimiliki, persis seperti transfer antar dompet.
		v.ShortDesc = "Beli " + t.InvestmentName
		v.Desc = v.ShortDesc + " — " + FormatQty(t.QtyE8)
		if t.Note != "" {
			v.Desc += " — " + t.Note
		}
		v.WalletLabel = t.WalletName
		if !t.HasWallet() {
			v.WalletLabel = "tanpa dompet"
		}
		v.Amount = "-" + Format(t.AmountMinor, t.WalletCur)
		v.Tone = "neutral"
	case "invest_sell":
		v.ShortDesc = "Jual " + t.InvestmentName
		v.Desc = v.ShortDesc + " — " + FormatQty(t.QtyE8)
		if t.Note != "" {
			v.Desc += " — " + t.Note
		}
		v.WalletLabel = t.WalletName
		v.Amount = "+" + Format(t.AmountMinor, t.WalletCur)
		v.Tone = "neutral"
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

// Bergabung: tanggal anggota ini mulai terdaftar, seperti yang dibaca user.
func (m Member) Bergabung() string { return tanggalPendek(m.CreatedAt) }

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
	Net         string
	NetTone     string
	Unconverted []string
}

func categoryAmount(s CategorySpend, rates map[string]Rate, base string) (int64, bool) {
	if s.Currency == base {
		return s.Minor, true
	}
	r, ok := rates[s.Currency+">"+base]
	if !ok || !r.Valid() {
		return 0, false
	}
	return r.Convert(s.Minor), true
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
		amount, ok := categoryAmount(s, rates, base)
		if !ok {
			if !seen[s.Currency] {
				seen[s.Currency] = true
				missing = append(missing, s.Currency)
			}
			continue
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
		Net:         Format(totalIn-totalOut, base),
		NetTone:     map[bool]string{true: "in", false: "out"}[totalIn >= totalOut],
		Unconverted: missing,
	}
}

type BudgetRow struct {
	Name, Limit, Used, Remaining, OverAmount string
	Percent                                  int
	Over                                     bool
}

func budgetRows(categories []Category, spend []CategorySpend, rates map[string]Rate, base string) []BudgetRow {
	used := map[string]int64{}
	for _, s := range spend {
		if s.Kind != "expense" {
			continue
		}
		if amount, ok := categoryAmount(s, rates, base); ok {
			used[s.Category] += amount
		}
	}
	var rows []BudgetRow
	for _, c := range categories {
		if c.Kind != "expense" || c.BudgetMinor <= 0 {
			continue
		}
		spent := used[c.Name]
		rows = append(rows, BudgetRow{
			Name: c.Name, Limit: Format(c.BudgetMinor, base), Used: Format(spent, base),
			Remaining:  Format(c.BudgetMinor-spent, base),
			OverAmount: Format(max(0, spent-c.BudgetMinor), base),
			Percent:    int(min(100, float64(spent)/float64(c.BudgetMinor)*100)), Over: spent > c.BudgetMinor,
		})
	}
	return rows
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
	// CicilanMendatang: bagian terpakai dari angsuran yang belum jatuh tempo.
	CicilanMendatang string

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

// mendesak: tingkat kegentingan sebuah kartu. Makin kecil makin mendesak.
func mendesak(c CardView) int {
	switch {
	case c.Telat():
		return 0
	case c.Segera():
		return 1
	case c.LimitTipis():
		return 2
	default:
		return 3
	}
}

// urutkanKartu menaruh yang paling mendesak di depan: yang sudah lewat jatuh
// tempo, lalu yang tenggatnya paling dekat, lalu yang limitnya menipis.
//
// Urutan ini bukan kosmetik. Deret kartu di dashboard menggulung ke samping,
// dan kartu keempat praktis tidak pernah terbaca di layar ponsel — jadi yang
// berada di sana harus yang paling tidak butuh tindakan, bukan yang kebetulan
// paling dulu dibuat.
func urutkanKartu(cards []CardView) {
	sort.SliceStable(cards, func(i, j int) bool {
		a, b := cards[i], cards[j]
		if ra, rb := mendesak(a), mendesak(b); ra != rb {
			return ra < rb
		}
		if a.HasCycle && b.HasCycle && a.HariLagi != b.HariLagi {
			return a.HariLagi < b.HariLagi
		}
		return a.TerpakaiPersen > b.TerpakaiPersen
	})
}

// Perhatian: satu baris peringatan di puncak dashboard.
//
// Tanda "telat" dan "3 hari lagi" sebenarnya sudah ada di dalam kartunya
// sendiri, tapi tanda di dalam kartu baru terbaca oleh orang yang sedang
// memandang kartunya. Yang telat bayar justru orang yang sedang tidak
// memikirkan kartu itu sama sekali. Karena itu peringatannya dipindah ke
// bagian layar yang dilihat lebih dulu — dan hanya muncul kalau memang ada
// yang perlu dikerjakan, supaya tidak jadi hiasan tetap yang berhenti dibaca.
type Perhatian struct {
	WalletID int64
	Nama     string
	Pesan    string
	Nominal  string
	Tone     string
	// PayablePlain: nominal tagihan tanpa simbol, untuk mengisi form
	// pembayaran. Kosong berarti tidak ada tagihan yang bisa dibayar dan
	// tombolnya tidak ditampilkan.
	PayablePlain string
}

// perhatian mengumpulkan akun kredit yang butuh tindakan hari ini. Satu baris
// per akun: kalau tagihannya telat sekaligus limitnya menipis, yang ditulis
// tagihannya — hanya itu yang punya tenggat.
func perhatian(cards []CardView) []Perhatian {
	var out []Perhatian
	for _, c := range cards {
		p := Perhatian{WalletID: c.ID, Nama: c.Name, Tone: "out", PayablePlain: c.PayablePlain}
		switch {
		case c.Telat():
			p.Pesan = fmt.Sprintf("Tagihan telat %d hari", -c.HariLagi)
			p.Nominal = c.Payable
		case c.Segera():
			p.Pesan = "Tagihan " + tempoLabel(c.HariLagi)
			p.Nominal = c.Payable
		case c.LimitTipis():
			p.Pesan = fmt.Sprintf("Limit terpakai %d%%", c.TerpakaiPersen)
			p.Nominal = "sisa " + c.SisaLimit
			p.PayablePlain = ""
		default:
			continue
		}
		out = append(out, p)
	}
	return out
}

// tempoLabel menamai tenggat dengan kata yang dipakai sehari-hari. "1 hari
// lagi" masih harus diterjemahkan sendiri oleh yang membaca; "besok" tidak.
func tempoLabel(hariLagi int) string {
	switch hariLagi {
	case 0:
		return "jatuh tempo hari ini"
	case 1:
		return "jatuh tempo besok"
	default:
		return fmt.Sprintf("jatuh tempo %d hari lagi", hariLagi)
	}
}

func viewCard(st CardStatus, today time.Time) CardView {
	w := st.Wallet
	v := CardView{
		Wallet:      w,
		HasCycle:    st.HasCycle,
		Outstanding: Format(w.CicilanMendatangMinor-st.OutstandingMinor, w.Currency),
	}
	if w.CicilanMendatangMinor > 0 {
		v.CicilanMendatang = Format(w.CicilanMendatangMinor, w.Currency)
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
		if sisa := w.TerpakaiMinor() - w.CicilanMendatangMinor - st.PayableMinor; sisa > 0 {
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
