package main

import (
	"encoding/csv"
	"log"
	"net/http"
	"strconv"
	"strings"
)

func (a *App) report(w http.ResponseWriter, r *http.Request) {
	month := r.URL.Query().Get("periode")
	if month == periodeSemua {
		month = ""
	}
	p := bacaPeriode(month, "", "", a.today())
	spend, err := a.store.CategorySpending(r.Context(), family(r), p.From, p.To)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	rates, err := a.rates(r.Context(), family(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.render(w, r, "laporan.html", map[string]any{
		"Title": "Laporan", "Nav": "laporan", "Period": p,
		"Breakdown": breakdown(spend, rates, a.base, p.Label),
	})
}

// csvText mencegah teks bebas pengguna menjadi rumus saat berkas dibuka di
// spreadsheet. Nominal numerik ditulis terpisah dan tidak melewati fungsi ini.
func csvText(s string) string {
	if trimmed := strings.TrimLeft(s, " \t\r\n"); trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + s
	}
	return s
}

func (a *App) txExport(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	kind := q.Get("jenis")
	if _, ok := kindLabel[kind]; !ok && kind != "hutang" {
		kind = ""
	}
	category := q.Get("kategori")
	if category != "" && (kind == "transfer" || kind == "hutang" || kind == "invest_buy") {
		kind = ""
	}
	walletID, _ := strconv.ParseInt(q.Get("dompet"), 10, 64)
	if walletID < 0 {
		walletID = 0
	}
	p := bacaPeriode(q.Get("periode"), q.Get("dari"), q.Get("sampai"), a.today())
	txs, err := a.store.Transactions(r.Context(), family(r), TxFilter{
		Kinds: kindsForFilter(kind), Category: category, Cari: strings.TrimSpace(q.Get("cari")),
		WalletID: walletID, From: p.From, To: p.To,
	})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="rukun-transaksi.csv"`)
	w.Write([]byte("\xef\xbb\xbf"))
	c := csv.NewWriter(w)
	c.Comma = ';'
	c.Write([]string{"Tanggal", "Jenis", "Kategori", "Dompet", "Nominal", "Mata uang", "Dompet tujuan", "Diterima", "Mata uang tujuan", "Pihak", "Investasi", "Catatan", "Dicatat oleh"})
	for _, t := range txs {
		received := ""
		if t.ToWalletID != 0 {
			received = FormatPlain(t.AmountInMino, t.ToWalletCur)
		}
		if err := c.Write([]string{
			t.Date.Format(formatTanggal), t.Kind, csvText(t.Category), csvText(t.WalletName),
			FormatPlain(t.AmountMinor, t.WalletCur), t.WalletCur, csvText(t.ToWalletName), received,
			t.ToWalletCur, csvText(t.PartyName), csvText(t.InvestmentName), csvText(t.Note), csvText(t.CreatedBy),
		}); err != nil {
			log.Printf("ekspor CSV: %v", err)
			return
		}
	}
	c.Flush()
	if err := c.Error(); err != nil {
		log.Printf("ekspor CSV: %v", err)
	}
}
