package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Kuantitas dibaca dan ditulis dengan aturan pemisah yang sama seperti nominal
// uang. Salah di sini berarti jumlah yang dimiliki berbeda dari yang diketik —
// kesalahan yang tidak menimbulkan error dan baru ketahuan saat nilainya
// dibandingkan dengan catatan broker.
func TestParseQty(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		err  bool
	}{
		{"20", 20 * qtyScale, false},
		{"0.5", qtyScale / 2, false},
		{"0,5", qtyScale / 2, false},
		// Titik dengan tepat tiga digit di belakangnya adalah pemisah ribuan,
		// sama seperti ParseAmount. "1.234" lembar, bukan 1,234 lembar.
		{"1.234", 1234 * qtyScale, false},
		{"1.234,5678", 123456780000, false},
		{"1,2345", 123450000, false},
		// Delapan desimal masih muat, sembilan tidak.
		{"1,12345678", 112345678, false},
		{"1,123456789", 0, true},
		{"", 0, true},
		{"-5", 0, true},
		{"0", 0, true},
		{"abc", 0, true},
	}
	for _, c := range cases {
		got, err := ParseQty(c.in)
		if c.err {
			if err == nil {
				t.Errorf("ParseQty(%q) = %d, mau error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseQty(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseQty(%q) = %d, mau %d", c.in, got, c.want)
		}
		// Bolak-balik: yang ditulis ulang harus terbaca jadi angka yang sama.
		if lagi, err := ParseQty(FormatQty(got)); err != nil || lagi != got {
			t.Errorf("bolak-balik %q: %d -> %q -> %d (%v)", c.in, got, FormatQty(got), lagi, err)
		}
	}

	if got := FormatQty(20 * qtyScale); got != "20" {
		t.Errorf("FormatQty(20) = %q, mau 20 tanpa nol di ekor", got)
	}
	if got := FormatQty(123456780000); got != "1.234,5678" {
		t.Errorf("FormatQty = %q, mau 1.234,5678", got)
	}
}

// Harga satuan disimpan empat desimal lebih halus dari satuan terkecil mata
// uangnya. Yang perlu dijaga: NAB reksadana empat desimal tetap utuh, dan
// harga besar tidak memamerkan desimal yang tidak berarti.
func TestHargaSatuan(t *testing.T) {
	// NAB Rp1.234,5678 -> 1234,5678 rupiah = 123456,78 sen = 1.234.567.800 e4.
	nab, err := ParsePriceE4("1.234,5678", "IDR")
	if err != nil {
		t.Fatal(err)
	}
	if nab != 1_234_567_800 {
		t.Errorf("NAB = %d, mau 1234567800", nab)
	}
	if got := FormatPriceE4(nab, "IDR"); got != "Rp1.234,5678" {
		t.Errorf("tulis NAB = %q, mau Rp1.234,5678", got)
	}

	// Harga besar: desimal halusnya dibulatkan, bukan dipajang.
	if got := FormatPriceE4(2_526_358_670_000, "IDR"); got != "Rp2.526.358,67" {
		t.Errorf("harga emas = %q, mau Rp2.526.358,67", got)
	}
	// Enam desimal untuk rupiah adalah batasnya; yang ketujuh ditolak.
	if _, err := ParsePriceE4("1,1234567", "IDR"); err == nil {
		t.Error("tujuh desimal rupiah diterima, mau ditolak")
	}
	// Simbol mata uang di depan angka ikut dibuang.
	if got, err := ParsePriceE4("Rp1.000", "IDR"); err != nil || got != 1_000_000_000 {
		t.Errorf("dengan simbol = %d (%v), mau 1000000000 (Rp1.000)", got, err)
	}
}

// Nilai dan modal per satuan adalah dua arah dari perkalian yang sama, dan
// keduanya lewat big.Int karena hasil antaranya melewati int64.
func TestNilaiDanModal(t *testing.T) {
	// 3 lembar VOO seharga $710,71 = $2.132,13.
	harga := int64(710_71 * priceScale)
	if got := NilaiMinor(harga, 3*qtyScale); got != 213_213 {
		t.Errorf("nilai VOO = %d, mau 213213", got)
	}
	// Modal $2.130,00 atas 3 lembar = $710,00 per lembar.
	if got := ModalPerUnitE4(213_000, 3*qtyScale); got != 710_00*priceScale {
		t.Errorf("modal per lembar = %d, mau %d", got, 710_00*priceScale)
	}
	// Kuantitas besar dikali harga besar tidak boleh meluap diam-diam:
	// 1 juta lembar seharga Rp10.000.000 per lembar.
	if got := NilaiMinor(1_000_000_00*priceScale, 1_000_000*qtyScale); got != 100_000_000_000_000 {
		t.Errorf("nilai besar = %d, mau 100000000000000", got)
	}
	if got := NilaiMinor(0, qtyScale); got != 0 {
		t.Errorf("tanpa harga = %d, mau 0", got)
	}
}

// Emas dunia dikuotasi per troy ounce; yang dipakai di sini selalu per gram.
func TestPerGramE4(t *testing.T) {
	// $4.399,70 per troy ounce = $141,4536… per gram.
	perOunce := int64(4399_70 * priceScale)
	got := PerGramE4(perOunce)
	if want := int64(141_45 * priceScale); got < want-priceScale || got > want+priceScale {
		t.Errorf("per gram = %d (%s), mau sekitar %d", got, FormatPriceE4(got, "USD"), want)
	}
	if PerGramE4(0) != 0 {
		t.Error("harga nol harus tetap nol")
	}
}

// Pemilihan harga adalah tempat paling mudah salah di fitur ini: satu posisi
// bisa punya harga pasar, harga isian sendiri, keduanya, atau tak satu pun —
// dan emas selalu butuh dua konversi sekaligus.
func TestHargaPakai(t *testing.T) {
	jkt := time.FixedZone("WIB", 7*3600)
	today := time.Date(2026, 8, 9, 0, 0, 0, 0, jkt)
	kemarin := time.Date(2026, 8, 8, 20, 0, 0, 0, time.UTC)

	// 1 USD = Rp17.858, dalam bentuk rasio nominal seperti Rate lainnya.
	rates := map[string]Rate{
		"USD>IDR": {FromMinor: 100, From: "USD", ToMinor: 17_858_00, To: "IDR"},
	}
	emasDunia := map[string]Kuotasi{
		simbolEmas: {PriceE4: 4399_70 * priceScale, Currency: "USD", At: kemarin},
	}

	// Emas rupiah mengikuti emas dunia: per troy ounce jadi per gram, lalu
	// dolar jadi rupiah. Tanpa keduanya, tidak ada posisi emas yang bisa
	// mengikuti harga pasar sama sekali.
	emas := Investment{Kind: "gold", Symbol: simbolEmas, Currency: "IDR"}
	e4, asal := hargaPakai(emas, emasDunia, rates, today)
	if e4 <= 0 {
		t.Fatal("emas rupiah tidak dapat harga pasar")
	}
	if perGram := e4 / priceScale / 100; perGram < 2_400_000 || perGram > 2_700_000 {
		t.Errorf("emas = %s per gram, di luar rentang yang masuk akal", FormatPriceE4(e4, "IDR"))
	}
	if asal == "" || !strings.Contains(asal, "dari USD") {
		t.Errorf("asal harga = %q, mau menyebut konversinya", asal)
	}

	// Kurs yang tidak diketahui: harga pasarnya dilewati, bukan dipajang
	// sebagai rupiah. Yang tampil harga isian sendiri.
	emasManual := emas
	emasManual.ManualPriceE4 = 2_100_000_00 * priceScale
	emasManual.ManualPriceOn = today
	if e4, asal := hargaPakai(emasManual, emasDunia, nil, today); e4 != emasManual.ManualPriceE4 {
		t.Errorf("tanpa kurs = %d (%s), mau jatuh ke harga isian sendiri", e4, asal)
	}

	// Tanpa simbol dan tanpa isian sendiri: tidak ada harga, dan itu harus
	// dikatakan, bukan ditebak jadi nol.
	if e4, _ := hargaPakai(Investment{Kind: "fund", Currency: "IDR"}, nil, rates, today); e4 != 0 {
		t.Errorf("tanpa harga = %d, mau 0", e4)
	}

	// Harga pasar menang atas isian sendiri saat keduanya ada.
	saham := Investment{Kind: "stock", Symbol: "VOO", Currency: "USD",
		ManualPriceE4: 600_00 * priceScale, ManualPriceOn: today}
	quotes := map[string]Kuotasi{"VOO": {PriceE4: 710_71 * priceScale, Currency: "USD", At: kemarin}}
	if e4, _ := hargaPakai(saham, quotes, rates, today); e4 != 710_71*priceScale {
		t.Errorf("harga saham = %d, mau harga pasar 7107100", e4)
	}
}

// Ringkasan menjumlahkan lintas mata uang, dan yang tidak bisa dijumlahkan
// harus disebut — total yang diam-diam kurang lengkap lebih menyesatkan
// daripada total yang mengaku kurang lengkap.
func TestSummarizeInvests(t *testing.T) {
	jkt := time.FixedZone("WIB", 7*3600)
	today := time.Date(2026, 8, 9, 0, 0, 0, 0, jkt)
	rates := map[string]Rate{
		"USD>IDR": {FromMinor: 100, From: "USD", ToMinor: 17_858_00, To: "IDR"},
	}

	vs := []Investment{
		// Emas: modal Rp12 juta untuk 20 gram, harga sendiri Rp2 juta/gram.
		{Kind: "gold", Currency: "IDR", QtyE8: 20 * qtyScale, ModalMinor: 1_200_000_000,
			ManualPriceE4: 2_000_000_00 * priceScale, ManualPriceOn: today},
		// Saham: modal $2.130 untuk 3 lembar, harga pasar $710,71.
		{Kind: "stock", Symbol: "VOO", Currency: "USD", QtyE8: 3 * qtyScale, ModalMinor: 213_000},
		// Reksadana tanpa harga sama sekali.
		{Kind: "fund", Currency: "IDR", QtyE8: 1000 * qtyScale, ModalMinor: 100_000_000},
	}
	quotes := map[string]Kuotasi{"VOO": {PriceE4: 710_71 * priceScale, Currency: "USD", At: today}}
	views := viewInvests(vs, quotes, rates, today)
	sum := summarizeInvests(views, rates, "IDR")

	if sum.TanpaHarga != 1 {
		t.Errorf("posisi tanpa harga = %d, mau 1", sum.TanpaHarga)
	}
	if len(sum.Unconverted) != 0 {
		t.Errorf("ada mata uang tak terkonversi: %v", sum.Unconverted)
	}
	if sum.Tone != "in" {
		t.Errorf("nada = %q, mau in (emas naik dari Rp600rb ke Rp2 juta per gram)", sum.Tone)
	}

	// Tanpa kurs USD, posisi saham tidak ikut terjumlah dan itu harus disebut.
	sumTanpaKurs := summarizeInvests(viewInvests(vs, quotes, nil, today), nil, "IDR")
	if len(sumTanpaKurs.Unconverted) != 1 || sumTanpaKurs.Unconverted[0] != "USD" {
		t.Errorf("tak terkonversi = %v, mau [USD]", sumTanpaKurs.Unconverted)
	}
}

// Harga tiga jenis datang dari dua sumber yang berbeda, dan yang membacanya
// tidak boleh perlu tahu dari mana. Yang dijaga di sini: reksadana diambil dari
// daftar NAB lewat namanya, saham dan emas dari bursa, dan posisi tanpa simbol
// tidak menghasilkan permintaan ke mana pun.
func TestKuotasiDuaSumber(t *testing.T) {
	nabSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(contohNAB))
	}))
	defer nabSrv.Close()

	var diminta []string
	bursa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		diminta = append(diminta, strings.TrimPrefix(r.URL.Path, "/"))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"chart":{"result":[{"meta":{"currency":"USD",
			"regularMarketPrice":710.71,"regularMarketTime":1786132801}}]}}`))
	}))
	defer bursa.Close()

	app := &App{hargaSrc: newHargaSource(bursa.URL + "/"), nabSrc: newNABSource(nabSrv.URL + "/")}
	if err := app.nabSrc.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	vs := []Investment{
		{Kind: "fund", Symbol: "Sucorinvest Money Market Fund", Currency: "IDR"},
		{Kind: "stock", Symbol: "VOO", Currency: "USD"},
		{Kind: "gold", Currency: "IDR"}, // tanpa simbol: harga diisi sendiri
	}
	q := app.kuotasi(context.Background(), vs)

	if k := q["Sucorinvest Money Market Fund"]; k.PriceE4 != 1_980_690_000 || k.Currency != "IDR" {
		t.Errorf("NAB reksadana = %+v, mau 1980690000 IDR", k)
	}
	if k := q["VOO"]; k.PriceE4 != 710_71*priceScale || k.Currency != "USD" {
		t.Errorf("harga saham = %+v", k)
	}
	// Reksadana tidak boleh ikut ditanyakan ke bursa: namanya bukan kode, dan
	// permintaannya pasti sia-sia.
	if len(diminta) != 1 || !strings.HasPrefix(diminta[0], "VOO") {
		t.Errorf("permintaan ke bursa = %v, mau hanya VOO", diminta)
	}
}

// Pencarian melayani dua sumber lewat satu endpoint, dan bentuk balasannya
// harus sama supaya sisi layarnya tidak perlu bercabang.
func TestInvestCari(t *testing.T) {
	nabSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(contohNAB))
	}))
	defer nabSrv.Close()

	app := &App{hargaSrc: newHargaSource(""), nabSrc: newNABSource(nabSrv.URL + "/")}
	if err := app.nabSrc.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	ambil := func(target string) []Cocok {
		rec := httptest.NewRecorder()
		app.investCari(rec, httptest.NewRequest("GET", target, nil))
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("%s: Content-Type %q", target, ct)
		}
		var out []Cocok
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s: %v (%s)", target, err, rec.Body.String())
		}
		return out
	}

	got := ambil("/investasi/cari?jenis=fund&q=sucorinvest")
	if len(got) == 0 {
		t.Fatal("reksadana tidak ketemu")
	}
	// Untuk reksadana, simbolnya adalah namanya persis — itu yang dipakai
	// mencocokkan NAB nanti.
	if got[0].Simbol != got[0].Nama {
		t.Errorf("simbol %q ≠ nama %q", got[0].Simbol, got[0].Nama)
	}
	if !strings.Contains(got[0].Info, "NAB") {
		t.Errorf("info %q tidak menyebut NAB-nya", got[0].Info)
	}

	// Satu huruf tidak dicari: ratusan nama cocok, dan daftarnya tidak berguna.
	if len(ambil("/investasi/cari?jenis=fund&q=s")) != 0 {
		t.Error("ketikan satu huruf ikut dicari")
	}
	// Balasan kosong tetap larik JSON, bukan null — sisi layarnya melakukan
	// for-of atas hasilnya tanpa memeriksa lebih dulu.
	if body := ambil("/investasi/cari?jenis=fund&q=zzzz"); body == nil {
		t.Error("hasil kosong dikirim sebagai null")
	}
}

// Total, kuantitas, harga satuan, dan biaya terikat satu persamaan:
// total = kuantitas × harga + biaya. Yang dijaga di sini urutan menangnya, dan
// bahwa form ini tetap utuh tanpa JS — mengisi harga satuan saja sudah cukup.
func TestReadLotBiayaDariHarga(t *testing.T) {
	jkt := time.FixedZone("WIB", 7*3600)
	saham := Investment{ID: 1, Kind: "stock", Currency: "USD"}

	baca := func(isi url.Values) (Tx, error) {
		r := httptest.NewRequest("POST", "/investasi/1/beli", strings.NewReader(isi.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		tx, _, err := readLot(r, saham, jkt)
		return tx, err
	}
	dasar := func() url.Values {
		return url.Values{"tanggal": {"2026-08-09"}, "kuantitas": {"3"}, "dompet": {"0"}}
	}

	// Harga satuan mengisi biaya: 3 lembar × $710,71 = $2.132,13, dibayar
	// $2.134,13, jadi komisinya $2.
	f := dasar()
	f.Set("total", "2.134,13")
	f.Set("harga", "710,71")
	tx, err := baca(f)
	if err != nil {
		t.Fatalf("harga satuan: %v", err)
	}
	if tx.AdminFee != 200 {
		t.Errorf("biaya = %d, mau 200 ($2)", tx.AdminFee)
	}
	if tx.AmountMinor != 213_413 {
		t.Errorf("total = %d, mau 213413", tx.AmountMinor)
	}

	// Isian biaya menang atas harga satuan, sama seperti biaya admin di form
	// transfer: yang diketik orangnya persis itu yang tersimpan.
	f = dasar()
	f.Set("total", "2.134,13")
	f.Set("harga", "710,71")
	f.Set("biaya", "5")
	if tx, err := baca(f); err != nil || tx.AdminFee != 500 {
		t.Errorf("isian biaya = %d (%v), mau 500", tx.AdminFee, err)
	}

	// Tanpa keduanya, tidak ada biaya — bukan menebak-nebak.
	f = dasar()
	f.Set("total", "2.132,13")
	if tx, err := baca(f); err != nil || tx.AdminFee != 0 {
		t.Errorf("tanpa harga dan biaya = %d (%v), mau 0", tx.AdminFee, err)
	}

	// Harga dikali kuantitas melebihi total: ditolak dengan kalimat yang
	// menyebut ketiganya, bukan disimpan sebagai biaya negatif.
	f = dasar()
	f.Set("total", "2.000")
	f.Set("harga", "710,71")
	if _, err := baca(f); err == nil {
		t.Error("harga di atas total diterima, mau ditolak")
	} else if !strings.Contains(err.Error(), "lembar") {
		t.Errorf("pesan %q tidak menyebut satuannya", err)
	}

	// Biaya sebesar totalnya berarti tidak ada yang dibeli.
	f = dasar()
	f.Set("total", "100")
	f.Set("biaya", "100")
	if _, err := baca(f); err == nil {
		t.Error("biaya sebesar total diterima, mau ditolak")
	}

	// Harga satuan boleh lebih halus dari satuan terkecil mata uangnya: NAB
	// reksadana empat desimal harus terbaca apa adanya.
	dana := Investment{ID: 2, Kind: "fund", Currency: "IDR"}
	isi := url.Values{"tanggal": {"2026-08-09"}, "kuantitas": {"1.234,5678"},
		"total": {"2.500.000"}, "harga": {"1.980,69"}, "dompet": {"0"}}
	r := httptest.NewRequest("POST", "/investasi/2/beli", strings.NewReader(isi.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tx, _, err = readLot(r, dana, jkt)
	if err != nil {
		t.Fatalf("reksadana: %v", err)
	}
	// 1.234,5678 unit × Rp1.980,69 = Rp2.445.106,58; sisanya jadi biaya.
	if mau := int64(250_000_000) - NilaiMinor(1_980_69*priceScale, 1234_5678_0000); tx.AdminFee != mau {
		t.Errorf("biaya reksadana = %d, mau %d", tx.AdminFee, mau)
	}
	if tx.QtyE8 != 1234_5678_0000 {
		t.Errorf("kuantitas = %d, mau 123456780000", tx.QtyE8)
	}
}

// Arah sebaliknya: kuantitas dikosongkan, dan ia yang dihitung dari total,
// biaya, dan harga satuannya. Begitulah emas dan reksadana dibeli — dengan
// nominal bulat, dan kuantitas yang baru ketahuan belakangan.
func TestReadLotKuantitasDariBiaya(t *testing.T) {
	jkt := time.FixedZone("WIB", 7*3600)
	dana := Investment{ID: 2, Kind: "fund", Currency: "IDR"}

	baca := func(isi url.Values) (Tx, error) {
		r := httptest.NewRequest("POST", "/investasi/2/beli", strings.NewReader(isi.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		tx, _, err := readLot(r, dana, jkt)
		return tx, err
	}
	dasar := func() url.Values {
		return url.Values{"tanggal": {"2026-08-09"}, "dompet": {"0"}}
	}

	// Rp1.000.000 dibayar, Rp2.500 komisinya, NAB Rp1.980,69 —
	// Rp997.500 / 1.980,69 = 503,6122... unit.
	f := dasar()
	f.Set("total", "1.000.000")
	f.Set("biaya", "2.500")
	f.Set("harga", "1.980,69")
	tx, err := baca(f)
	if err != nil {
		t.Fatalf("kuantitas dari biaya: %v", err)
	}
	if mau := KuantitasDari(99_750_000, 1_980_69*priceScale); tx.QtyE8 != mau {
		t.Errorf("kuantitas = %d, mau %d", tx.QtyE8, mau)
	}
	if tx.AdminFee != 250_000 || tx.AmountMinor != 100_000_000 {
		t.Errorf("biaya %d total %d, mau 250000 dan 100000000", tx.AdminFee, tx.AmountMinor)
	}

	// Biaya nol adalah jawaban, bukan kolom kosong: seluruh total jadi barang.
	f = dasar()
	f.Set("total", "1.000.000")
	f.Set("biaya", "0")
	f.Set("harga", "1.980,69")
	nol, err := baca(f)
	if err != nil {
		t.Fatalf("biaya nol: %v", err)
	}
	if nol.AdminFee != 0 {
		t.Errorf("biaya = %d, mau 0", nol.AdminFee)
	}
	if mau := KuantitasDari(100_000_000, 1_980_69*priceScale); nol.QtyE8 != mau {
		t.Errorf("kuantitas dengan biaya nol = %d, mau %d", nol.QtyE8, mau)
	}
	if nol.QtyE8 <= tx.QtyE8 {
		t.Error("biaya nol harusnya dapat lebih banyak daripada biaya Rp2.500")
	}

	// Biaya kosong bukan biaya nol: tanpa kuantitas, tidak ada yang bisa
	// dihitung, dan menebaknya nol berarti menyimpan kuantitas karangan.
	f = dasar()
	f.Set("total", "1.000.000")
	f.Set("harga", "1.980,69")
	if _, err := baca(f); err == nil {
		t.Error("kuantitas dan biaya sama-sama kosong diterima, mau ditolak")
	}

	// Tanpa harga satuan pun tidak ada yang bisa dihitung.
	f = dasar()
	f.Set("total", "1.000.000")
	f.Set("biaya", "0")
	if _, err := baca(f); err == nil {
		t.Error("tanpa harga satuan diterima, mau ditolak")
	}

	// Biaya menghabiskan totalnya: tidak ada yang dibeli.
	f = dasar()
	f.Set("total", "1.000.000")
	f.Set("biaya", "1.000.000")
	f.Set("harga", "1.980,69")
	if _, err := baca(f); err == nil {
		t.Error("biaya sebesar total diterima, mau ditolak")
	}
}

// Nama posisi diambil dari kode bursanya di server, bukan diisi skrip di
// browser. Tiga jalannya harus dibedakan: kode yang dikenal memberi nama resmi,
// kode yang salah ketik ditolak sebelum tersimpan, dan sumber yang sedang mati
// tidak boleh menghalangi pencatatan.
func TestReadInvestNamaDariSimbol(t *testing.T) {
	bursa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/NGAWUR") {
			w.Write([]byte(`{"chart":{"result":[],"error":{"code":"Not Found",
				"description":"No data found, symbol may be delisted"}}}`))
			return
		}
		w.Write([]byte(`{"chart":{"result":[{"meta":{"currency":"USD","symbol":"VOOG",
			"shortName":"Vanguard S&P 500 Growth ETF",
			"regularMarketPrice":85.42,"regularMarketTime":1786132801}}]}}`))
	}))
	defer bursa.Close()

	kirim := func(app *App, isi url.Values) (Investment, error) {
		r := httptest.NewRequest("POST", "/investasi/baru", strings.NewReader(isi.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		v, _, err := app.readInvest(r)
		return v, err
	}
	saham := func(simbol string) url.Values {
		return url.Values{"jenis": {"stock"}, "simbol": {simbol}, "mata_uang": {"USD"}, "dompet": {"0"}}
	}

	app := &App{hargaSrc: newHargaSource(bursa.URL + "/"), nabSrc: newNABSource("")}

	// Kode dikenal: namanya datang dari sumbernya, bukan dari form.
	v, err := kirim(app, saham("voog"))
	if err != nil {
		t.Fatalf("kode dikenal: %v", err)
	}
	if v.Name != "Vanguard S&P 500 Growth ETF" {
		t.Errorf("nama = %q, mau nama resmi dari bursa", v.Name)
	}
	if v.Symbol != "VOOG" {
		t.Errorf("simbol = %q, mau VOOG (huruf besar)", v.Symbol)
	}

	// Kode salah ketik ditolak: posisinya tidak akan pernah dapat harga, dan
	// mengetahuinya sekarang lebih baik daripada saat harganya tak kunjung ada.
	if _, err := kirim(app, saham("NGAWUR")); err == nil {
		t.Error("kode ngawur diterima, mau ditolak")
	} else if !strings.Contains(err.Error(), "NGAWUR") {
		t.Errorf("pesan %q tidak menyebut kodenya", err)
	}

	// Kode kosong ditolak dengan kalimat yang menunjukkan jalan keluarnya.
	if _, err := kirim(app, saham("")); err == nil {
		t.Error("simbol kosong diterima, mau ditolak")
	}

	// Sumber mati: kodenya dipakai sebagai nama sementara, pencatatan jalan
	// terus. Menghalangi di sini berarti kegagalan jaringan orang lain
	// menghentikan pencatatan keuangan sendiri.
	mati := &App{hargaSrc: newHargaSource("http://127.0.0.1:1/"), nabSrc: newNABSource("")}
	v, err = kirim(mati, saham("VOO"))
	if err != nil {
		t.Fatalf("sumber mati malah menolak: %v", err)
	}
	if v.Name != "VOO" {
		t.Errorf("nama saat sumber mati = %q, mau kodenya sendiri", v.Name)
	}

	// Reksadana: namanya memang namanya sendiri, tanpa menyentuh jaringan.
	v, err = kirim(app, url.Values{"jenis": {"fund"}, "mata_uang": {"IDR"}, "dompet": {"0"},
		"simbol": {"  Sucorinvest   Money Market Fund "}})
	if err != nil {
		t.Fatalf("reksadana: %v", err)
	}
	if v.Name != "Sucorinvest Money Market Fund" || v.Symbol != v.Name {
		t.Errorf("reksadana: nama %q, simbol %q", v.Name, v.Symbol)
	}

	// Emas tidak punya kode: mereknya tetap diketik.
	v, err = kirim(app, url.Values{"jenis": {"gold"}, "nama": {"Antam"}, "varian": {"10 gram"},
		"mata_uang": {"IDR"}, "dompet": {"0"}})
	if err != nil || v.Name != "Antam" || v.Varian != "10 gram" {
		t.Errorf("emas = %q / %q (%v)", v.Name, v.Varian, err)
	}
	if _, err := kirim(app, url.Values{"jenis": {"gold"}, "nama": {"A"}, "mata_uang": {"IDR"}}); err == nil {
		t.Error("merek satu huruf diterima, mau ditolak")
	}
}
