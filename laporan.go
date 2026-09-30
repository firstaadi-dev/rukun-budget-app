package main

import (
	"encoding/csv"
	"fmt"
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
	f, err := a.store.FamilyByID(r.Context(), family(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	p := bacaPeriodeKustom(month, "", "", a.today(), f.PeriodStartDay)
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
	current := breakdown(spend, rates, a.base, p.Label)
	prevFrom, prevTo := p.From.AddDate(0, -1, 0), p.To.AddDate(0, -1, 0)
	prevLabel := bacaPeriodeKustom(prevFrom.Format(formatPeriode), "", "", a.today(), f.PeriodStartDay).Label
	prevSpend, err := a.store.CategorySpending(r.Context(), family(r), prevFrom, prevTo)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	prev := breakdown(prevSpend, rates, a.base, "")
	count, err := a.store.ReportTransactionCount(r.Context(), family(r), p.From, p.To)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	cats, err := a.store.Categories(r.Context(), family(r), "expense")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	end := p.To
	if todayEnd := a.today().AddDate(0, 0, 1); end.After(todayEnd) {
		end = todayEnd
	}
	days := int(hari(end).Sub(hari(p.From)).Hours() / 24)
	if days < 1 {
		days = 1
	}
	avg := (current.TotalOutMinor + int64(days)/2) / int64(days)
	change := "Sama dengan " + prevLabel
	if prev.TotalOutMinor == 0 && current.TotalOutMinor > 0 {
		change = "Ada pengeluaran baru dibanding " + prevLabel
	} else if prev.TotalOutMinor > 0 {
		pct := float64(current.TotalOutMinor-prev.TotalOutMinor) * 100 / float64(prev.TotalOutMinor)
		if pct > 0 {
			change = fmt.Sprintf("Naik %.1f%% dibanding %s", pct, prevLabel)
		} else if pct < 0 {
			change = fmt.Sprintf("Turun %.1f%% dibanding %s", -pct, prevLabel)
		}
	}
	savingRate := "—"
	if current.TotalInMinor > 0 {
		savingRate = fmt.Sprintf("%.1f%%", float64(current.TotalInMinor-current.TotalOutMinor)*100/float64(current.TotalInMinor))
	}
	a.render(w, r, "laporan.html", map[string]any{
		"Title": "Laporan Finansial", "Nav": "laporan", "Period": p,
		"Breakdown": current, "TransactionCount": count, "AvgDaily": Format(avg, a.base),
		"SavingRate": savingRate, "ExpenseChange": change,
		"Budgets": budgetRows(cats, spend, rates, a.base), "Base": a.base,
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
	recorderID, _ := strconv.ParseInt(q.Get("pencatat"), 10, 64)
	recorderID = max(0, recorderID)
	walletID, _ := strconv.ParseInt(q.Get("dompet"), 10, 64)
	if walletID < 0 {
		walletID = 0
	}
	f, err := a.store.FamilyByID(r.Context(), family(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	p := bacaPeriodeKustom(q.Get("periode"), q.Get("dari"), q.Get("sampai"), a.today(), f.PeriodStartDay)
	txs, err := a.store.Transactions(r.Context(), family(r), TxFilter{
		Kinds: kindsForFilter(kind), Category: category, Cari: strings.TrimSpace(q.Get("cari")),
		WalletID: walletID, RecorderID: recorderID, From: p.From, To: p.To,
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
