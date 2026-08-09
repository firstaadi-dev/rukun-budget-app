package main

import (
	"bytes"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Semua halaman harus bisa dirender dengan data yang bentuknya sama dengan
// yang dikirim handler. Test ini menangkap salah nama field, blok yang belum
// didefinisikan, dan template yang tidak ter-parse — tanpa perlu database.
func TestPagesRender(t *testing.T) {
	pages := parsePages()

	today := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)
	wallets := []Wallet{
		{ID: 1, Name: "Tunai", Type: "cash", Currency: "IDR", BalanceMinor: 85_000_000},
		{ID: 2, Name: "Rekening Utama", Type: "bank", Provider: "BCA", Currency: "IDR", BalanceMinor: 1_250_000_000},
		{ID: 5, Name: "Kartu Kredit BCA", Type: "credit", Provider: "BCA", Currency: "IDR", BalanceMinor: -210_000_000},
		{ID: 6, Name: "Tabungan USD", Type: "bank", Provider: "Mandiri", Currency: "USD", BalanceMinor: 100000},
	}
	transfer := Tx{
		ID: 3, Kind: "transfer", Date: today.AddDate(0, 0, -1),
		WalletID: 2, WalletName: "Rekening Utama", WalletCur: "IDR", AmountMinor: 1_555_000_000,
		ToWalletID: 6, ToWalletName: "Tabungan USD", ToWalletCur: "USD", AmountInMino: 100000,
		AdminFee: 5_000_000, Note: "Nabung dalam USD", CreatedBy: "Ayah", CreatedAt: today,
	}
	txs := []Tx{
		{ID: 1, Kind: "income", Date: today, WalletID: 2, WalletName: "Rekening Utama", WalletCur: "IDR",
			AmountMinor: 1_500_000_000, Category: "Gaji", Note: "Gaji Agustus", CreatedBy: "Ayah", CreatedAt: today},
		{ID: 2, Kind: "expense", Date: today, WalletID: 1, WalletName: "Tunai", WalletCur: "IDR",
			AmountMinor: 45_000_000, Category: "Belanja", CreatedBy: "Ibu", CreatedAt: today},
		transfer,
	}
	views := viewTxs(txs, today)
	rates := map[string]Rate{
		"USD>IDR": {FromMinor: 100000, From: "USD", ToMinor: 1_550_000_000, To: "IDR"},
		"IDR>USD": {FromMinor: 1_550_000_000, From: "IDR", ToMinor: 100000, To: "USD"},
	}
	form := map[string]string{"jenis": "bank", "mata_uang": "IDR", "saldo_awal": "0", "tanggal": "2026-08-07"}

	cases := []struct {
		page string
		data map[string]any
		want string
	}{
		{"dashboard.html", map[string]any{
			"Title": "Dashboard", "Nav": "dashboard", "Today": tanggalPanjang(today),
			"Summary": summarize(wallets, rates, "IDR"),
			"Wallets": viewWallets(wallets), "Recent": views,
			"Cards": []CardView{kartuUji(today)},
			"Breakdown": breakdown([]CategorySpend{
				{Kind: "expense", Category: "Belanja", Currency: "IDR", Minor: 45_000_000},
				{Kind: "expense", Category: "Tagihan", Currency: "IDR", Minor: 35_000_000},
				{Kind: "expense", Category: "Sekolah", Currency: "USD", Minor: 20000},
				{Kind: "income", Category: "Gaji", Currency: "IDR", Minor: 1_500_000_000},
			}, rates, "IDR", "Agustus 2026"),
		}, "Per Kategori"},

		// Dashboard yang sedang membawa peringatan tagihan. Dipisah jadi kasus
		// sendiri karena bagian itu hanya muncul saat ada yang perlu dikerjakan,
		// jadi dashboard yang tenang tidak pernah merendernya.
		{"dashboard.html", map[string]any{
			"Title": "Dashboard", "Nav": "dashboard", "Today": tanggalPanjang(today),
			"Summary":   summarize(wallets, rates, "IDR"),
			"Perhatian": perhatian([]CardView{kartuTempo(5, "Kartu Kredit BCA", -2, 200_000_000, 0, today)}),
		}, "Tagihan telat 2 hari"},

		{"dompet.html", map[string]any{
			"Title": "Dompet", "Nav": "dompet",
			"Wallets": withCards(viewWallets(wallets), []CardView{kartuUji(today)}),
		}, "Terpakai"},

		{"dompet_form.html", map[string]any{
			"Title": "Dompet Baru", "Nav": "dompet", "Back": "/dompet", "Action": "/dompet/baru",
			"Form": form, "Types": walletTypes, "ProvidersJSON": jsonAttr(walletProviders),
			"Currencies": Currencies,
		}, "Simpan Dompet"},

		{"transaksi.html", map[string]any{
			"Title": "Transaksi", "Nav": "transaksi", "Filter": "",
			"Filters":  txFilterOptions(url.Values{}, ""),
			"Kategori": "", "Categories": []Category{{ID: 1, Kind: "expense", Name: "Belanja"}},
			"Cari": "", "Periode": bacaPeriode("", today),
			"URLPrev": "/transaksi?periode=2026-07", "URLNext": "/transaksi?periode=2026-09",
			"URLSemua": "/transaksi?periode=semua", "URLBulanIni": "/transaksi",
			"URLTanpaCari": "/transaksi", "URLTanpaKategori": "/transaksi",
			"Groups": groupTxs(views),
		}, "Agustus 2026"},

		// Daftar yang kosong karena pencariannya tidak menemukan apa-apa, dan
		// yang terpotong di batas halaman. Keduanya blok tersendiri, dan
		// keduanya justru yang paling menyesatkan kalau salah kata.
		{"transaksi.html", map[string]any{
			"Title": "Transaksi", "Nav": "transaksi", "Filter": "",
			"Filters":  txFilterOptions(url.Values{"cari": {"listrik"}}, ""),
			"Kategori": "", "Categories": []Category{{ID: 1, Kind: "expense", Name: "Belanja"}},
			"Cari": "listrik", "Periode": bacaPeriode("", today),
			"URLPrev": "/transaksi?cari=listrik&periode=2026-07", "URLNext": "/transaksi?cari=listrik&periode=2026-09",
			"URLSemua": "/transaksi?cari=listrik&periode=semua", "URLBulanIni": "/transaksi?cari=listrik",
			"URLTanpaCari": "/transaksi", "URLTanpaKategori": "/transaksi?cari=listrik",
			"Terpotong": true, "Batas": txListLimit,
		}, "Cari di seluruh waktu"},

		{"transaksi_form.html", map[string]any{
			"Title": "Catat Pengeluaran", "Nav": "transaksi", "Back": "/transaksi",
			"Action": "/transaksi/baru?jenis=expense", "Kind": "expense", "KindLabel": "Pengeluaran",
			"Form": form, "Wallets": viewWallets(wallets),
			"Categories": []string{"Belanja", "Tagihan", "Transportasi"},
		}, "Transportasi"},

		{"transfer_form.html", map[string]any{
			"Title": "Transfer Antar Dompet", "Nav": "transaksi", "Back": "/transaksi",
			"Action": "/transaksi/baru?jenis=transfer", "Kind": "transfer", "KindLabel": "Transfer",
			"Form": form, "Wallets": viewWallets(wallets),
			"RatesJSON": jsonAttr(ratesForJS(nil, rates)), "Base": "IDR",
		}, "Biaya Admin"},

		{"detail.html", map[string]any{
			"Title": "Detail Transaksi", "Nav": "transaksi", "Back": "/transaksi",
			"Tx": viewTx(transfer, today), "KindLabel": "Transfer",
			"Waktu": "6 Agustus 2026, 14:32", "Amount": Format(transfer.AmountMinor, "IDR"),
			"AmountIn": Format(transfer.AmountInMino, "USD"), "Fee": Format(transfer.AdminFee, "IDR"),
			"RateCustom": true,
		}, "1 USD = Rp15.500"},

		{"kategori.html", map[string]any{
			"Title": "Kategori", "Nav": "kategori",
			"Expense": []Category{{ID: 1, Kind: "expense", Name: "Belanja", Usage: 3},
				{ID: 2, Kind: "expense", Name: "Pendidikan Anak"}},
			"Income": []Category{{ID: 9, Kind: "income", Name: "Gaji", Usage: 1}},
		}, "Pendidikan Anak"},

		{"kategori_form.html", map[string]any{
			"Title": "Ubah Kategori", "Nav": "kategori", "Back": "/kategori",
			"Action": "/kategori/1/ubah", "ID": int64(1),
			"Form":  map[string]string{"jenis": "expense", "nama": "Belanja"},
			"Kinds": []struct{ Value, Label string }{{"expense", "Pengeluaran"}, {"income", "Pemasukan"}},
		}, "Simpan Kategori"},

		{"hutang.html", map[string]any{
			"Title": "Hutang & Piutang", "Nav": "hutang",
			"Summary": summarizeDebts([]Party{
				{ID: 1, Name: "Pak Budi", Saldo: []PartyBalance{
					{Currency: "IDR", HutangMinor: 500_000_000, PiutangMinor: 200_000_000}}},
				{ID: 2, Name: "Sudah Lunas"},
			}, rates, "IDR"),
		}, "Selisih Bersih"},

		{"pihak.html", map[string]any{
			"Title": "Pak Budi", "Nav": "hutang", "Back": "/hutang",
			"Party": viewParty(Party{ID: 1, Name: "Pak Budi", Note: "Tetangga",
				Saldo: []PartyBalance{{Currency: "IDR", HutangMinor: 300_000_000}}}),
			"Groups": groupTxs(viewTxs([]Tx{{
				ID: 20, Kind: "debt_in", Date: today, WalletID: 2, WalletName: "Rekening Utama",
				WalletCur: "IDR", AmountMinor: 500_000_000, PartyID: 1, PartyName: "Pak Budi",
			}}, today)),
		}, "Bayar Hutang"},

		{"hutang_form.html", map[string]any{
			"Title": "Catat Hutang", "Nav": "hutang", "Back": "/hutang",
			"Form":       map[string]string{"jenis": "debt_in", "tanggal": "2026-08-07", "mata_uang": "IDR"},
			"Kinds":      debtKinds,
			"Wallets":    viewWallets(wallets),
			"Parties":    []Party{{ID: 1, Name: "Pak Budi"}},
			"Currencies": Currencies,
		}, "Tanpa dompet"},

		{"bayar_form.html", map[string]any{
			"Title": "Bayar Hutang ke Pak Budi", "Nav": "hutang", "Back": "/hutang/pihak/1",
			"Party": viewParty(Party{ID: 1, Name: "Pak Budi",
				Saldo: []PartyBalance{{Currency: "IDR", HutangMinor: 300_000_000}}}),
			"Form":    map[string]string{"arah": "hutang", "tanggal": "2026-08-07", "mata_uang": "IDR", "nominal": "3.000.000"},
			"Wallets": viewWallets(wallets), "Currencies": Currencies,
		}, "Catat Pembayaran"},

		// Dilihat sebagai kepala keluarga: hanya dia yang melihat tombol
		// pencabutan akses, jadi hanya dari sudut ini blok itu ikut terender.
		{"pengaturan.html", map[string]any{
			"Title": "Akun", "Nav": "akun",
			"User": User{ID: 1, Name: "Ayah", FamilyID: 1, FamilyName: "Keluarga Santoso", Kepala: true},
			"Members": []Member{
				{User: User{ID: 1, Name: "Ayah", Kepala: true}, CreatedAt: today.AddDate(0, -6, 0), Txs: 42},
				{User: User{ID: 2, Name: "Ibu"}, CreatedAt: today.AddDate(0, -3, 0), Txs: 17},
				{User: User{ID: 3, Name: "Anak Sulung", Disabled: true}, CreatedAt: today},
			},
			"Sukses": pesanSukses["nonaktif"],
		}, "Cabut akses"},

		{"masuk.html", map[string]any{"NoChrome": true,
			"Form": map[string]string{"Kode": "abcde-fghij-klmno"}}, "Kode Keluarga"},
		{"daftar.html", map[string]any{"NoChrome": true,
			"Form":  map[string]string{"Nama": "Ayah", "Kode": "abcde-fghij-klmno"},
			"Error": "Kode undangan tidak cocok dengan keluarga mana pun."}, "Kode Undangan"},
	}

	for _, c := range cases {
		tmpl, ok := pages[c.page]
		if !ok {
			t.Errorf("%s: template tidak ditemukan", c.page)
			continue
		}
		// Halaman yang perannya menentukan isi layar membawa User-nya sendiri.
		if _, ok := c.data["User"]; !ok {
			c.data["User"] = User{ID: 1, Name: "Ayah", FamilyID: 1, FamilyName: "Keluarga Santoso"}
		}
		if _, ok := c.data["Family"]; !ok {
			c.data["Family"] = "Keluarga Santoso"
		}

		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, "layout.html", c.data); err != nil {
			t.Errorf("%s: %v", c.page, err)
			continue
		}
		if !strings.Contains(buf.String(), c.want) {
			t.Errorf("%s: keluaran tidak memuat %q", c.page, c.want)
		}
	}
}

// Total dashboard harus menjumlahkan dompet lintas mata uang lewat kurs, dan
// melaporkan mata uang yang kursnya belum diketahui alih-alih diam-diam
// menganggapnya nol.
// kartuUji: satu kartu kredit dengan siklus terisi, untuk menguji rendernya.
func kartuUji(today time.Time) CardView {
	wl := Wallet{ID: 5, Name: "Kartu Kredit BCA", Type: "credit", Provider: "BCA",
		Currency: "IDR", BalanceMinor: -280_000_000, SettlementDay: 25, PaymentDay: 15}
	settlement := SettlementTerakhir(today, wl.SettlementDay, today.Location())
	return viewCard(CardStatus{
		Wallet:           wl,
		PayableMinor:     200_000_000,
		OutstandingMinor: wl.BalanceMinor,
		Settlement:       settlement,
		Due:              JatuhTempo(settlement, wl.PaymentDay, today.Location()),
	}, today)
}

func TestSummarize(t *testing.T) {
	wallets := []Wallet{
		{Type: "bank", Currency: "IDR", BalanceMinor: 1_000_000_000},  // Rp10.000.000
		{Type: "credit", Currency: "IDR", BalanceMinor: -200_000_000}, // terpakai Rp2.000.000
		{Type: "bank", Currency: "USD", BalanceMinor: 100000},         // $1.000
		{Type: "bank", Currency: "SGD", BalanceMinor: 50000},          // kurs belum ada
	}
	rates := map[string]Rate{
		"USD>IDR": {FromMinor: 100000, From: "USD", ToMinor: 1_550_000_000, To: "IDR"},
	}
	s := summarize(wallets, rates, "IDR")

	if s.TotalDebit != "Rp25.500.000" {
		t.Errorf("TotalDebit = %s, mau Rp25.500.000", s.TotalDebit)
	}
	if s.TotalCredit != "Rp2.000.000" {
		t.Errorf("TotalCredit = %s, mau Rp2.000.000", s.TotalCredit)
	}
	if s.TotalOwned != "Rp23.500.000" {
		t.Errorf("TotalOwned = %s, mau Rp23.500.000", s.TotalOwned)
	}
	if len(s.Unconverted) != 1 || s.Unconverted[0] != "SGD" {
		t.Errorf("Unconverted = %v, mau [SGD]", s.Unconverted)
	}
}

func TestGroupTxs(t *testing.T) {
	// "Hari ini" mengikuti zona keluarga, sedangkan tanggal transaksi datang
	// dari kolom DATE sebagai tengah malam UTC. Test ini sengaja memakai dua
	// zona berbeda supaya label tidak bisa lagi meleset sehari.
	jakarta := time.FixedZone("WIB", 7*3600)
	today := time.Date(2026, 8, 7, 0, 0, 0, 0, jakarta)

	// pgx mengembalikan kolom DATE sebagai tengah malam UTC pada tanggal
	// kalendernya, bukan tengah malam waktu setempat.
	tgl := func(d int) Tx {
		return Tx{Kind: "expense", Date: time.Date(2026, 8, d, 0, 0, 0, 0, time.UTC),
			WalletCur: "IDR", AmountMinor: 100, Category: "Lainnya"}
	}
	groups := groupTxs(viewTxs([]Tx{tgl(7), tgl(7), tgl(6), tgl(4)}, today))

	if len(groups) != 3 {
		t.Fatalf("jumlah grup = %d, mau 3", len(groups))
	}
	if groups[0].Label != "Hari ini" || len(groups[0].Items) != 2 {
		t.Errorf("grup pertama = %s (%d item)", groups[0].Label, len(groups[0].Items))
	}
	if groups[1].Label != "Kemarin" {
		t.Errorf("grup kedua = %s, mau Kemarin", groups[1].Label)
	}
	if groups[2].Label != "4 Agustus 2026" {
		t.Errorf("grup ketiga = %s, mau 4 Agustus 2026", groups[2].Label)
	}
}
