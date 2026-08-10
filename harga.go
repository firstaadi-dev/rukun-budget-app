package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Harga pasar saham, ETF, dan emas diambil dari endpoint chart Yahoo Finance:
// tanpa API key, tanpa pendaftaran, dan menyebut mata uang kuotasinya sendiri
// sehingga tidak perlu ditebak dari simbolnya.
//
// Yang dipakai penutupan terakhir, bukan harga berjalan. Bursa Amerika tutup
// saat sebagian besar keluarga di sini melihat layarnya, dan angka yang
// bergerak tiap detik tidak mengubah satu pun keputusan yang diambil dari
// halaman ini.
//
// Sumber ini tidak resmi dan bisa berubah sewaktu-waktu. Karena itu setiap
// posisi tetap boleh menyimpan harga yang diisi sendiri, dan kegagalan
// pengambilan tidak pernah mengosongkan layar — yang tampil harga terakhir
// yang diketahui, lengkap dengan kapan ia diambil.
const defaultHargaURL = "https://query1.finance.yahoo.com/v8/finance/chart/"

// Host cadangan. Yahoo menyajikan endpoint yang sama dari query1 dan query2;
// keduanya kadang dibatasi terpisah, jadi yang satu masih menjawab saat yang
// lain sudah 429.
//
// Ini peredam, bukan obat: keduanya satu penyedia, dan pembatasannya dihitung
// per alamat IP. Deploy di Render memakai alamat bersama, jadi 429 datang bukan
// karena aplikasi ini rakus melainkan karena tetangganya — dan saat batasnya
// kena, kedua host biasanya menolak bersamaan.
const defaultCadanganURL = "https://query2.finance.yahoo.com/v8/finance/chart/"

// Twelve Data: sumber ketiga yang benar-benar terpisah, dan satu-satunya jalan
// keluar nyata dari 429 di atas. Ia menuntut pendaftaran — tier gratisnya tanpa
// kartu — jadi ia mati sendiri selama HARGA_TWELVE_KEY belum diisi, dan tidak
// ada yang berubah bagi yang tidak mengisinya.
//
// Batasnya dihitung per akun, bukan per alamat IP, jadi tetangga di Render
// tidak ikut menghabiskannya. Ia juga menyebut mata uang kuotasinya sendiri,
// sama seperti Yahoo, sehingga tidak ada yang perlu ditebak dari simbolnya.
const defaultTwelveURL = "https://api.twelvedata.com/quote"

// diam429: sesudah sumber utama membalas 429, ia dilewati selama ini dan
// permintaan langsung jatuh ke cadangan. Menekan "coba lagi" berulang kali ke
// sumber yang sedang menolak justru memperpanjang penolakannya — dan selama
// jeda itu host cadangan tetap dicoba, jadi tidak ada yang hilang.
const diam429 = 15 * time.Minute

// simbolEmas: kontrak berjangka emas COMEX, dikuotasi dalam dolar per troy
// ounce. Ini harga emas dunia, bukan harga jual gerai Antam — lihat
// GramPerTroyOunce dan catatannya di investasi.go.
const simbolEmas = "GC=F"

// hargaSegar: selama masih semuda ini, harga yang tersimpan dipakai apa adanya.
// Lewat dari itu ia tetap dipakai, tapi diperbarui di latar belakang.
const hargaSegar = time.Hour

// hargaBudget: waktu paling lama yang boleh dihabiskan sebuah permintaan
// halaman untuk menunggu harga yang belum pernah diambil sama sekali. Sesudah
// itu halamannya tetap tampil, hanya tanpa harga pasar.
const hargaBudget = 5 * time.Second

// Kuotasi: satu harga satuan seperti yang dilaporkan sumbernya.
type Kuotasi struct {
	// PriceE4: harga satuan dalam satuan terkecil mata uangnya, dikali 10.000.
	// Skala yang sama dipakai harga yang diisi sendiri — lihat migrasi 007.
	PriceE4  int64
	Currency string
	// At: waktu penutupan yang dilaporkan sumbernya. Untuk NAB reksadana,
	// sumbernya tidak menerbitkan tanggal sama sekali, jadi yang terisi waktu
	// pengambilan — dan Diambil menandai bedanya supaya labelnya di layar tidak
	// mengaku tahu hal yang tidak diketahui.
	At      time.Time
	Diambil bool
	// Nama: nama resmi instrumennya seperti disebut sumbernya. Ikut di sini
	// karena endpoint yang sama sudah membawanya — mengambilnya lewat
	// permintaan kedua cuma menambah satu hal lagi yang bisa gagal sendiri.
	Nama string
}

func (k Kuotasi) Ada() bool { return k.PriceE4 > 0 && k.Currency != "" }

type hargaSource struct {
	base     string
	cadangan string
	// twelveURL dan twelveKey: sumber ketiga. Kunci kosong berarti ia mati.
	twelveURL string
	twelveKey string
	client    *http.Client

	mu    sync.RWMutex
	cache map[string]Kuotasi
	// ambil: waktu pengambilan terakhir per simbol, dipakai memutuskan perlu
	// tidaknya menyegarkan. Terpisah dari Kuotasi.At, yang waktu bursanya:
	// akhir pekan membuat At diam tiga hari sementara pengambilannya tetap
	// berjalan, dan menyamakan keduanya akan membuat simbol itu diambil ulang
	// terus-menerus.
	ambil   map[string]time.Time
	lastErr error
	// diam: sampai kapan sumber utama dilewati karena sedang membalas 429.
	diam time.Time
	// jalan: simbol yang sedang diambil di latar belakang, supaya satu halaman
	// yang dibuka berkali-kali tidak menumpuk permintaan ke sumber yang sama.
	jalan map[string]bool
}

func newHargaSource(base, cadangan string) *hargaSource {
	return &hargaSource{
		base:      base,
		cadangan:  cadangan,
		twelveURL: defaultTwelveURL,
		client:    &http.Client{Timeout: 10 * time.Second},
		cache:     map[string]Kuotasi{},
		ambil:     map[string]time.Time{},
		jalan:     map[string]bool{},
	}
}

func (s *hargaSource) enabled() bool { return s != nil && s.base != "" }

type yahooChart struct {
	Chart struct {
		Result []struct {
			Meta struct {
				Currency           string  `json:"currency"`
				Symbol             string  `json:"symbol"`
				ShortName          string  `json:"shortName"`
				LongName           string  `json:"longName"`
				RegularMarketPrice float64 `json:"regularMarketPrice"`
				RegularMarketTime  int64   `json:"regularMarketTime"`
			} `json:"meta"`
		} `json:"result"`
		Error *struct {
			Code        string `json:"code"`
			Description string `json:"description"`
		} `json:"error"`
	} `json:"chart"`
}

// fetch mencoba ketiga sumber berurutan sampai ada yang menjawab. Yang gagal
// dicatat semua, lalu ikut di pesan galatnya: "status 429" saja tidak memberi
// tahu apakah cadangannya sudah dicoba, atau apakah sumber ketiganya memang
// belum diaktifkan.
func (s *hargaSource) fetch(ctx context.Context, symbol string) (Kuotasi, error) {
	var gagal []string

	if s.sedangDiam() {
		gagal = append(gagal, "utama: dilewati sementara sesudah 429")
	} else if k, err := s.fetchYahoo(ctx, s.base, symbol); err == nil {
		return k, nil
	} else {
		gagal = append(gagal, "utama: "+err.Error())
	}

	if s.cadangan != "" {
		if k, err := s.fetchYahoo(ctx, s.cadangan, symbol); err == nil {
			log.Printf("harga %s: dipakai host cadangan setelah %s", symbol, strings.Join(gagal, "; "))
			return k, nil
		} else {
			gagal = append(gagal, "cadangan: "+err.Error())
		}
	}

	if s.twelveKey == "" {
		gagal = append(gagal, "twelvedata: HARGA_TWELVE_KEY belum diisi")
	} else if k, err := s.fetchTwelve(ctx, symbol); err == nil {
		log.Printf("harga %s: dipakai Twelve Data setelah %s", symbol, strings.Join(gagal, "; "))
		return k, nil
	} else {
		gagal = append(gagal, "twelvedata: "+err.Error())
	}

	return Kuotasi{}, fmt.Errorf("harga %s gagal di semua sumber — %s", symbol, strings.Join(gagal, "; "))
}

type twelveQuote struct {
	Symbol    string `json:"symbol"`
	Name      string `json:"name"`
	Currency  string `json:"currency"`
	Close     string `json:"close"`
	Timestamp int64  `json:"timestamp"`
	// Galat dijawab dengan bentuk yang berbeda, di badan yang sama.
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

// fetchTwelve mengambil satu kuotasi dari Twelve Data. Simbolnya dipakai apa
// adanya: kode yang dipahami Yahoo — VOO, SPUS, AAPL — dipahami juga di sini.
//
// Emas adalah pengecualiannya. Kontrak berjangka COMEX tidak ada di sini, jadi
// yang dipakai emas spot XAU/USD: sama-sama dolar per troy ounce, selisihnya
// beberapa dolar, jauh lebih kecil daripada selisih harga gerai yang memang
// sudah jadi catatan tersendiri di investasi.go.
func (s *hargaSource) fetchTwelve(ctx context.Context, symbol string) (Kuotasi, error) {
	kode := symbol
	if kode == simbolEmas {
		kode = "XAU/USD"
	}
	u := s.twelveURL + "?symbol=" + url.QueryEscape(kode) + "&apikey=" + url.QueryEscape(s.twelveKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return Kuotasi{}, err
	}
	req.Header.Set("User-Agent", "Rukun/1.0 (+https://github.com/firsta/rukun)")

	resp, err := s.client.Do(req)
	if err != nil {
		return Kuotasi{}, err
	}
	defer resp.Body.Close()

	// Kuncinya ada di URL, jadi pesan galatnya tidak boleh ikut apa adanya ke
	// layar maupun ke log — yang disebut cuma kode statusnya.
	if resp.StatusCode != http.StatusOK {
		return Kuotasi{}, fmt.Errorf("status %d", resp.StatusCode)
	}
	var body twelveQuote
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Kuotasi{}, err
	}
	if body.Status == "error" {
		return Kuotasi{}, fmt.Errorf("kode %d: %s", body.Code, body.Message)
	}

	// Harganya dikirim sebagai teks, bukan angka.
	tutup, err := strconv.ParseFloat(strings.TrimSpace(body.Close), 64)
	if err != nil {
		return Kuotasi{}, fmt.Errorf("harga penutupan %q tidak terbaca", body.Close)
	}
	e4, ok := priceToE4(tutup, body.Currency)
	if !ok {
		return Kuotasi{}, fmt.Errorf("nilai %v %s tidak masuk akal", tutup, body.Currency)
	}
	k := Kuotasi{PriceE4: e4, Currency: body.Currency,
		Nama: strings.Join(strings.Fields(body.Name), " ")}
	if body.Timestamp > 0 {
		k.At = time.Unix(body.Timestamp, 0).UTC()
	} else {
		k.At, k.Diambil = time.Now().UTC(), true
	}
	return k, nil
}

// Endpoint spark menjawab banyak simbol dalam satu permintaan, tanpa kunci.
// Ini yang membuat satu keluarga dengan sepuluh posisi cukup mengetuk sumbernya
// sekali, bukan sepuluh kali — dan pembatasan yang dihitung per alamat IP jelas
// lebih jarang kena dengan sepersepuluh permintaan.
//
// Yang tidak dibawanya: mata uang dan nama instrumennya. Karena itu ia hanya
// dipakai untuk simbol yang mata uangnya sudah diketahui dari pengambilan
// sebelumnya — cache di memori, yang saat start diisi dari tabel quotes. Simbol
// yang benar-benar baru tetap lewat endpoint chart satu per satu, sekali saja,
// dan sesudah itu ikut borongan.
type yahooSpark map[string]struct {
	Symbol    string    `json:"symbol"`
	Timestamp []int64   `json:"timestamp"`
	Close     []float64 `json:"close"`
}

// fetchSpark mengambil harga penutupan terakhir untuk banyak simbol sekaligus.
func (s *hargaSource) fetchSpark(ctx context.Context, base string, symbols []string) (map[string]Kuotasi, error) {
	// Path-nya bersaudara dengan endpoint chart, satu tingkat di atas nama
	// akhirnya — sama seperti endpoint pencarian di cariSimbol.
	u := strings.TrimSuffix(base, "chart/") + "spark?range=5d&interval=1d&symbols=" +
		url.QueryEscape(strings.Join(symbols, ","))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Rukun/1.0 (+https://github.com/firsta/rukun)")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusTooManyRequests {
			s.tahan429(resp.Header.Get("Retry-After"))
		}
		return nil, fmt.Errorf("status %d dari %s: %s", resp.StatusCode, u, cuplik(resp.Body))
	}

	var body yahooSpark
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[string]Kuotasi{}
	for sym, sp := range body {
		// Mata uangnya tidak ada di balasan ini, jadi diambil dari yang sudah
		// diketahui. Yang belum pernah terlihat dilewati — menebaknya berarti
		// menyimpan harga dengan satuan karangan.
		lama, ada := s.cache[sym]
		if !ada || lama.Currency == "" {
			continue
		}
		tutup, waktu, ok := penutupanTerakhir(sp.Close, sp.Timestamp)
		if !ok {
			continue
		}
		e4, ok := priceToE4(tutup, lama.Currency)
		if !ok {
			continue
		}
		out[sym] = Kuotasi{PriceE4: e4, Currency: lama.Currency, At: waktu, Nama: lama.Nama}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("tidak ada simbol terbaca dari %d yang diminta", len(symbols))
	}
	return out, nil
}

// penutupanTerakhir mengambil harga terakhir yang benar-benar ada. Deret dari
// spark bisa berakhir dengan nol untuk hari bursa yang belum tutup, dan memakai
// elemen terakhir apa adanya membuat harga jatuh ke nol tiap pagi.
func penutupanTerakhir(closes []float64, stamps []int64) (float64, time.Time, bool) {
	for i := len(closes) - 1; i >= 0; i-- {
		if closes[i] <= 0 {
			continue
		}
		var waktu time.Time
		if i < len(stamps) && stamps[i] > 0 {
			waktu = time.Unix(stamps[i], 0).UTC()
		}
		return closes[i], waktu, true
	}
	return 0, time.Time{}, false
}

// seed mengisi cache dari harga yang tersimpan di database, dipanggil sekali
// saat start.
//
// Waktu pengambilannya sengaja dibiarkan kosong, jadi seluruhnya terhitung basi
// dan disegarkan di latar belakang. Yang penting halaman pertama sesudah
// instance bangun langsung berisi angka — dan mata uangnya diketahui, sehingga
// penyegaran itu bisa lewat satu permintaan borongan.
func (s *hargaSource) seed(quotes map[string]Kuotasi) {
	if !s.enabled() || len(quotes) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for sym, k := range quotes {
		if k.Ada() {
			s.cache[sym] = k
		}
	}
}

func (s *hargaSource) sedangDiam() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return time.Now().Before(s.diam)
}

// tahan429 menidurkan sumber utama. Retry-After dihormati kalau ada dan masuk
// akal: sumbernya sendiri yang paling tahu kapan ia mau dihubungi lagi.
func (s *hargaSource) tahan429(retryAfter string) {
	jeda := diam429
	if d, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && d > 0 && d < 3600 {
		jeda = time.Duration(d) * time.Second
	}
	s.mu.Lock()
	s.diam = time.Now().Add(jeda)
	s.mu.Unlock()
	log.Printf("harga: sumber utama membalas 429, dilewati %s ke depan", jeda)
}

func (s *hargaSource) fetchYahoo(ctx context.Context, base, symbol string) (Kuotasi, error) {
	u := base + url.PathEscape(symbol) + "?range=5d&interval=1d"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return Kuotasi{}, err
	}
	// Tanpa User-Agent, endpoint ini kadang membalas 429 sekalipun permintaannya
	// jarang. Yang disebut di sini aplikasinya sendiri, bukan peramban: kalau
	// sumbernya memang sedang menolak, jalan keluarnya sumber cadangan, bukan
	// menyamar jadi orang lain.
	req.Header.Set("User-Agent", "Rukun/1.0 (+https://github.com/firsta/rukun)")

	resp, err := s.client.Do(req)
	if err != nil {
		return Kuotasi{}, fmt.Errorf("harga %s: %s: %w", symbol, u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusTooManyRequests {
			s.tahan429(resp.Header.Get("Retry-After"))
		}
		// Cuplikan badan balasannya ikut: 429 dari Yahoo kadang menjelaskan
		// dirinya, dan tanpa itu yang tercatat cuma angka status.
		return Kuotasi{}, fmt.Errorf("harga %s: status %d dari %s: %s",
			symbol, resp.StatusCode, u, cuplik(resp.Body))
	}

	var body yahooChart
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Kuotasi{}, err
	}
	if body.Chart.Error != nil {
		return Kuotasi{}, fmt.Errorf("harga %s: %s", symbol, body.Chart.Error.Description)
	}
	if len(body.Chart.Result) == 0 {
		return Kuotasi{}, fmt.Errorf("harga %s: kosong", symbol)
	}
	m := body.Chart.Result[0].Meta
	e4, ok := priceToE4(m.RegularMarketPrice, m.Currency)
	if !ok {
		return Kuotasi{}, fmt.Errorf("harga %s: nilai %v %s tidak masuk akal", symbol, m.RegularMarketPrice, m.Currency)
	}
	nama := m.ShortName
	if nama == "" {
		nama = m.LongName
	}
	return Kuotasi{PriceE4: e4, Currency: m.Currency, Nama: strings.Join(strings.Fields(nama), " "),
		At: time.Unix(m.RegularMarketTime, 0).UTC()}, nil
}

// cuplik membaca sedikit awal badan balasan untuk dititipkan ke pesan galat.
// Dipangkas dan diratakan spasinya: yang dicari petunjuk di log, bukan salinan
// halaman galat setinggi layar.
func cuplik(r io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, 300))
	s := strings.Join(strings.Fields(string(b)), " ")
	if s == "" {
		return "(kosong)"
	}
	return s
}

// priceToE4 mengubah harga pecahan dari sumber jadi bilangan bulat berskala.
// Di luar rentang yang masuk akal ia ditolak, bukan disimpan sebagai angka
// salah: harga yang meleset diam-diam lebih buruk daripada harga yang hilang.
func priceToE4(price float64, cur string) (int64, bool) {
	if !(price > 0) || math.IsInf(price, 0) || cur == "" {
		return 0, false
	}
	v := math.Round(price * float64(pow10(Exp(cur))) * priceScale)
	if v < 1 || v > math.MaxInt64/2 {
		return 0, false
	}
	return int64(v), true
}

// harga mengembalikan kuotasi untuk simbol yang diminta.
//
// Simbol yang belum pernah diambil dijemput sekarang juga, dalam anggaran waktu
// yang dijepit: halaman pertama yang dibuka sebaiknya sudah berisi. Simbol yang
// sudah punya nilai tapi mulai basi dikembalikan apa adanya dan disegarkan di
// latar belakang — menunggu jaringan untuk memperbaiki angka yang cuma berbeda
// beberapa jam bukan pertukaran yang sepadan.
func (s *hargaSource) harga(ctx context.Context, symbols []string) map[string]Kuotasi {
	if !s.enabled() || len(symbols) == 0 {
		return nil
	}

	out := map[string]Kuotasi{}
	var baru, basi []string
	s.mu.RLock()
	for _, sym := range symbols {
		if sym == "" {
			continue
		}
		if _, sudah := out[sym]; sudah {
			continue
		}
		k, ada := s.cache[sym]
		switch {
		case !ada:
			baru = append(baru, sym)
		default:
			out[sym] = k
			if time.Since(s.ambil[sym]) > hargaSegar {
				basi = append(basi, sym)
			}
		}
	}
	s.mu.RUnlock()

	s.latar(basi)
	if len(baru) == 0 {
		return out
	}

	tunggu, batal := context.WithTimeout(ctx, hargaBudget)
	defer batal()

	type hasil struct {
		sym string
		k   Kuotasi
	}
	ch := make(chan hasil, len(baru))
	for _, sym := range baru {
		go func(sym string) {
			k, err := s.fetch(tunggu, sym)
			s.simpan(sym, k, err)
			ch <- hasil{sym, k}
		}(sym)
	}
	for range baru {
		select {
		case h := <-ch:
			if h.k.Ada() {
				out[h.sym] = h.k
			}
		case <-tunggu.Done():
			return out
		}
	}
	return out
}

// latar menyegarkan simbol yang mulai basi tanpa menahan permintaan yang sedang
// berjalan. Konteksnya sengaja bukan konteks permintaan itu: halaman yang sudah
// selesai dikirim akan membatalkannya di tengah jalan.
//
// Seluruhnya dijemput dalam satu permintaan borongan. Sebelumnya tiap simbol
// punya permintaannya sendiri, dan sepuluh posisi berarti sepuluh ketukan ke
// sumber yang membatasi per alamat IP — kelipatan yang tidak dibutuhkan siapa
// pun. Yang gagal di borongan jatuh ke pengambilan satu per satu, karena di
// situlah mata uang dan namanya bisa didapat.
func (s *hargaSource) latar(symbols []string) {
	s.mu.Lock()
	var ambil []string
	for _, sym := range symbols {
		if !s.jalan[sym] {
			s.jalan[sym] = true
			ambil = append(ambil, sym)
		}
	}
	s.mu.Unlock()
	if len(ambil) == 0 {
		return
	}

	go func() {
		ctx, batal := context.WithTimeout(context.Background(), 30*time.Second)
		defer batal()
		s.jemput(ctx, ambil)

		s.mu.Lock()
		for _, sym := range ambil {
			delete(s.jalan, sym)
		}
		s.mu.Unlock()
	}()
}

// jemput mengambil sekumpulan simbol: borongan dulu, sisanya satu per satu.
func (s *hargaSource) jemput(ctx context.Context, symbols []string) {
	sisa := symbols
	if len(symbols) > 1 && !s.sedangDiam() {
		borongan, err := s.fetchSpark(ctx, s.base, symbols)
		if err != nil {
			log.Printf("harga borongan %d simbol gagal, dicoba satu per satu: %v", len(symbols), err)
		}
		sisa = nil
		for _, sym := range symbols {
			if k, ok := borongan[sym]; ok {
				s.simpan(sym, k, nil)
				continue
			}
			sisa = append(sisa, sym)
		}
	}
	for _, sym := range sisa {
		k, err := s.fetch(ctx, sym)
		s.simpan(sym, k, err)
	}
}

// segarkan menjemput ulang seluruh simbol sekarang juga dan menunggu hasilnya.
// Dipakai tombol "Ambil ulang harga": yang menekannya sedang menunggu jawaban,
// bukan menunggu latar belakang, jadi umur harga tersimpan tidak dilihat sama
// sekali — begitu juga jeda 429, karena permintaannya datang dari orangnya
// sendiri, bukan dari halaman yang kebetulan dibuka.
//
// Harga lama tidak dihapus lebih dulu: kalau sumbernya masih mati, yang
// terakhir diketahui tetap lebih berguna daripada kolom kosong.
func (s *hargaSource) segarkan(ctx context.Context, symbols []string) {
	if !s.enabled() {
		return
	}
	s.mu.Lock()
	s.diam = time.Time{}
	s.mu.Unlock()

	var bersih []string
	for _, sym := range symbols {
		if sym != "" {
			bersih = append(bersih, sym)
		}
	}
	s.jemput(ctx, bersih)
}

// simpan menyimpan hasil pengambilan. Kegagalan tidak menghapus harga lama:
// yang terakhir diketahui lebih berguna daripada kolom kosong, selama umurnya
// ikut ditampilkan.
func (s *hargaSource) simpan(symbol string, k Kuotasi, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.lastErr = err
		log.Printf("gagal mengambil harga %s: %v", symbol, err)
		return
	}
	s.cache[symbol] = k
	s.ambil[symbol] = time.Now()
	s.lastErr = nil
}

func (s *hargaSource) status() error {
	if !s.enabled() {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastErr
}

// ---------- pencarian simbol ----------

// Cocok: satu hasil pencarian simbol, dalam bentuk yang sama untuk saham dan
// reksadana meski sumbernya berbeda.
type Cocok struct {
	Simbol string `json:"simbol"`
	Nama   string `json:"nama"`
	Info   string `json:"info"`
}

type yahooSearch struct {
	Quotes []struct {
		Symbol    string `json:"symbol"`
		ShortName string `json:"shortname"`
		LongName  string `json:"longname"`
		QuoteType string `json:"quoteType"`
		Exchange  string `json:"exchange"`
	} `json:"quotes"`
}

// cariSimbol mencari saham dan ETF dari potongan kode atau nama. Yang dicari
// bisa "VOO" maupun "vanguard s&p" — orang mengingat salah satu dari keduanya,
// dan memaksa mengetik kode persis membuat fitur ini cuma berguna bagi yang
// sudah hafal.
func (s *hargaSource) cariSimbol(ctx context.Context, q string, batas int) ([]Cocok, error) {
	if !s.enabled() || strings.TrimSpace(q) == "" {
		return nil, nil
	}
	// Endpoint pencariannya bersaudara dengan endpoint chart, satu tingkat di
	// atas path versinya.
	u := strings.TrimSuffix(s.base, "v8/finance/chart/") + "v1/finance/search" +
		"?newsCount=0&quotesCount=" + strconv.Itoa(batas) + "&q=" + url.QueryEscape(q)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Rukun/1.0 (+https://github.com/firsta/rukun)")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cari %q: status %d", q, resp.StatusCode)
	}

	var body yahooSearch
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	out := make([]Cocok, 0, len(body.Quotes))
	for _, q := range body.Quotes {
		// Kripto dan indeks ikut terbawa hasil pencarian Yahoo. Fase ini hanya
		// menjanjikan saham dan ETF, jadi sisanya tidak ditawarkan — pilihan
		// yang tidak bisa dipakai lebih buruk daripada daftar yang lebih pendek.
		if q.QuoteType != "EQUITY" && q.QuoteType != "ETF" {
			continue
		}
		nama := q.ShortName
		if nama == "" {
			nama = q.LongName
		}
		if q.Symbol == "" || nama == "" {
			continue
		}
		out = append(out, Cocok{Simbol: q.Symbol, Nama: strings.Join(strings.Fields(nama), " "),
			Info: q.QuoteType + " · " + q.Exchange})
	}
	return out, nil
}

// namaSimbol mencari nama resmi sebuah kode bursa.
//
// Dipakai saat menyimpan posisi, supaya namanya tidak bergantung pada balasan
// pencarian yang kebetulan sudah sampai di browser. Tiga hasil yang berbeda
// artinya, dan pemanggilnya perlu membedakan ketiganya: ketemu, tidak dikenal
// (kode salah ketik — posisinya tidak akan pernah dapat harga), dan sumbernya
// sedang tidak bisa dihubungi (bukan salah orangnya, jadi jangan dihalangi).
func (s *hargaSource) namaSimbol(ctx context.Context, symbol string) (nama string, dikenal bool, err error) {
	if !s.enabled() || symbol == "" {
		return "", false, nil
	}
	s.mu.RLock()
	k, ada := s.cache[symbol]
	s.mu.RUnlock()
	if ada && k.Nama != "" {
		return k.Nama, true, nil
	}

	tunggu, batal := context.WithTimeout(ctx, hargaBudget)
	defer batal()
	k, err = s.fetch(tunggu, symbol)
	if err != nil {
		// Simbol yang tidak dikenal dijawab endpoint-nya sebagai galat isi,
		// bukan galat jaringan.
		if strings.Contains(err.Error(), "No data found") || strings.Contains(err.Error(), "delisted") {
			return "", false, nil
		}
		return "", false, err
	}
	s.simpan(symbol, k, nil)
	return k.Nama, true, nil
}
