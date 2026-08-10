package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Investasi fase 1: emas batangan, saham dan ETF Amerika, dan reksadana
// Indonesia.
//
// Ketiganya berbentuk sama — sejumlah unit yang dimiliki, modal yang sudah
// dikeluarkan, dan harga pasar yang bergerak sendiri — jadi ketiganya memakai
// satu model. Yang berbeda cuma satuannya, cara harganya didapat, dan dari mana
// uangnya diambil.

const (
	// qtyScale: kuantitas disimpan sebagai bilangan bulat berskala 1e8.
	// Alasannya sama dengan nominal uang di money.go: 0,1 lembar tidak punya
	// wakil eksak di float64, dan galatnya menumpuk di penjumlahan lot.
	qtyScale = 100_000_000
	// priceScale: harga satuan disimpan empat desimal lebih halus dari satuan
	// terkecil mata uangnya. NAB reksadana lazim ditulis empat desimal, dan
	// membulatkannya ke rupiah penuh menghapus persis perbedaan antar hari yang
	// ingin dilihat.
	priceScale = 10_000
)

// troyOunceGramE7: satu troy ounce dalam gram, dikali 1e7. Angkanya eksak
// menurut definisi — 31,1034768 gram — bukan hasil pembulatan.
const troyOunceGramE7 = 311_034_768

// ---------- kuantitas ----------

// ParseQty membaca kuantitas yang diketik user jadi bilangan bulat berskala.
//
// Aturannya mengikuti ParseAmount supaya seluruh aplikasi membaca angka dengan
// cara yang sama: pemisah terakhir adalah desimal kecuali ia titik yang diikuti
// tepat tiga digit, yang berarti pemisah ribuan. "1.234" jadi 1234 lembar,
// "1,2345" jadi 1,2345 unit, "0.5" jadi setengah gram.
func ParseQty(s string) (int64, error) {
	return parseSkala(s, 8, "kuantitas")
}

// parseSkala membaca angka berdesimal jadi bilangan bulat berskala 10^desimal.
// Dipakai kuantitas dan harga satuan, yang skalanya berbeda tapi cara membaca
// pemisahnya harus sama persis dengan seluruh aplikasi.
func parseSkala(s string, desimal int, apa string) (int64, error) {
	s = strings.ReplaceAll(strings.TrimSpace(s), " ", "")
	if s == "" {
		return 0, fmt.Errorf("%s kosong", apa)
	}
	if strings.HasPrefix(s, "-") {
		return 0, fmt.Errorf("%s tidak boleh negatif", apa)
	}
	s = strings.TrimPrefix(s, "+")

	whole, frac := s, ""
	if i := strings.LastIndexAny(s, ".,"); i >= 0 {
		d := len(s) - i - 1
		ribuan := s[i] == '.' && d == 3
		if d >= 1 && !ribuan {
			whole, frac = s[:i], s[i+1:]
		}
	}
	whole = strings.ReplaceAll(strings.ReplaceAll(whole, ".", ""), ",", "")
	if whole == "" {
		whole = "0"
	}
	if !hanyaDigit(whole) || !hanyaDigit(frac) {
		return 0, fmt.Errorf("%s tidak terbaca", apa)
	}
	// Pecahan yang lebih panjang dari skalanya ditolak, bukan dibulatkan diam-
	// diam: menerima angka yang berbeda dari yang diketik lebih buruk daripada
	// menolaknya di depan mata orangnya.
	if len(frac) > desimal {
		return 0, fmt.Errorf("%s paling banyak %d angka di belakang koma", apa, desimal)
	}
	frac += strings.Repeat("0", desimal-len(frac))

	v, ok := new(big.Int).SetString(whole+frac, 10)
	if !ok || !v.IsInt64() {
		return 0, fmt.Errorf("%s terlalu besar", apa)
	}
	if v.Sign() == 0 {
		return 0, fmt.Errorf("%s harus lebih dari nol", apa)
	}
	return v.Int64(), nil
}

// ParsePriceE4 membaca harga satuan. Desimalnya empat lebih banyak dari satuan
// terkecil mata uangnya — enam untuk rupiah — supaya NAB reksadana bisa
// diketik apa adanya.
func ParsePriceE4(s, cur string) (int64, error) {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), Symbol(cur)))
	return parseSkala(s, int(Exp(cur))+4, "harga")
}

func hanyaDigit(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// FormatQty menulis kuantitas dengan pemisah ribuan dan tanpa nol di ekor.
// 20 gram tampil "20", bukan "20,00000000".
func FormatQty(q int64) string {
	sign := ""
	if q < 0 {
		sign, q = "-", -q
	}
	frac := strings.TrimRight(fmt.Sprintf("%08d", q%qtyScale), "0")
	out := sign + group(q/qtyScale)
	if frac != "" {
		out += "," + frac
	}
	return out
}

// ---------- harga dan nilai ----------

// hargaHalusMax: di atas nilai ini, desimal tambahan tidak lagi ditulis.
// Empat desimal ekstra berguna untuk NAB reksadana yang bernilai ribuan rupiah
// dan bergerak di angka keempatnya; pada harga emas sejuta rupiah per gram,
// desimal yang sama cuma derau yang tidak dibaca siapa pun.
const hargaHalusMax = 10_000

// FormatPriceE4 menulis harga satuan. Desimal yang lebih halus dari satuan
// terkecil mata uangnya hanya ditulis kalau memang ada isinya dan harganya
// masih cukup kecil untuk memerlukannya.
func FormatPriceE4(e4 int64, cur string) string {
	if e4 <= 0 {
		return "—"
	}
	minor, sisa := e4/priceScale, e4%priceScale
	if sisa == 0 || minor/pow10(Exp(cur)) >= hargaHalusMax {
		// Dibulatkan, bukan dipotong: harga yang ditampilkan sebaiknya angka
		// terdekat dari yang tersimpan.
		return Format((e4+priceScale/2)/priceScale, cur)
	}
	out := Format(minor, cur)
	ekor := strings.TrimRight(fmt.Sprintf("%04d", sisa), "0")
	// IDR bulat ditulis tanpa desimal oleh Format, jadi komanya belum ada.
	if !strings.Contains(out, ",") {
		return out + "," + ekor
	}
	return out + ekor
}

// NilaiMinor menghitung harga satuan dikali kuantitas, kembali ke satuan
// terkecil mata uang. Perkalian tengahnya lewat big.Int: harga saham mahal
// dikali kuantitas berskala 1e8 sudah melewati int64 jauh sebelum pembaginya
// bekerja.
func NilaiMinor(priceE4, qtyE8 int64) int64 {
	if priceE4 <= 0 || qtyE8 <= 0 {
		return 0
	}
	n := new(big.Int).Mul(big.NewInt(priceE4), big.NewInt(qtyE8))
	d := new(big.Int).SetInt64(int64(priceScale) * qtyScale)
	n.Lsh(n, 1)
	n.Add(n, d) // (2n + d) / 2d = pembulatan setengah ke atas
	d.Lsh(d, 1)
	return n.Quo(n, d).Int64()
}

// ModalPerUnitE4 membalik arahnya: dari total yang dikeluarkan dan kuantitas
// yang didapat, berapa modal tiap satuannya.
func ModalPerUnitE4(modalMinor, qtyE8 int64) int64 {
	if modalMinor <= 0 || qtyE8 <= 0 {
		return 0
	}
	n := new(big.Int).Mul(big.NewInt(modalMinor), big.NewInt(int64(priceScale)*qtyScale))
	d := big.NewInt(qtyE8)
	n.Lsh(n, 1)
	n.Add(n, d)
	d.Lsh(d, 1)
	return n.Quo(n, d).Int64()
}

// KuantitasDari membalik NilaiMinor: dari nilai yang benar-benar dibayarkan
// untuk barangnya — total dikurangi biaya — dan harga satuannya, berapa banyak
// yang didapat.
//
// Rumusnya kebetulan sama persis dengan ModalPerUnitE4: di kedua arah, skala
// harga dan skala kuantitas saling menghabiskan. Dipanggil ulang di sini alih-
// alih disalin, dengan nama yang menyebut apa yang sedang dihitung.
func KuantitasDari(nilaiMinor, priceE4 int64) int64 {
	return ModalPerUnitE4(nilaiMinor, priceE4)
}

// PerGramE4 mengubah harga per troy ounce jadi harga per gram. Emas dunia
// dikuotasi per troy ounce, sedangkan emas batangan di sini selalu disebut per
// gram, dan pembagian itu tidak boleh tersebar ke banyak tempat.
func PerGramE4(perOunceE4 int64) int64 {
	if perOunceE4 <= 0 {
		return 0
	}
	n := new(big.Int).Mul(big.NewInt(perOunceE4), big.NewInt(10_000_000))
	d := big.NewInt(troyOunceGramE7)
	n.Lsh(n, 1)
	n.Add(n, d)
	d.Lsh(d, 1)
	return n.Quo(n, d).Int64()
}

// ---------- posisi ----------

// Investment: satu hal yang dimiliki, dengan ringkasan seluruh pembeliannya.
// Kuantitas dan modal di sini hasil penjumlahan lot, bukan kolom tersimpan —
// sama seperti saldo dompet, tidak ada angka yang bisa melenceng dari catatan.
type Investment struct {
	ID       int64
	Kind     string // gold | stock | fund
	Name     string
	Varian   string
	Symbol   string
	Currency string

	WalletID   int64
	WalletName string

	ManualPriceE4 int64
	ManualPriceOn time.Time
	Note          string
	CreatedAt     time.Time

	// Ringkasan lot.
	QtyE8      int64
	ModalMinor int64 // seluruh yang dikeluarkan, biaya beli sudah termasuk
	FeeMinor   int64
	Lots       int
	LastBuy    time.Time
}

var investKindLabel = map[string]string{
	"gold": "Emas", "stock": "Saham & ETF", "fund": "Reksadana",
}

var investUnit = map[string]string{
	"gold": "gram", "stock": "lembar", "fund": "unit",
}

func (v Investment) KindLabel() string { return investKindLabel[v.Kind] }
func (v Investment) Unit() string      { return investUnit[v.Kind] }

// Label: nama posisi seperti yang dibaca user. Varian menempel di belakang
// namanya, karena dua pecahan emas dengan merek sama adalah dua posisi berbeda
// dan daftar yang menyebut keduanya "Antam" saja tidak bisa dibedakan.
func (v Investment) Label() string {
	if v.Varian == "" {
		return v.Name
	}
	return v.Name + " · " + v.Varian
}

// IsGold: emas dibeli dari dompet mana pun dan tidak menyimpan saldo di broker.
func (v Investment) IsGold() bool { return v.Kind == "gold" }

// PakaiPasar: harganya diikuti dari sumber harga, bukan diisi sendiri.
func (v Investment) PakaiPasar() bool { return v.Symbol != "" }

// ---------- tampilan ----------

type InvestView struct {
	Investment
	UnitLabel string
	Qty       string
	Modal     string
	ModalUnit string
	Fee       string

	// Harga: harga satuan yang dipakai, sudah jadi teks. HargaAsal menjelaskan
	// dari mana ia datang — tanpa itu, angka pasar dan angka isian sendiri
	// tidak bisa dibedakan, padahal keduanya beda derajat kepercayaannya.
	Harga     string
	HargaAsal string
	// Nilai dan Selisih kosong saat harganya belum diketahui sama sekali.
	Nilai    string
	Selisih  string
	Persen   string
	Tone     string // in | out | neutral
	AdaHarga bool

	nilaiMinor int64
}

// hargaPakai memilih harga satuan yang dipakai sebuah posisi, beserta
// keterangan asalnya.
//
// Harga pasar menang atas harga isian sendiri, dan mata uang kuotasinya
// dikonversi kalau perlu. Konversi itu bukan kemewahan: emas dunia dikuotasi
// dalam dolar per troy ounce sementara emas batangan di sini selalu dicatat
// dalam rupiah per gram, jadi tanpa konversi tidak ada satu pun posisi emas
// yang bisa mengikuti harga pasar.
//
// Kurs yang tidak diketahui berarti harga pasarnya dilewati, bukan dipakai apa
// adanya: angka dolar yang dipajang sebagai rupiah lebih buruk daripada tidak
// ada angka sama sekali.
func hargaPakai(v Investment, quotes map[string]Kuotasi, rates map[string]Rate, today time.Time) (e4 int64, asal string) {
	if v.PakaiPasar() {
		if k, ok := quotes[v.Symbol]; ok && k.Ada() {
			p, cur := k.PriceE4, k.Currency
			// Troy ounce jadi gram lebih dulu, selagi masih dalam mata uang
			// kuotasinya: urutannya tidak mengubah hasil, tapi menaruhnya di
			// sini membuat konversi mata uang di bawah cuma satu langkah.
			if v.Kind == "gold" {
				p = PerGramE4(p)
			}
			tukar := ""
			if cur != v.Currency {
				r, ok := rates[cur+">"+v.Currency]
				if ok && r.Valid() {
					// Kurs bekerja pada rasio nominal, jadi skala 1e4 yang
					// menempel di kedua sisi ikut terbawa utuh.
					p, cur = r.Convert(p), v.Currency
					tukar = " (dari " + k.Currency + ")"
				} else {
					p = 0
				}
			}
			if cur == v.Currency && p > 0 {
				// NAB reksadana tidak menerbitkan tanggalnya sendiri, jadi yang
				// disebut waktu pengambilan — dan kalimatnya harus mengatakan
				// itu, bukan berpura-pura tahu NAB per tanggal berapa.
				asal := "harga pasar "
				if k.Diambil {
					asal = "NAB diambil "
				}
				return p, asal + tanggalPendek(k.At.In(today.Location())) + tukar
			}
		}
	}
	if v.ManualPriceE4 > 0 {
		return v.ManualPriceE4, "diisi sendiri " + tanggalPendek(v.ManualPriceOn)
	}
	return 0, ""
}

func viewInvest(v Investment, quotes map[string]Kuotasi, rates map[string]Rate, today time.Time) InvestView {
	out := InvestView{
		Investment: v,
		UnitLabel:  v.Unit(),
		Qty:        FormatQty(v.QtyE8),
		Modal:      Format(v.ModalMinor, v.Currency),
		ModalUnit:  FormatPriceE4(ModalPerUnitE4(v.ModalMinor, v.QtyE8), v.Currency),
		Tone:       "neutral",
	}
	if v.FeeMinor > 0 {
		out.Fee = Format(v.FeeMinor, v.Currency)
	}

	e4, asal := hargaPakai(v, quotes, rates, today)
	if e4 <= 0 {
		out.Harga = "—"
		return out
	}
	out.AdaHarga = true
	out.Harga = FormatPriceE4(e4, v.Currency)
	out.HargaAsal = asal

	nilai := NilaiMinor(e4, v.QtyE8)
	out.nilaiMinor = nilai
	out.Nilai = Format(nilai, v.Currency)

	selisih := nilai - v.ModalMinor
	switch {
	case selisih > 0:
		out.Selisih, out.Tone = "+"+Format(selisih, v.Currency), "in"
	case selisih < 0:
		out.Selisih, out.Tone = "-"+Format(-selisih, v.Currency), "out"
	default:
		out.Selisih = Format(0, v.Currency)
	}
	if v.ModalMinor > 0 {
		out.Persen = persenSelisih(selisih, v.ModalMinor)
	}
	return out
}

// persenSelisih menulis selisih sebagai persentase modal, satu desimal.
// Dihitung dari bilangan bulat supaya tidak ada pembulatan biner yang
// menggeser angka yang sudah eksak.
func persenSelisih(selisih, modal int64) string {
	if modal <= 0 {
		return ""
	}
	permil := selisih * 1000 / modal // satu desimal persen
	sign := "+"
	if permil < 0 {
		sign, permil = "-", -permil
	}
	return fmt.Sprintf("%s%d,%d%%", sign, permil/10, permil%10)
}

func viewInvests(vs []Investment, quotes map[string]Kuotasi, rates map[string]Rate, today time.Time) []InvestView {
	out := make([]InvestView, len(vs))
	for i, v := range vs {
		out[i] = viewInvest(v, quotes, rates, today)
	}
	return out
}

// ---------- ringkasan ----------

type InvestSummary struct {
	TotalModal string
	TotalNilai string
	Selisih    string
	Persen     string
	Tone       string
	Base       string
	// Unconverted: mata uang yang kursnya tidak diketahui, jadi posisinya tidak
	// ikut terjumlah. Disebut, bukan didiamkan — total yang diam-diam kurang
	// lengkap lebih menyesatkan daripada total yang mengaku kurang lengkap.
	Unconverted []string
	// TanpaHarga: posisi yang harganya belum diketahui sama sekali. Modalnya
	// tetap terhitung, nilainya tidak.
	TanpaHarga int
}

func summarizeInvests(vs []InvestView, rates map[string]Rate, base string) InvestSummary {
	var modal, nilai int64
	seen := map[string]bool{}
	var missing []string
	tanpa := 0

	for _, v := range vs {
		m, n := v.ModalMinor, v.nilaiMinor
		if !v.AdaHarga {
			tanpa++
			n = m // tanpa harga, nilainya dianggap sebesar modalnya
		}
		if v.Currency != base {
			r, ok := rates[v.Currency+">"+base]
			if !ok || !r.Valid() {
				if !seen[v.Currency] {
					seen[v.Currency] = true
					missing = append(missing, v.Currency)
				}
				continue
			}
			m, n = r.Convert(m), r.Convert(n)
		}
		modal += m
		nilai += n
	}

	out := InvestSummary{
		TotalModal: Format(modal, base), TotalNilai: Format(nilai, base),
		Base: base, Unconverted: missing, TanpaHarga: tanpa, Tone: "neutral",
	}
	switch selisih := nilai - modal; {
	case selisih > 0:
		out.Selisih, out.Tone = "+"+Format(selisih, base), "in"
	case selisih < 0:
		out.Selisih, out.Tone = "-"+Format(-selisih, base), "out"
	default:
		out.Selisih = Format(0, base)
	}
	if modal > 0 {
		out.Persen = persenSelisih(nilai-modal, modal)
	}
	return out
}

// ---------- lot ----------

// LotView: satu pembelian di riwayat posisi.
type LotView struct {
	Tx
	DateLabel string
	Qty       string
	Total     string
	PerUnit   string
	Fee       string
	Wallet    string
}

func viewLot(t Tx, v Investment) LotView {
	out := LotView{
		Tx:        t,
		DateLabel: tanggalPendek(t.Date),
		Qty:       FormatQty(t.QtyE8) + " " + v.Unit(),
		Total:     Format(t.AmountMinor, t.WalletCur),
		PerUnit:   FormatPriceE4(ModalPerUnitE4(t.AmountMinor, t.QtyE8), t.WalletCur),
		Wallet:    t.WalletName,
	}
	if t.AdminFee > 0 {
		out.Fee = Format(t.AdminFee, t.WalletCur)
	}
	// Pembelian tanpa dompet tidak menggerakkan saldo mana pun. Itu harus
	// terlihat, kalau tidak angkanya terbaca seperti uang yang berpindah.
	if !t.HasWallet() {
		out.Wallet = "tanpa dompet"
	}
	return out
}

func viewLots(ts []Tx, v Investment) []LotView {
	out := make([]LotView, len(ts))
	for i, t := range ts {
		out[i] = viewLot(t, v)
	}
	return out
}

// ---------- handler ----------

var investKinds = []struct{ Value, Label string }{
	{"gold", "Emas"}, {"stock", "Saham & ETF"}, {"fund", "Reksadana"},
}

func knownInvestKind(k string) bool { _, ok := investKindLabel[k]; return ok }

// simbolDari mengumpulkan simbol yang perlu dimintakan harganya. Diambil dari
// posisi yang sedang ditampilkan saja: daftar simbol seluruh deployment akan
// memaksa query lintas keluarga, dan tidak ada satu pun query di aplikasi ini
// yang boleh melewati penyaring keluarganya.
func simbolDari(vs []Investment) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range vs {
		if v.Symbol != "" && !seen[v.Symbol] {
			seen[v.Symbol] = true
			out = append(out, v.Symbol)
		}
	}
	return out
}

type InvestGroup struct {
	Kind  string
	Label string
	Items []InvestView
}

func groupInvests(vs []InvestView) []InvestGroup {
	var out []InvestGroup
	for _, v := range vs {
		if n := len(out); n > 0 && out[n-1].Kind == v.Kind {
			out[n-1].Items = append(out[n-1].Items, v)
			continue
		}
		out = append(out, InvestGroup{Kind: v.Kind, Label: v.KindLabel(), Items: []InvestView{v}})
	}
	return out
}

// kuotasi merakit harga untuk sekumpulan posisi dari dua sumber yang berbeda:
// saham, ETF, dan emas dari bursa; reksadana dari daftar NAB. Keduanya keluar
// sebagai satu peta yang dikunci simbol, karena yang membacanya tidak perlu
// tahu dari mana angkanya datang — bentroknya pun tidak mungkin, kode bursa
// dan nama reksadana tidak pernah sama.
func (a *App) kuotasi(ctx context.Context, vs []Investment) map[string]Kuotasi {
	var bursa []string
	out := map[string]Kuotasi{}
	for _, v := range vs {
		if v.Symbol == "" {
			continue
		}
		if v.Kind == "fund" {
			if k, ok := a.nabSrc.nab(v.Symbol); ok {
				out[v.Symbol] = k
			}
			continue
		}
		bursa = append(bursa, v.Symbol)
	}
	for sym, k := range a.hargaSrc.harga(ctx, bursa) {
		out[sym] = k
	}
	return out
}

func (a *App) investList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	vs, err := a.store.Investments(ctx, family(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	rates, err := a.rates(ctx, family(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	views := viewInvests(vs, a.kuotasi(ctx, vs), rates, a.today())

	a.render(w, r, "investasi.html", map[string]any{
		"Title": "Investasi", "Nav": "investasi",
		"Groups": groupInvests(views), "Summary": summarizeInvests(views, rates, a.base),
		"Kinds": investKinds, "HargaError": a.hargaSrc.status() != nil,
	})
}

func (a *App) investDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	v, err := a.store.Investment(ctx, family(r), pathID(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	lots, err := a.store.Transactions(ctx, family(r), TxFilter{Investment: v.ID})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	rates, err := a.rates(ctx, family(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	view := viewInvest(v, a.kuotasi(ctx, []Investment{v}), rates, a.today())

	a.render(w, r, "investasi_detail.html", map[string]any{
		"Title": v.Label(), "Nav": "investasi", "Back": "/investasi",
		"Inv": view, "Lots": viewLots(lots, v),
		// Harga manual selalu bisa diisi, juga untuk posisi yang punya simbol:
		// ia yang dipakai saat sumber harganya sedang tidak bisa dihubungi.
		"HargaForm": map[string]string{
			"harga":   hargaPlain(v),
			"tanggal": a.today().Format(formatTanggal),
		},
	})
}

// hargaPlain: harga isian sendiri sebagai teks yang bisa diketik ulang, tanpa
// simbol mata uang.
func hargaPlain(v Investment) string {
	if v.ManualPriceE4 <= 0 {
		return ""
	}
	s := FormatPriceE4(v.ManualPriceE4, v.Currency)
	return strings.TrimPrefix(s, Symbol(v.Currency))
}

func (a *App) investForm(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	f := map[string]string{
		"jenis":     r.URL.Query().Get("jenis"),
		"mata_uang": a.base,
	}
	if !knownInvestKind(f["jenis"]) {
		f["jenis"] = "gold"
	}
	if id != 0 {
		v, err := a.store.Investment(r.Context(), family(r), id)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		f = map[string]string{
			"jenis": v.Kind, "nama": v.Name, "varian": v.Varian, "simbol": v.Symbol,
			"mata_uang": v.Currency, "dompet": strconv.FormatInt(v.WalletID, 10),
			"catatan": v.Note,
		}
	}
	a.renderInvestForm(w, r, id, f, "")
}

func (a *App) renderInvestForm(w http.ResponseWriter, r *http.Request, id int64, f map[string]string, errMsg string) {
	wallets, err := a.store.Wallets(r.Context(), family(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	action, title := "/investasi/baru", "Posisi Baru"
	if id != 0 {
		action = "/investasi/" + strconv.FormatInt(id, 10) + "/ubah"
		title = "Ubah Posisi"
	}
	if errMsg != "" {
		w.WriteHeader(http.StatusUnprocessableEntity)
	}
	a.render(w, r, "investasi_form.html", map[string]any{
		"Title": title, "Nav": "investasi", "Back": "/investasi",
		"Action": action, "ID": id, "Form": f, "Error": errMsg,
		"Kinds": investKinds, "Currencies": Currencies,
		"Brokers": pilihDompet(wallets, "broker"),
		"Unit":    investUnit[f["jenis"]],
	})
}

// pilihDompet menyaring dompet yang boleh dipilih. Jenis kosong berarti semua.
func pilihDompet(ws []Wallet, jenis string) []Wallet {
	if jenis == "" {
		return ws
	}
	out := make([]Wallet, 0, len(ws))
	for _, w := range ws {
		if w.Type == jenis {
			out = append(out, w)
		}
	}
	return out
}

// readInvest memvalidasi form posisi.
//
// Namanya tidak lagi diketik: untuk saham dan ETF ia diambil dari kode
// bursanya, untuk reksadana ia memang namanya sendiri. Sebelumnya nama diisi
// oleh skrip di browser begitu hasil pencarian sampai, dan itu balapan yang
// sering kalah — memilih dari daftar lebih cepat daripada jaringan, dan yang
// tersimpan posisi tanpa nama.
func (a *App) readInvest(r *http.Request) (Investment, map[string]string, error) {
	f := map[string]string{
		"jenis":     r.FormValue("jenis"),
		"nama":      strings.TrimSpace(r.FormValue("nama")),
		"varian":    strings.TrimSpace(r.FormValue("varian")),
		"simbol":    strings.ToUpper(strings.TrimSpace(r.FormValue("simbol"))),
		"mata_uang": r.FormValue("mata_uang"),
		"dompet":    r.FormValue("dompet"),
		"catatan":   strings.TrimSpace(r.FormValue("catatan")),
	}
	v := Investment{
		Kind: f["jenis"], Name: f["nama"], Varian: f["varian"],
		Symbol: f["simbol"], Currency: f["mata_uang"], Note: f["catatan"],
	}
	v.WalletID, _ = strconv.ParseInt(f["dompet"], 10, 64)

	if !knownInvestKind(v.Kind) {
		return v, f, errors.New("Jenis investasi tidak dikenal.")
	}
	if !knownCurrency(v.Currency) {
		return v, f, errors.New("Mata uang tidak dikenal.")
	}

	switch v.Kind {
	case "stock":
		if v.Symbol == "" {
			return v, f, errors.New("Isi kode bursanya — VOO, QQQ, AAPL. Ketik kode atau namanya, lalu pilih dari daftar yang muncul.")
		}
		nama, dikenal, err := a.hargaSrc.namaSimbol(r.Context(), v.Symbol)
		switch {
		case err != nil:
			// Sumber harganya sedang tidak bisa dihubungi. Itu bukan salah
			// orangnya, jadi kodenya dipakai apa adanya sebagai nama sementara
			// — pembelian tetap bisa dicatat sekarang.
			log.Printf("nama simbol %q: %v", v.Symbol, err)
			v.Name = v.Symbol
		case !dikenal:
			return v, f, errors.New("Kode " + v.Symbol + " tidak dikenal di bursa. Periksa ejaannya, atau pilih dari daftar yang muncul saat mengetik.")
		default:
			v.Name = nama
		}
	case "fund":
		// Simbol reksadana bukan kode bursa melainkan namanya persis seperti di
		// daftar NAB, jadi ia tidak boleh dihuruf-besarkan seperti ticker.
		v.Symbol = strings.Join(strings.Fields(r.FormValue("simbol")), " ")
		f["simbol"] = v.Symbol
		if v.Symbol == "" {
			return v, f, errors.New("Pilih reksadananya dari daftar NAB yang muncul saat mengetik.")
		}
		v.Name = v.Symbol
	default:
		if len(v.Name) < 2 {
			return v, f, errors.New("Merek emasnya minimal 2 karakter.")
		}
	}
	// Emas dibeli dari dompet mana pun dan tidak menyimpan saldo di broker,
	// jadi ia tidak menempel ke satu dompet.
	if v.Kind == "gold" {
		v.WalletID = 0
	}
	return v, f, nil
}

func (a *App) investCreate(w http.ResponseWriter, r *http.Request) {
	v, f, err := a.readInvest(r)
	if err != nil {
		a.renderInvestForm(w, r, 0, f, err.Error())
		return
	}
	id, err := a.store.CreateInvestment(r.Context(), family(r), v)
	if err != nil {
		a.renderInvestForm(w, r, 0, f, pesanSimpanInvest(err))
		return
	}
	http.Redirect(w, r, "/investasi/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (a *App) investUpdate(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	v, f, err := a.readInvest(r)
	if err != nil {
		a.renderInvestForm(w, r, id, f, err.Error())
		return
	}
	lama, err := a.store.Investment(r.Context(), family(r), id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	// Jenis dan harga isian sendiri tidak ikut form ini. Jenis menentukan
	// satuan seluruh lot yang sudah tercatat, dan menggantinya akan mengubah
	// arti angka yang sudah ada tanpa menyentuh angkanya.
	v.ID, v.Kind = id, lama.Kind
	v.ManualPriceE4, v.ManualPriceOn = lama.ManualPriceE4, lama.ManualPriceOn
	if lama.Lots > 0 && v.Currency != lama.Currency {
		f["mata_uang"] = lama.Currency
		a.renderInvestForm(w, r, id, f, "Mata uang tidak bisa diganti selama masih ada pembelian tercatat: nominal lot lama sudah tersimpan dalam mata uang lamanya.")
		return
	}
	if err := a.store.UpdateInvestment(r.Context(), family(r), v); err != nil {
		a.renderInvestForm(w, r, id, f, pesanSimpanInvest(err))
		return
	}
	http.Redirect(w, r, "/investasi/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

// pesanSimpanInvest menerjemahkan pelanggaran keunikan jadi kalimat yang
// menjelaskan apa yang harus diubah user.
func pesanSimpanInvest(err error) string {
	if strings.Contains(err.Error(), "investments_family_id_kind_name_varian_key") {
		return "Posisi dengan jenis, nama, dan varian itu sudah ada. Pakai yang sudah ada, atau bedakan variannya."
	}
	return "Gagal menyimpan: " + err.Error()
}

func (a *App) investDelete(w http.ResponseWriter, r *http.Request) {
	err := a.store.DeleteInvestment(r.Context(), family(r), pathID(r))
	if errors.Is(err, ErrNotFound) {
		// Satu-satunya sebab lain baris ini tidak terhapus adalah lot yang
		// masih menempel, dan itu bukan kesalahan yang pantas jadi layar error.
		http.Redirect(w, r, "/investasi/"+strconv.FormatInt(pathID(r), 10)+"?galat=terpakai", http.StatusSeeOther)
		return
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/investasi", http.StatusSeeOther)
}

// ---------- harga isian sendiri ----------

func (a *App) investPrice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := pathID(r)
	v, err := a.store.Investment(ctx, family(r), id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	harga := strings.TrimSpace(r.FormValue("harga"))
	tanggal, errTgl := time.ParseInLocation(formatTanggal, r.FormValue("tanggal"), a.loc)

	switch {
	case harga == "":
		// Dikosongkan berarti dilepas, bukan nol rupiah.
		v.ManualPriceE4, v.ManualPriceOn = 0, time.Time{}
	default:
		e4, err := ParsePriceE4(harga, v.Currency)
		if err != nil || errTgl != nil {
			http.Redirect(w, r, "/investasi/"+strconv.FormatInt(id, 10)+"?galat=harga", http.StatusSeeOther)
			return
		}
		v.ManualPriceE4, v.ManualPriceOn = e4, tanggal
	}
	if err := a.store.UpdateInvestment(ctx, family(r), v); err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/investasi/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

// ---------- pembelian ----------

func (a *App) investBuyForm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	v, err := a.store.Investment(ctx, family(r), pathID(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	f := map[string]string{
		"tanggal": a.today().Format(formatTanggal),
		"dompet":  strconv.FormatInt(v.WalletID, 10),
	}
	// Harga satuan diisi lebih dulu dari harga yang berlaku sekarang. Yang
	// mencatat pembelian hari ini hampir selalu membeli di harga itu atau
	// sekitarnya, dan mengetik ulang angka yang sudah diketahui aplikasi cuma
	// menambah peluang salah ketik.
	rates, err := a.rates(ctx, family(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if e4, asal := hargaPakai(v, a.kuotasi(ctx, []Investment{v}), rates, a.today()); e4 > 0 {
		f["harga"] = strings.TrimPrefix(FormatPriceE4(e4, v.Currency), Symbol(v.Currency))
		f["harga_asal"] = asal
	}
	a.renderBuyForm(w, r, v, f, "")
}

func (a *App) renderBuyForm(w http.ResponseWriter, r *http.Request, v Investment, f map[string]string, errMsg string) {
	wallets, err := a.store.Wallets(r.Context(), family(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	// Hanya dompet yang mata uangnya sama yang boleh dipilih. Membeli posisi
	// dolar dari dompet rupiah butuh kurs, dan kurs pada pembelian aset bukan
	// hal yang bisa ditebak belakangan dari dua angka yang tersimpan.
	pilihan := make([]Wallet, 0, len(wallets))
	for _, wl := range wallets {
		if wl.Currency == v.Currency && !wl.IsCredit() {
			pilihan = append(pilihan, wl)
		}
	}
	if errMsg != "" {
		w.WriteHeader(http.StatusUnprocessableEntity)
	}
	id := strconv.FormatInt(v.ID, 10)
	a.render(w, r, "investasi_beli.html", map[string]any{
		"Title": "Catat Pembelian", "Nav": "investasi", "Back": "/investasi/" + id,
		"Action": "/investasi/" + id + "/beli",
		"Inv":    v, "Unit": v.Unit(), "Form": f, "Error": errMsg,
		"Wallets": pilihan,
	})
}

// readLot memvalidasi form pembelian.
func readLot(r *http.Request, v Investment, loc *time.Location) (Tx, map[string]string, error) {
	f := map[string]string{
		"tanggal":   r.FormValue("tanggal"),
		"kuantitas": strings.TrimSpace(r.FormValue("kuantitas")),
		"total":     strings.TrimSpace(r.FormValue("total")),
		"harga":     strings.TrimSpace(r.FormValue("harga")),
		"biaya":     strings.TrimSpace(r.FormValue("biaya")),
		"dompet":    r.FormValue("dompet"),
		"catatan":   strings.TrimSpace(r.FormValue("catatan")),
	}
	t := Tx{Kind: "invest_buy", InvestmentID: v.ID, Note: f["catatan"], WalletCur: v.Currency}

	tanggal, err := time.ParseInLocation(formatTanggal, f["tanggal"], loc)
	if err != nil {
		return t, f, errors.New("Tanggal tidak valid.")
	}
	t.Date = tanggal

	total, err := ParseAmount(f["total"], v.Currency)
	if err != nil || total <= 0 {
		return t, f, errors.New("Total yang dibayar harus angka lebih dari nol.")
	}
	t.AmountMinor = total

	// Total, kuantitas, harga satuan, dan biaya terikat satu persamaan:
	// total = kuantitas × harga + biaya. Total selalu diisi, jadi yang tersisa
	// dua arah, dan keduanya bekerja di sini juga tanpa JS:
	//
	//   kuantitas ada  -> biaya yang dihitung, dari harga satuannya
	//   kuantitas kosong -> kuantitas yang dihitung, dari harga dan biayanya
	//
	// Biaya kosong berbeda dari biaya nol. Nol adalah jawaban — pembelian tanpa
	// komisi sama sekali — dan dengan kuantitas kosong ia justru yang membuat
	// kuantitasnya bisa dihitung. Kosong berarti belum dijawab.
	biayaDiisi := f["biaya"] != ""
	var biaya int64
	if biayaDiisi {
		if biaya, err = ParseAmount(f["biaya"], v.Currency); err != nil || biaya < 0 {
			return t, f, errors.New("Biaya harus angka nol atau lebih.")
		}
	}

	var hargaE4 int64
	if f["harga"] != "" {
		if hargaE4, err = ParsePriceE4(f["harga"], v.Currency); err != nil {
			return t, f, errors.New("Harga per " + v.Unit() + " tidak valid.")
		}
	}

	if f["kuantitas"] != "" {
		qty, err := ParseQty(f["kuantitas"])
		if err != nil {
			return t, f, errors.New("Kuantitas tidak valid: " + err.Error() + ".")
		}
		t.QtyE8 = qty
		switch {
		case biayaDiisi:
			t.AdminFee = biaya
		case hargaE4 > 0:
			t.AdminFee = total - NilaiMinor(hargaE4, qty)
			if t.AdminFee < 0 {
				return t, f, errors.New("Harga per " + v.Unit() + " dikali kuantitasnya melebihi total yang dibayar. Periksa ketiganya.")
			}
		}
	} else {
		if hargaE4 <= 0 || !biayaDiisi {
			return t, f, errors.New("Isi kuantitasnya, atau isi harga per " + v.Unit() +
				" dan biayanya supaya kuantitas bisa dihitung sendiri. Biaya nol tetap harus ditulis 0.")
		}
		if biaya >= total {
			return t, f, errors.New("Biaya harus lebih kecil dari total yang dibayar — biaya sudah termasuk di dalamnya.")
		}
		t.AdminFee = biaya
		t.QtyE8 = KuantitasDari(total-biaya, hargaE4)
		if t.QtyE8 <= 0 {
			return t, f, errors.New("Total dikurangi biaya terlalu kecil untuk harga per " +
				v.Unit() + " itu — kuantitasnya jadi nol.")
		}
	}
	// Biaya sudah termasuk di total, sama seperti biaya admin pada transfer.
	// Biaya sebesar totalnya berarti tidak ada yang dibeli sama sekali.
	if t.AdminFee >= total {
		return t, f, errors.New("Biaya harus lebih kecil dari total yang dibayar — biaya sudah termasuk di dalamnya.")
	}

	t.WalletID, _ = strconv.ParseInt(f["dompet"], 10, 64)
	return t, f, nil
}

func (a *App) investBuyCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	v, err := a.store.Investment(ctx, family(r), pathID(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	t, f, err := readLot(r, v, a.loc)
	if err != nil {
		a.renderBuyForm(w, r, v, f, err.Error())
		return
	}
	if t.WalletID != 0 {
		wl, err := a.store.Wallet(ctx, family(r), t.WalletID)
		if err != nil {
			a.renderBuyForm(w, r, v, f, "Dompet tidak ditemukan.")
			return
		}
		if wl.Currency != v.Currency {
			a.renderBuyForm(w, r, v, f, "Mata uang dompet harus sama dengan mata uang posisinya. Topup dompet brokernya dulu lewat transfer, di situlah kurs dan biayanya dicatat.")
			return
		}
	}
	if _, err := a.store.CreateTx(ctx, family(r), t, userFrom(ctx).ID); err != nil {
		a.renderBuyForm(w, r, v, f, "Gagal menyimpan: "+err.Error())
		return
	}
	http.Redirect(w, r, "/investasi/"+strconv.FormatInt(v.ID, 10), http.StatusSeeOther)
}

// ---------- pencarian simbol ----------

// investCari melayani isian otomatis di form posisi. Balasannya JSON, dan ini
// satu-satunya endpoint JSON di aplikasi ini — halaman lain semuanya HTML.
// Pengecualiannya dibayar setimpal: tanpa ini, mencatat sebuah ETF berarti
// mengetik ulang kode dan namanya dari ingatan, dan kode yang salah satu huruf
// menghasilkan posisi yang harganya tidak pernah datang tanpa penjelasan.
func (a *App) investCari(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	jenis := r.URL.Query().Get("jenis")

	var out []Cocok
	switch {
	case len(q) < 2:
		// Satu huruf mencocokkan ratusan nama; menunggu huruf kedua membuat
		// daftarnya berguna sekaligus menghemat permintaan ke sumbernya.
	case jenis == "fund":
		for _, rd := range a.nabSrc.cari(q, 8) {
			out = append(out, Cocok{Simbol: rd.Nama, Nama: rd.Nama,
				Info: rd.Kategori + " · NAB " + FormatPriceE4(rd.PriceE4, rd.Currency)})
		}
	default:
		cocok, err := a.hargaSrc.cariSimbol(r.Context(), q, 8)
		if err != nil {
			// Pencarian yang gagal bukan alasan menampilkan layar error: kolom
			// simbolnya tetap bisa diketik sendiri.
			log.Printf("cari simbol %q: %v", q, err)
		}
		out = cocok
	}

	if out == nil {
		out = []Cocok{}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(out); err != nil {
		log.Printf("cari simbol: %v", err)
	}
}
