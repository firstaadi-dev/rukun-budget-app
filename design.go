package main

import (
	"fmt"
	"html/template"
	"strings"
	"time"
	"unicode"
)

func initials(s string) string {
	var out []rune
	for _, w := range strings.Fields(s) {
		out = append(out, unicode.ToUpper([]rune(w)[0]))
		if len(out) == 2 {
			break
		}
	}
	return string(out)
}

// Icons are trusted, local SVG paths; category names never become markup.
func designIcon(name string) template.HTML {
	paths := map[string]string{
		"contactless": `<path d="M7 9a5 5 0 0 1 0 6m4-9a9 9 0 0 1 0 12m4-15a13 13 0 0 1 0 18"/>`,
		"balance":     `<path d="M12 3v18M4 21h16M3 6h18M6 6l-4 8h8L6 6m12 0-4 8h8l-4-8"/>`,
		"send":        `<path d="m3 3 19 9-19 9 4-9-4-9m4 9h15"/>`,
		"bell":        `<path d="M18 8a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9M10 21h4"/>`,
		"check":       `<circle cx="12" cy="12" r="9"/><path d="m8 12 3 3 5-6"/>`,
		"calendar":    `<rect x="3" y="5" width="18" height="16" rx="2"/><path d="M7 3v4m10-4v4M3 11h18"/>`,
		"search":      `<circle cx="10" cy="10" r="6"/><path d="m15 15 6 6"/>`,
		"mic":         `<rect x="9" y="2" width="6" height="12" rx="3"/><path d="M5 10v2a7 7 0 0 0 14 0v-2M12 19v3m-4 0h8"/>`,
		"info":        `<circle cx="12" cy="12" r="9"/><path d="M12 11v6m0-10h.01"/>`,
		"pie":         `<circle cx="12" cy="12" r="9"/><path d="M12 3v9h9M12 12l-6 7"/>`,
		"cart":        `<path d="M2 3h3l3 12h11l3-8H6M9 20h.01M18 20h.01"/>`,
		"coffee":      `<path d="M4 7h13v7a6 6 0 0 1-12 0V7m12 0h2a3 3 0 0 1 0 6h-2M3 21h16M7 3v1m5-1v1"/>`,
		"food":        `<path d="M5 2v8m-3-8v5a3 3 0 0 0 6 0V2M5 10v12M16 2c-4 5-4 10 0 10h2V2h-2m2 10v10"/>`,
		"car":         `<path d="m4 10 2-6h12l2 6M3 10h18v9H3V10m3 9v3m12-3v3M6 14h2m8 0h2"/>`,
		"bolt":        `<path d="m13 2-9 12h7l-1 8 10-13h-7l0-7"/>`,
		"school":      `<path d="m2 8 10-5 10 5-10 5L2 8m4 2v7l6 4 6-4v-7M22 8v9"/>`,
		"health":      `<path d="m12 2 9 4v6c0 5-9 10-9 10S3 17 3 12V6l9-4m0 5v9m-4-5h8"/>`,
		"movie":       `<rect x="3" y="8" width="18" height="13" rx="2"/><path d="M3 8V3h18v5M7 3l3 5m4-5 3 5"/>`,
		"receipt":     `<path d="M5 2v20l3-2 4 2 4-2 3 2V2l-3 2-4-2-4 2-3-2m4 6h6m-6 4h6m-6 4h4"/>`,
		"wallet":      `<rect x="3" y="5" width="18" height="16" rx="2"/><path d="M3 5V3h15m-2 9h6v5h-6v-5"/>`,
		"bank":        `<path d="m2 8 10-6 10 6H2m2 3v8m5-8v8m6-8v8m5-8v8M2 22h20"/>`,
		"cash":        `<rect x="2" y="5" width="20" height="14" rx="2"/><circle cx="12" cy="12" r="3"/><path d="M5 8h1m12 8h1"/>`,
		"ewallet":     `<rect x="6" y="2" width="12" height="20" rx="2"/><path d="M10 18h4"/>`,
		"credit":      `<rect x="2" y="4" width="20" height="16" rx="2"/><path d="M2 9h20M5 16h4"/>`,
		"invest":      `<path d="m2 17 6-7 5 4 8-9m-7 0h7v7"/>`,
		"gold":        `<path d="m7 5-4 14h18L17 5H7m1 5h8"/>`,
		"heart":       `<path d="M20 4a5 5 0 0 0-8 2 5 5 0 0 0-8-2c-5 5 8 17 8 17S25 9 20 4"/>`,
		"camera":      `<path d="M3 7h4l2-4h6l2 4h4v14H3V7"/><circle cx="12" cy="13" r="4"/>`,
		"share":       `<circle cx="18" cy="4" r="3"/><circle cx="6" cy="12" r="3"/><circle cx="18" cy="20" r="3"/><path d="m9 10 6-4m-6 8 6 4"/>`,
		"dots":        `<circle cx="12" cy="4" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="12" cy="20" r="1"/>`,
		"download":    `<path d="M12 2v13m-5-5 5 5 5-5M4 17v5h16v-5"/>`,
		"handshake":   `<path d="m2 10 5-7 5 2 5-2 5 7-6 9-4 2-7-5-3-6m5-7 5 5-3 3 2 2 4-3 5 5M7 13l5 6"/>`,
		"minus":       `<path d="M5 12h14"/>`,
		"plus":        `<path d="M12 5v14M5 12h14"/>`,
		"transfer":    `<path d="M3 8h17m-4-4 4 4-4 4M21 16H4m4-4-4 4 4 4"/>`,
		"leaf":        `<path d="M5 20C2 7 13 3 21 3c0 10-3 18-13 17m-4 1L16 8"/>`,
		"plant":       `<path d="M7 13h10l-2 8H9l-2-8m5 0V7c-5 1-6-4-6-4 6 0 6 4 6 4m0 4c1-6 6-6 6-6s0 6-6 6"/>`,
		"history":     `<path d="M3 3v6h6M3 9a9 9 0 1 1 0 8M12 6v6l4 2"/>`,
		"settings":    `<path d="M4 6h16M4 12h16M4 18h16"/><circle cx="8" cy="6" r="2"/><circle cx="16" cy="12" r="2"/><circle cx="9" cy="18" r="2"/>`,
		"cloud":       `<path d="M6 18a5 5 0 0 1 0-10 7 7 0 0 1 13-1 5 5 0 0 1 0 11H6"/>`,
		"router":      `<rect x="3" y="13" width="18" height="8" rx="2"/><path d="M16 13V7M10 6a8 8 0 0 1 12 0M13 9a4 4 0 0 1 6 0M6 17h.01m4 0h.01"/>`,
		"music":       `<path d="M9 18V5l12-3v13M9 8l12-3"/><circle cx="6" cy="18" r="3"/><circle cx="18" cy="15" r="3"/>`,
	}
	p, ok := paths[name]
	if !ok {
		p = paths["receipt"]
	}
	return template.HTML(`<svg class="ic" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">` + p + `</svg>`)
}
func categoryIcon(s string) string {
	s = strings.ToLower(s)
	for _, v := range []struct{ k, i string }{{"belanja", "cart"}, {"dapur", "cart"}, {"makan", "food"}, {"jajan", "food"}, {"kopi", "coffee"}, {"transport", "car"}, {"bensin", "car"}, {"tagihan", "bolt"}, {"listrik", "bolt"}, {"pendidikan", "school"}, {"sekolah", "school"}, {"kesehatan", "health"}, {"hiburan", "movie"}, {"gaji", "cash"}, {"bonus", "cash"}, {"emas", "gold"}, {"invest", "invest"}} {
		if strings.Contains(s, v.k) {
			return v.i
		}
	}
	return "receipt"
}
func serviceIcon(s string) string {
	s = strings.ToLower(s)
	if strings.Contains(s, "spotify") {
		return "music"
	}
	if strings.Contains(s, "internet") || strings.Contains(s, "indihome") {
		return "router"
	}
	if strings.Contains(s, "cloud") || strings.Contains(s, "google") {
		return "cloud"
	}
	return "movie"
}
func dateOnly(t time.Time) string        { return tanggalPendek(t) }
func daysUntil(t, timeNow time.Time) int { return int(hari(t).Sub(hari(timeNow)).Hours() / 24) }
func percentLabel(n, limit int64) string {
	if limit <= 0 {
		return "0"
	}
	return fmt.Sprintf("%.1f", float64(n)*100/float64(limit))
}

func periodDays() []int {
	out := make([]int, 28)
	for i := range out {
		out[i] = i + 1
	}
	return out
}

// compactPeriod keeps cross-year ranges unambiguous inside mobile headings.
func compactPeriod(p Periode) string {
	if p.From.IsZero() || p.To.IsZero() {
		return p.Label
	}
	end := p.To.AddDate(0, 0, -1)
	if p.From.Equal(end) {
		return p.From.Format("2") + " " + bulanSingkat[int(p.From.Month())] + p.From.Format(" 2006")
	}
	startYear := ""
	if p.From.Year() != end.Year() {
		startYear = p.From.Format(" 2006")
	}
	return fmt.Sprintf("%d %s%s – %d %s %d", p.From.Day(), bulanSingkat[int(p.From.Month())], startYear, end.Day(), bulanSingkat[int(end.Month())], end.Year())
}

var bulanSingkat = [...]string{"", "Jan", "Feb", "Mar", "Apr", "Mei", "Jun", "Jul", "Agu", "Sep", "Okt", "Nov", "Des"}
