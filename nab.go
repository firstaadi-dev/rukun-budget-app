package main

import (
	"context"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// NAB reksadana Indonesia diambil dari Infovesta. Ini bukan API resmi melainkan
// fragmen HTML yang dipakai halaman datanya sendiri, jadi ia bisa berubah
// sewaktu-waktu tanpa pemberitahuan — dan karena itu setiap posisi tetap boleh
// menyimpan NAB yang diisi sendiri, dan kegagalan pengambilan tidak pernah
// mengosongkan layar.
//
// Sumber lain sudah dicoba lebih dulu dan tidak bisa dipakai: dua API komunitas
// yang beredar sudah mati, dan OJK hanya menerbitkan HTML bulanan. Yang ini
// satu-satunya yang menerbitkan NAB harian seluruh reksadana Indonesia dalam
// bentuk yang bisa dibaca mesin.
//
// Pengambilannya sengaja hemat: enam permintaan sehari untuk seluruh
// deployment, bukan satu per halaman yang dibuka. NAB memang cuma berubah
// sekali sehari sesudah bursa tutup.
const defaultNABURL = "https://www.infovesta.com/index/mutualfund/"

// kategoriNAB: enam kategori reksadana di Infovesta. Semuanya diambil karena
// satu keluarga bisa memegang reksadana pasar uang dan saham sekaligus, dan
// menebak kategori dari namanya tidak mungkin.
var kategoriNAB = []struct{ Kode, Label string }{
	{"PU", "Pasar Uang"},
	{"PT", "Pendapatan Tetap"},
	{"CP", "Campuran"},
	{"SH", "Saham"},
	{"ETF", "ETF"},
	{"IDX", "Indeks"},
}

// nabSegar: NAB berubah sekali sehari sesudah bursa tutup. Setengah hari sudah
// lebih dari cukup rapat, dan lebih sering dari itu hanya membebani sumbernya
// tanpa mengubah satu angka pun.
const nabSegar = 12 * time.Hour

// Reksadana: satu baris dari daftar NAB.
type Reksadana struct {
	Nama     string
	Kategori string
	PriceE4  int64
	Currency string
}

type nabSource struct {
	base   string
	client *http.Client

	mu      sync.RWMutex
	daftar  []Reksadana
	indeks  map[string]Reksadana // dikunci nama yang sudah dinormalkan
	fetched time.Time
	lastErr error
}

func newNABSource(base string) *nabSource {
	return &nabSource{
		base:   base,
		client: &http.Client{Timeout: 30 * time.Second},
		indeks: map[string]Reksadana{},
	}
}

func (s *nabSource) enabled() bool { return s != nil && s.base != "" }

// Baris tabelnya dirakit Kendo UI di sisi server, jadi bentuknya tetap: nama
// reksadana di kolom ketiga, NAB/UP di keempat. Diurai dengan regex, bukan
// pengurai HTML penuh, karena yang dibutuhkan cuma dua kolom dari satu tabel
// yang bentuknya sudah diketahui — dan pengurai yang lebih pintar tetap akan
// patah kalau tabelnya berubah.
var (
	reBaris = regexp.MustCompile(`(?s)<tr[^>]*>(.*?)</tr>`)
	reSel   = regexp.MustCompile(`(?s)<td[^>]*>(.*?)</td>`)
	reTag   = regexp.MustCompile(`<[^>]+>`)
)

func (s *nabSource) fetchKategori(ctx context.Context, kode string) ([]Reksadana, error) {
	// Akhiran /2 adalah nomor tabel yang dipakai halaman datanya; tanpa itu
	// endpoint-nya membalas 404.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+kode+"/2", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Rukun/1.0 (pencatat keuangan keluarga; +https://github.com/firsta/rukun)")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("NAB %s: status %d", kode, resp.StatusCode)
	}
	body, err := bacaBatas(resp.Body, 4<<20)
	if err != nil {
		return nil, err
	}

	var out []Reksadana
	for _, m := range reBaris.FindAllStringSubmatch(body, -1) {
		sel := reSel.FindAllStringSubmatch(m[1], -1)
		if len(sel) < 4 {
			continue
		}
		nama := bersih(sel[2][1])
		nab, err := strconv.ParseFloat(strings.TrimSpace(bersih(sel[3][1])), 64)
		if nama == "" || err != nil || nab <= 0 {
			continue
		}
		// Mata uangnya tidak ada di tabel. Yang berdenominasi dolar menyebutnya
		// di nama, dan itu satu-satunya petunjuk yang tersedia; salah tebak di
		// sini tertahan lapis berikutnya, yang menolak kuotasi bermata uang
		// beda dari posisinya.
		cur := "IDR"
		if strings.HasSuffix(strings.ToUpper(nama), " USD") {
			cur = "USD"
		}
		e4, ok := priceToE4(nab, cur)
		if !ok {
			continue
		}
		out = append(out, Reksadana{Nama: nama, Kategori: kode, PriceE4: e4, Currency: cur})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("NAB %s: tidak ada baris terbaca", kode)
	}
	return out, nil
}

func bersih(s string) string {
	return strings.TrimSpace(html.UnescapeString(reTag.ReplaceAllString(s, "")))
}

// bacaBatas membaca badan respons dengan batas atas. Halaman yang dibaca ini
// bukan API dengan kontrak yang dijanjikan siapa pun, dan membaca sebanyak
// apa pun yang dikirimnya ke dalam memori adalah kepercayaan yang tidak perlu
// diberikan. Satu kategori terbesar sekitar 200 KB; empat megabyte sudah
// jauh di atasnya.
func bacaBatas(r io.Reader, max int64) (string, error) {
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return "", err
	}
	if int64(len(b)) > max {
		return "", fmt.Errorf("balasan lebih besar dari %d byte", max)
	}
	return string(b), nil
}

// refresh mengambil keenam kategori. Kategori yang gagal dilewati, bukan
// membatalkan semuanya: reksadana pasar uang yang berhasil tetap berguna
// walau kategori saham sedang bermasalah.
func (s *nabSource) refresh(ctx context.Context) error {
	var semua []Reksadana
	var gagal []string
	for _, k := range kategoriNAB {
		rows, err := s.fetchKategori(ctx, k.Kode)
		if err != nil {
			gagal = append(gagal, k.Kode)
			log.Printf("gagal mengambil NAB %s: %v", k.Kode, err)
			continue
		}
		semua = append(semua, rows...)
	}
	if len(semua) == 0 {
		err := fmt.Errorf("NAB: seluruh kategori gagal")
		s.mu.Lock()
		s.lastErr = err
		s.mu.Unlock()
		return err
	}

	indeks := make(map[string]Reksadana, len(semua))
	for _, r := range semua {
		indeks[kunciNama(r.Nama)] = r
	}
	sort.Slice(semua, func(i, j int) bool { return semua[i].Nama < semua[j].Nama })

	s.mu.Lock()
	s.daftar, s.indeks, s.fetched = semua, indeks, time.Now()
	s.lastErr = nil
	if len(gagal) > 0 {
		s.lastErr = fmt.Errorf("NAB: kategori %s gagal", strings.Join(gagal, ", "))
	}
	s.mu.Unlock()
	log.Printf("NAB diperbarui: %d reksadana", len(semua))
	return nil
}

// keep menyegarkan NAB di latar belakang. Sama seperti kurs, kegagalan hanya
// dicatat: aplikasi tetap jalan dengan NAB terakhir, atau dengan NAB yang
// diisi sendiri.
func (s *nabSource) keep(ctx context.Context) {
	for {
		if err := s.refresh(ctx); err != nil {
			log.Printf("gagal memperbarui NAB: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(nabSegar):
		}
	}
}

// kunciNama menormalkan nama reksadana untuk pencocokan: huruf kecil, dan
// setiap deret spasi jadi satu. Nama di Infovesta ditulis manusia dan sesekali
// berubah spasinya; mencocokkan mentah-mentah membuat posisi kehilangan
// harganya karena satu spasi ganda.
func kunciNama(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }

// nab mencari NAB satu reksadana dari namanya.
func (s *nabSource) nab(nama string) (Kuotasi, bool) {
	if !s.enabled() || nama == "" {
		return Kuotasi{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.indeks[kunciNama(nama)]
	if !ok {
		return Kuotasi{}, false
	}
	// Tanggal NAB tidak ikut diterbitkan di tabelnya, jadi yang dicatat waktu
	// pengambilan. Itu memang bukan hal yang sama, dan Diambil yang membuat
	// labelnya di layar mengatakan "diambil", bukan "per".
	return Kuotasi{PriceE4: r.PriceE4, Currency: r.Currency, At: s.fetched, Diambil: true}, true
}

// cari mencari reksadana yang namanya memuat potongan yang diketik. Dipakai
// isian otomatis di form; hasilnya dijepit supaya daftar pilihannya tetap bisa
// dibaca sekali lihat.
func (s *nabSource) cari(q string, batas int) []Reksadana {
	if !s.enabled() {
		return nil
	}
	q = kunciNama(q)
	if q == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	var awalan, memuat []Reksadana
	for _, r := range s.daftar {
		k := kunciNama(r.Nama)
		switch {
		case strings.HasPrefix(k, q):
			awalan = append(awalan, r)
		case strings.Contains(k, q):
			memuat = append(memuat, r)
		}
		if len(awalan) >= batas {
			break
		}
	}
	// Yang namanya diawali ketikan didahulukan: mengetik "suco" hampir selalu
	// berarti mencari yang namanya dimulai begitu.
	out := append(awalan, memuat...)
	if len(out) > batas {
		out = out[:batas]
	}
	return out
}

func (s *nabSource) status() (time.Time, error) {
	if !s.enabled() {
		return time.Time{}, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.fetched, s.lastErr
}
