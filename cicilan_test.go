package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Yang paling mudah salah di cicilan adalah dua hal yang sama-sama tidak
// menimbulkan error: jumlah angsuran yang tidak pas dengan harganya, dan bulan
// yang terlewat karena tanggalnya tidak ada di bulan itu.
func TestJadwalCicilan(t *testing.T) {
	jakarta := time.FixedZone("WIB", 7*3600)
	iso := func(t time.Time) string { return t.Format("2006-01-02") }

	// Rp10.000.000 dibagi 3 tidak habis. Sisanya harus tetap tercatat, bukan
	// hilang jadi selisih yang tidak pernah ditagih siapa pun.
	dasar := Tx{Kind: "expense", Category: "Belanja", Note: "Laptop",
		Date: time.Date(2026, 8, 20, 0, 0, 0, 0, jakarta), AmountMinor: 1_000_000_000}

	angsuran := jadwalCicilan(dasar, 3, true)
	if len(angsuran) != 3 {
		t.Fatalf("jumlah angsuran = %d, mau 3", len(angsuran))
	}
	var total int64
	for _, a := range angsuran {
		total += a.AmountMinor
	}
	if total != dasar.AmountMinor {
		t.Errorf("jumlah angsuran = %d, mau pas %d", total, dasar.AmountMinor)
	}
	if angsuran[0].AmountMinor != 333_333_334 || angsuran[2].AmountMinor != 333_333_333 {
		t.Errorf("sisa pembagian tidak jatuh di angsuran pertama: %d lalu %d",
			angsuran[0].AmountMinor, angsuran[2].AmountMinor)
	}
	if iso(angsuran[0].Date) != "2026-08-20" || iso(angsuran[2].Date) != "2026-10-20" {
		t.Errorf("tanggal = %s..%s, mau 2026-08-20..2026-10-20",
			iso(angsuran[0].Date), iso(angsuran[2].Date))
	}
	// Catatan tetap milik yang menulisnya; nomor urutnya di kolom sendiri.
	// Kalau ia ikut ditempel ke catatan, edit massal harus merakitnya ulang.
	// SeriesID sengaja masih kosong: CreateTxs yang tahu nomor rangkaiannya.
	if a := angsuran[1]; a.Note != "Laptop" || a.SeriesSeq != 2 || a.SeriesN != 3 ||
		a.SeriesKind != "cicil" || a.SeriesID != 0 {
		t.Errorf("angsuran kedua = catatan %q, %s %d/%d, seri %d — mau Laptop, cicil 2/3, seri 0",
			a.Note, a.SeriesKind, a.SeriesSeq, a.SeriesN, a.SeriesID)
	}
	// Label yang dibaca user dirakit dari ketiga kolom itu.
	berlabel := angsuran[1]
	berlabel.SeriesID = 77
	if got := berlabel.SeriesLabel(); got != "Cicilan 2/3" {
		t.Errorf("label = %q, mau Cicilan 2/3", got)
	}

	// Berulang: nominal penuh tiap bulan, dan totalnya memang berlipat.
	ulang := jadwalCicilan(dasar, 3, false)
	for i, a := range ulang {
		if a.AmountMinor != dasar.AmountMinor {
			t.Errorf("bulan ke-%d = %d, mau penuh %d", i+1, a.AmountMinor, dasar.AmountMinor)
		}
	}
	if ulang[0].SeriesKind != "ulang" || ulang[0].Note != "Laptop" {
		t.Errorf("berulang = jenis %q catatan %q, mau ulang dan Laptop",
			ulang[0].SeriesKind, ulang[0].Note)
	}

	// Yang menyentuh saldo hari ini cuma angsuran yang sudah jatuh. Ini yang
	// membuat sewa dua belas bulan tidak ditolak karena uangnya belum ada
	// seluruhnya hari ini — dan yang membuat saldo tetap jujur kalau ditolak.
	if got := totalSampai(angsuran, dasar.Date); got != 333_333_334 {
		t.Errorf("jatuh sampai hari transaksinya = %d, mau angsuran pertama saja", got)
	}
	if got := totalSampai(angsuran, dasar.Date.AddDate(0, 1, 0)); got != 666_666_667 {
		t.Errorf("jatuh sampai sebulan kemudian = %d, mau dua angsuran", got)
	}
	if got := totalSampai(angsuran, dasar.Date.AddDate(0, 0, -1)); got != 0 {
		t.Errorf("seluruhnya masih di depan = %d, mau 0", got)
	}

	// Tanggal 31 harus dijepit ke akhir bulan, bukan melompat ke bulan
	// berikutnya. Melompat berarti ada bulan yang tidak punya angsuran sama
	// sekali, dan satu bulan lain yang punya dua.
	akhir := dasar
	akhir.Date = time.Date(2026, 1, 31, 0, 0, 0, 0, jakarta)
	mau := []string{"2026-01-31", "2026-02-28", "2026-03-31", "2026-04-30"}
	for i, a := range jadwalCicilan(akhir, 4, true) {
		if iso(a.Date) != mau[i] {
			t.Errorf("angsuran ke-%d jatuh %s, mau %s", i+1, iso(a.Date), mau[i])
		}
	}

	// n=1 harus mengembalikan transaksinya apa adanya, tanpa jadi rangkaian
	// beranggota satu — "Cicilan 1/1" adalah kebisingan di transaksi biasa.
	satu := jadwalCicilan(dasar, 1, true)
	if len(satu) != 1 || satu[0].SeriesN != 0 || satu[0].AmountMinor != dasar.AmountMinor {
		t.Errorf("n=1 = %+v, mau transaksinya apa adanya", satu)
	}
}

// Mode datang dari URL dan dari isian form, keduanya bisa dikarang siapa saja.
// Yang dijaga di sini: mode ngawur jatuh ke jalur biasa alih-alih menghalangi,
// dan jumlah bulan yang tidak masuk akal ditolak — bukan diam-diam jadi satu
// transaksi yang nominalnya sudah terlanjur dibagi.
func TestReadCicilan(t *testing.T) {
	post := func(v url.Values) *http.Request {
		r := httptest.NewRequest("POST", "/transaksi/baru", strings.NewReader(v.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return r
	}
	belanja := Tx{Kind: "expense"}

	// Tanpa mode: jalur utama, dan kolom cicilan yang kebetulan ikut terkirim
	// tidak boleh mengubah apa pun.
	for _, v := range []url.Values{
		{},
		{"cicilan": {"12"}},
		{"mode": {"tiap-jumat"}, "cicilan": {"12"}},
	} {
		n, bagi, err := readCicilan(post(v), belanja)
		if n != 1 || bagi || err != nil {
			t.Errorf("%v = (%d, %v, %v), mau (1, false, nil)", v, n, bagi, err)
		}
	}

	n, bagi, err := readCicilan(post(url.Values{"mode": {"cicil"}, "cicilan": {"12"}}), belanja)
	if n != 12 || !bagi || err != nil {
		t.Errorf("cicil 12 = (%d, %v, %v), mau (12, true, nil)", n, bagi, err)
	}
	n, bagi, err = readCicilan(post(url.Values{"mode": {"ulang"}, "cicilan": {"6"}}), belanja)
	if n != 6 || bagi || err != nil {
		t.Errorf("ulang 6 = (%d, %v, %v), mau (6, false, nil)", n, bagi, err)
	}

	// Jumlah bulan di luar akal, dan satu bulan yang bukan cicilan sama sekali.
	for _, bulan := range []string{"", "0", "1", "-3", "999", "dua belas"} {
		if _, _, err := readCicilan(post(url.Values{"mode": {"cicil"}, "cicilan": {bulan}}), belanja); err == nil {
			t.Errorf("cicilan %q diterima, mau ditolak", bulan)
		}
	}

	// Transfer punya dua sisi nominal; memecah sisi keluarnya saja membuat kurs
	// tiap angsuran ngawur, jadi ditolak meski menunya memang tidak ada di sana.
	if _, _, err := readCicilan(post(url.Values{"mode": {"cicil"}, "cicilan": {"3"}}),
		Tx{Kind: "transfer"}); err == nil {
		t.Error("transfer diterima untuk dicicil, mau ditolak")
	}
}
