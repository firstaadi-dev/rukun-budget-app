package main

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// Semua nominal uang disimpan sebagai int64 dalam satuan terkecil (minor unit)
// mata uangnya. 150050 + "IDR" = Rp1.500,50. Tidak pernah float64: error
// pembulatan biner terakumulasi di SUM saldo dan tidak bisa dilacak balik.
//
// Kurs TIDAK disimpan sebagai angka ter-skala. Menyimpan 0,0000645 dengan 8
// desimal cuma menyisakan 3 digit signifikan, dan bolak-balik konversi
// menggeser nominal. Kurs di sini selalu berupa rasio dua nominal int64 yang
// memang benar-benar tercatat (Rate) — eksak, dan dua sisinya sudah jadi
// sumber kebenaran saldo.

// expTable: jumlah digit desimal menurut ISO 4217.
// ponytail: hanya mata uang yang menyimpang dari 2 yang perlu didaftar.
var expTable = map[string]int32{
	"JPY": 0, "KRW": 0, "VND": 0, "CLP": 0, "ISK": 0,
	"BHD": 3, "KWD": 3, "JOD": 3, "OMR": 3, "TND": 3,
}

func Exp(cur string) int32 {
	if e, ok := expTable[cur]; ok {
		return e
	}
	return 2
}

func pow10(n int32) int64 {
	p := int64(1)
	for ; n > 0; n-- {
		p *= 10
	}
	return p
}

var currencySymbol = map[string]string{
	"IDR": "Rp", "USD": "$", "SGD": "S$", "EUR": "€",
	"JPY": "¥", "AUD": "A$", "MYR": "RM", "GBP": "£",
}

func Symbol(cur string) string {
	if s, ok := currencySymbol[cur]; ok {
		return s
	}
	return cur
}

// Currencies: pilihan mata uang di form Dompet.
var Currencies = []struct{ Code, Name string }{
	{"IDR", "Rupiah Indonesia"},
	{"USD", "Dolar AS"},
	{"SGD", "Dolar Singapura"},
	{"EUR", "Euro"},
	{"JPY", "Yen Jepang"},
	{"AUD", "Dolar Australia"},
	{"MYR", "Ringgit Malaysia"},
	{"GBP", "Pound Sterling"},
}

// ParseAmount mengubah teks user jadi minor unit.
// Menerima "1.234.567", "1234567", "1234,56", "1234.56".
// Pemisah desimal = titik/koma terakhir yang diikuti 1-2 digit; sisanya
// dianggap pemisah ribuan. Konsekuensinya "1.234" dibaca 1234, bukan 1,234 —
// sesuai konvensi penulisan angka Indonesia.
func ParseAmount(s, cur string) (int64, error) {
	s = strings.ReplaceAll(strings.TrimSpace(s), " ", "")
	s = strings.TrimSpace(strings.TrimPrefix(s, Symbol(cur)))
	if s == "" {
		return 0, fmt.Errorf("nominal kosong")
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimLeft(s, "+-")

	whole, frac := s, ""
	if i := strings.LastIndexAny(s, ".,"); i >= 0 {
		if d := len(s) - i - 1; d >= 1 && d <= 2 {
			whole, frac = s[:i], s[i+1:]
		}
	}
	whole = strings.NewReplacer(".", "", ",", "").Replace(whole)
	if whole == "" {
		whole = "0"
	}

	e := int(Exp(cur))
	for len(frac) < e {
		frac += "0"
	}
	frac = frac[:e]

	v, err := strconv.ParseInt(whole+frac, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("nominal tidak valid: %q", s)
	}
	if neg {
		v = -v
	}
	return v, nil
}

// group: 1234567 -> "1.234.567"
func group(n int64) string {
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "." + s[i:]
	}
	return s
}

// FormatPlain: angka saja tanpa simbol mata uang, untuk isi field input.
func FormatPlain(minor int64, cur string) string {
	e := Exp(cur)
	div := pow10(e)
	sign := ""
	if minor < 0 {
		sign, minor = "-", -minor
	}
	out := sign + group(minor/div)
	// IDR bulat ditampilkan tanpa ",00" — murni urusan tampilan, bukan storage.
	if e > 0 && !(cur == "IDR" && minor%div == 0) {
		out += fmt.Sprintf(",%0*d", e, minor%div)
	}
	return out
}

// Format: nominal lengkap dengan simbol, mis. "Rp1.600.000" / "-$1.000,00".
func Format(minor int64, cur string) string {
	if minor < 0 {
		return "-" + Symbol(cur) + FormatPlain(-minor, cur)
	}
	return Symbol(cur) + FormatPlain(minor, cur)
}

// Rate adalah kurs sebagai rasio dua nominal nyata: FromMinor unit mata uang
// From setara InMinor unit mata uang To. Karena keduanya int64 dalam minor
// unit masing-masing, perbedaan jumlah desimal antar mata uang sudah ikut
// terkandung di rasio — tidak ada faktor skala terpisah yang bisa salah arah.
type Rate struct {
	FromMinor int64
	From      string
	ToMinor   int64
	To        string
}

func (r Rate) Valid() bool {
	return r.FromMinor > 0 && r.ToMinor > 0 && r.From != "" && r.To != ""
}

func (r Rate) Invert() Rate {
	return Rate{FromMinor: r.ToMinor, From: r.To, ToMinor: r.FromMinor, To: r.From}
}

// Convert menilai amount (minor unit r.From) ke minor unit r.To.
// Pembulatan setengah ke atas, pakai math/big supaya perkalian tengah aman.
func (r Rate) Convert(amount int64) int64 {
	if !r.Valid() {
		return 0
	}
	if r.From == r.To {
		return amount
	}
	n := new(big.Int).Mul(big.NewInt(amount), big.NewInt(r.ToMinor))
	d := big.NewInt(r.FromMinor)
	n.Lsh(n, 1)
	n.Add(n, d) // (2n + d) / 2d = pembulatan setengah ke atas
	d.Lsh(d, 1)
	return n.Quo(n, d).Int64()
}

// Unit menampilkan kurs dalam bentuk "harga 1 unit", memilih arah yang enak
// dibaca: 1 USD = Rp15.500, bukan 1 IDR = $0,0000645.
// Mengembalikan (nominal minor, mata uang nominal, mata uang yang dihargai).
func (r Rate) Unit() (int64, string, string) {
	if !r.Valid() {
		return 0, r.From, r.To
	}
	fromMajor := new(big.Rat).SetFrac(big.NewInt(r.FromMinor), big.NewInt(pow10(Exp(r.From))))
	toMajor := new(big.Rat).SetFrac(big.NewInt(r.ToMinor), big.NewInt(pow10(Exp(r.To))))
	if fromMajor.Cmp(toMajor) >= 0 {
		// 1 unit To berharga sekian From.
		return r.Invert().Convert(pow10(Exp(r.To))), r.From, r.To
	}
	return r.Convert(pow10(Exp(r.From))), r.To, r.From
}

func (r Rate) String() string {
	if !r.Valid() || r.From == r.To {
		return "—"
	}
	price, priceCur, perCur := r.Unit()
	return fmt.Sprintf("1 %s = %s", perCur, Format(price, priceCur))
}

// SameAs membandingkan dua kurs sebagai rasio, tanpa lewat pembulatan.
func (r Rate) SameAs(o Rate) bool {
	if !r.Valid() || !o.Valid() || r.From != o.From || r.To != o.To {
		return false
	}
	l := new(big.Int).Mul(big.NewInt(r.FromMinor), big.NewInt(o.ToMinor))
	rr := new(big.Int).Mul(big.NewInt(o.FromMinor), big.NewInt(r.ToMinor))
	return l.Cmp(rr) == 0
}

// ParseUnitRate membaca kurs yang diketik user dalam bentuk "harga 1 unit
// priceCur untuk 1 perCur" (mis. 15.550 pada label "1 USD = Rp ___") dan
// mengubahnya jadi Rate berarah from -> to.
func ParseUnitRate(s, priceCur, perCur, from, to string) (Rate, error) {
	price, err := ParseAmount(s, priceCur)
	if err != nil {
		return Rate{}, err
	}
	if price <= 0 {
		return Rate{}, fmt.Errorf("kurs harus lebih dari nol")
	}
	r := Rate{FromMinor: pow10(Exp(perCur)), From: perCur, ToMinor: price, To: priceCur}
	switch {
	case r.From == from && r.To == to:
		return r, nil
	case r.To == from && r.From == to:
		return r.Invert(), nil
	default:
		return Rate{}, fmt.Errorf("kurs %s/%s tidak cocok dengan transfer %s→%s", priceCur, perCur, from, to)
	}
}

// AdminFee: biaya admin transfer dalam mata uang sumber, yaitu selisih antara
// nominal yang keluar dan nilai nominal yang benar-benar diterima.
func AdminFee(outMinor, inMinor int64, curOut, curIn string, r Rate) int64 {
	if curOut == curIn {
		return outMinor - inMinor
	}
	if !r.Valid() {
		return 0
	}
	return outMinor - r.Invert().Convert(inMinor)
}

// EffectiveRate: kurs yang sebenarnya didapat setelah biaya admin dipotong.
// Inilah kurs yang ditampilkan di Detail Transaksi — diturunkan dari nominal
// tersimpan, bukan dari angka kurs yang pernah diketik user.
func EffectiveRate(outMinor, feeMinor, inMinor int64, curOut, curIn string) Rate {
	net := outMinor - feeMinor
	if net <= 0 || inMinor <= 0 || curOut == curIn {
		return Rate{}
	}
	return Rate{FromMinor: net, From: curOut, ToMinor: inMinor, To: curIn}
}
