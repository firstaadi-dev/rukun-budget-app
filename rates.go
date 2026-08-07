package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"sync"
	"time"
)

// Kurs pasar diambil dari open.er-api.com: gratis, tanpa API key, diperbarui
// sekali sehari. Yang diminta selalu base USD, tidak pernah base IDR — dengan
// base IDR endpoint itu membulatkan ke enam desimal sehingga 1 IDR = 0,000056
// USD, cuma dua digit signifikan dan meleset belasan persen. Dari base USD
// (1 USD = 17936,304774 IDR) semua pasangan lain diturunkan lewat USD.
const defaultRatesURL = "https://open.er-api.com/v6/latest/USD"

// rateScale: berapa unit mayor mata uang sumber yang dipakai sebagai penyebut
// saat mengubah kurs pecahan jadi rasio bilangan bulat. Makin besar makin
// presisi; 1e9 menyimpan sekitar tujuh digit signifikan, cukup untuk enam
// desimal yang dikirim API.
const rateScale = 1_000_000_000

type rateSource struct {
	url    string
	client *http.Client

	mu      sync.RWMutex
	perUSD  map[string]float64 // 1 USD = sekian unit mata uang ini
	fetched time.Time
	lastErr error
}

func newRateSource(url string) *rateSource {
	return &rateSource{
		url:    url,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (s *rateSource) enabled() bool { return s != nil && s.url != "" }

type erAPIResponse struct {
	Result     string             `json:"result"`
	BaseCode   string             `json:"base_code"`
	LastUpdate string             `json:"time_last_update_utc"`
	Rates      map[string]float64 `json:"rates"`
	ErrorType  string             `json:"error-type"`
}

func (s *rateSource) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("kurs: status %d", resp.StatusCode)
	}

	var body erAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return err
	}
	if body.Result != "success" {
		return fmt.Errorf("kurs: %s", body.ErrorType)
	}
	if body.BaseCode != "USD" || body.Rates["USD"] != 1 {
		return fmt.Errorf("kurs: base tak terduga %q", body.BaseCode)
	}

	s.mu.Lock()
	s.perUSD, s.fetched, s.lastErr = body.Rates, time.Now(), nil
	s.mu.Unlock()
	log.Printf("kurs diperbarui: %d mata uang, per %s", len(body.Rates), body.LastUpdate)
	return nil
}

// keep memperbarui kurs di latar belakang. Kegagalan hanya dicatat: aplikasi
// tetap jalan dengan kurs transfer terakhir sebagai cadangan.
func (s *rateSource) keep(ctx context.Context) {
	for {
		if err := s.refresh(ctx); err != nil {
			s.mu.Lock()
			s.lastErr = err
			s.mu.Unlock()
			log.Printf("gagal mengambil kurs: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(6 * time.Hour):
		}
	}
}

// pairs membangun kurs untuk setiap pasangan mata uang yang dipakai aplikasi,
// dalam bentuk yang sama dengan kurs hasil transfer: rasio dua nominal int64.
func (s *rateSource) pairs(codes []string) map[string]Rate {
	s.mu.RLock()
	perUSD, fetched := s.perUSD, s.fetched
	s.mu.RUnlock()
	if len(perUSD) == 0 || fetched.IsZero() {
		return nil
	}

	out := make(map[string]Rate, len(codes)*(len(codes)-1))
	for _, from := range codes {
		a, ok := perUSD[from]
		if !ok || a <= 0 {
			continue
		}
		for _, to := range codes {
			b, ok := perUSD[to]
			if from == to || !ok || b <= 0 {
				continue
			}
			if r, ok := rateFromPrice(from, to, b/a); ok {
				out[from+">"+to] = r
			}
		}
	}
	return out
}

// rateFromPrice mengubah kurs pecahan (berapa unit `to` untuk 1 unit `from`,
// keduanya satuan mayor) jadi Rate berupa rasio dua nominal minor.
func rateFromPrice(from, to string, price float64) (Rate, bool) {
	if !(price > 0) || math.IsInf(price, 0) {
		return Rate{}, false
	}
	fromMinor := pow10(Exp(from)) * rateScale
	toMinor := math.Round(price * rateScale * float64(pow10(Exp(to))))
	// Jaga-jaga untuk pasangan mata uang ekstrem: di luar rentang int64 atau
	// membulat jadi nol, kurs itu dilewati saja daripada menyimpan angka salah.
	if toMinor < 1 || toMinor > math.MaxInt64/2 {
		return Rate{}, false
	}
	return Rate{FromMinor: fromMinor, From: from, ToMinor: int64(toMinor), To: to}, true
}

// status merangkum kondisi sumber kurs untuk ditampilkan ke user.
func (s *rateSource) status() (fetched time.Time, err error) {
	if !s.enabled() {
		return time.Time{}, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.fetched, s.lastErr
}

// rates menggabungkan kurs pasar dengan kurs dari transfer yang sudah tercatat.
// Kurs pasar menang karena lebih baru; kurs transfer jadi cadangan untuk
// pasangan mata uang yang tidak dikenal API atau saat API sedang tidak bisa
// dihubungi.
func (a *App) rates(ctx context.Context) (map[string]Rate, error) {
	merged, err := a.store.Rates(ctx)
	if err != nil {
		return nil, err
	}
	if merged == nil {
		merged = map[string]Rate{}
	}
	if a.rateSrc.enabled() {
		codes := make([]string, 0, len(Currencies))
		for _, c := range Currencies {
			codes = append(codes, c.Code)
		}
		for k, v := range a.rateSrc.pairs(codes) {
			merged[k] = v
		}
	}
	return merged, nil
}
