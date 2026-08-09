package main

import (
	"context"
	"encoding/json"
	"fmt"
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
	base   string
	client *http.Client

	mu    sync.RWMutex
	cache map[string]Kuotasi
	// ambil: waktu pengambilan terakhir per simbol, dipakai memutuskan perlu
	// tidaknya menyegarkan. Terpisah dari Kuotasi.At, yang waktu bursanya:
	// akhir pekan membuat At diam tiga hari sementara pengambilannya tetap
	// berjalan, dan menyamakan keduanya akan membuat simbol itu diambil ulang
	// terus-menerus.
	ambil   map[string]time.Time
	lastErr error
	// jalan: simbol yang sedang diambil di latar belakang, supaya satu halaman
	// yang dibuka berkali-kali tidak menumpuk permintaan ke sumber yang sama.
	jalan map[string]bool
}

func newHargaSource(base string) *hargaSource {
	return &hargaSource{
		base:   base,
		client: &http.Client{Timeout: 10 * time.Second},
		cache:  map[string]Kuotasi{},
		ambil:  map[string]time.Time{},
		jalan:  map[string]bool{},
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

func (s *hargaSource) fetch(ctx context.Context, symbol string) (Kuotasi, error) {
	u := s.base + url.PathEscape(symbol) + "?range=5d&interval=1d"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return Kuotasi{}, err
	}
	// Tanpa User-Agent, endpoint ini kadang membalas 429 sekalipun permintaannya
	// jarang.
	req.Header.Set("User-Agent", "Rukun/1.0 (+https://github.com/firsta/rukun)")

	resp, err := s.client.Do(req)
	if err != nil {
		return Kuotasi{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Kuotasi{}, fmt.Errorf("harga %s: status %d", symbol, resp.StatusCode)
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

	for _, sym := range basi {
		s.latar(sym)
	}
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

// latar menyegarkan satu simbol tanpa menahan permintaan yang sedang berjalan.
// Konteksnya sengaja bukan konteks permintaan itu: halaman yang sudah selesai
// dikirim akan membatalkannya di tengah jalan.
func (s *hargaSource) latar(symbol string) {
	s.mu.Lock()
	if s.jalan[symbol] {
		s.mu.Unlock()
		return
	}
	s.jalan[symbol] = true
	s.mu.Unlock()

	go func() {
		ctx, batal := context.WithTimeout(context.Background(), 15*time.Second)
		defer batal()
		k, err := s.fetch(ctx, symbol)
		s.simpan(symbol, k, err)

		s.mu.Lock()
		delete(s.jalan, symbol)
		s.mu.Unlock()
	}()
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
