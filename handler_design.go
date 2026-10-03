package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type budgetOverview struct {
	Limit, Used, Remaining, Percent, Label string
	Over                                   bool
	Days                                   int
}
type weeklyFlow struct {
	Label, In, Out    string
	InWidth, OutWidth int
}
type partnerActivity struct {
	Name         string
	Count, Share int
}
type periodMetrics struct {
	In, Out, Net, Transfer            string
	SpendingRatio, PaceLabel, NetTone string
	HasIncome                         bool
	InCount, OutCount                 int
	Weeks                             []weeklyFlow
	Partners                          []partnerActivity
	Daily                             map[string]string
}
type savingsGoal struct{ Name, Target, TargetPlain, Date, Collected, Percent string }
type debtCard struct {
	PartyView
	Original, Remaining, Last, Due, Direction, Action string
	PaidPercent                                       int
	Overpaid                                          bool
}

func (a *App) designData(r *http.Request, page string, d map[string]any) error {
	if a.store == nil || d["NoChrome"] == true || family(r) == 0 {
		return nil
	}
	ctx := r.Context()
	id := family(r)
	members, err := a.store.Members(ctx, id)
	if err != nil {
		return err
	}
	d["FamilyMembers"] = members
	var revision int64
	var savingTarget int
	if err := a.store.db.QueryRow(ctx, `SELECT ledger_revision,saving_target FROM families WHERE id=$1`, id).Scan(&revision, &savingTarget); err != nil {
		return err
	}
	d["Revision"] = revision
	d["SavingTarget"] = savingTarget
	if breakdown, ok := d["Breakdown"].(CategoryBreakdown); ok && breakdown.TotalInMinor > 0 {
		d["SavingTargetReached"] = float64(breakdown.TotalInMinor-breakdown.TotalOutMinor)*100/float64(breakdown.TotalInMinor) >= float64(savingTarget)
	}
	d["InSheet"] = r.URL.Query().Get("sheet") == "1"
	switch page {
	case "subscription.html":
		d["HeaderBack"] = "/lainnya"
	case "hutang.html", "investasi.html":
		d["HeaderBack"] = "/lainnya"
		d["NoDock"] = true
	case "laporan.html":
		d["HeaderBack"] = "/lainnya"
	}
	d["Now"] = time.Now().In(a.loc)
	d["Today"] = a.today()
	d["Base"] = a.base
	f, err := a.store.FamilyByID(ctx, id)
	if err != nil {
		return err
	}
	p := bacaPeriodeKustom(r.URL.Query().Get("periode"), "", "", a.today(), f.PeriodStartDay)
	if v, ok := d["Periode"].(Periode); ok {
		p = v
	}
	if v, ok := d["Period"].(Periode); ok {
		p = v
	}
	if v, ok := d["Tx"].(TxView); ok {
		p = bacaPeriodeKustom(v.Date.Format(formatPeriode), "", "", v.Date, f.PeriodStartDay)
	}
	d["CurrentPeriod"] = p
	if page == "transaksi.html" {
		current := bacaPeriodeKustom("", "", "", a.today(), f.PeriodStartDay)
		d["PeriodStartDay"] = f.PeriodStartDay
		d["PickerCycle"] = current
		d["PickerPreviousCycle"] = bacaPeriodeKustom(current.Prev, "", "", a.today(), f.PeriodStartDay)
		d["PickerFrom"], d["PickerTo"] = p.Dari, p.Sampai
		if p.Bulan {
			d["PickerFrom"], d["PickerTo"] = p.From.Format(formatTanggal), p.To.AddDate(0, 0, -1).Format(formatTanggal)
		}
		d["PeriodCompact"] = compactPeriod(p)
		if !p.From.IsZero() && !p.To.IsZero() {
			days := daysUntil(p.To, p.From)
			d["CycleDays"] = days
			d["CycleDay"] = max(0, min(days, daysUntil(a.today(), p.From)+1))
			d["CycleRemaining"] = max(0, min(days, daysUntil(p.To, a.today())-1))
			d["CycleFuture"] = hari(a.today()).Before(hari(p.From))
			d["CycleEnded"] = !hari(a.today()).Before(hari(p.To))
		}
	}
	switch page {
	case "dashboard.html", "anggaran.html", "detail.html", "transaksi_form.html":
		cats, err := a.store.Categories(ctx, id, "expense")
		if err != nil {
			return err
		}
		spend, err := a.store.CategorySpending(ctx, id, p.From, p.To)
		if err != nil {
			return err
		}
		rates, err := a.rates(ctx, id)
		if err != nil {
			return err
		}
		var limit, used int64
		for _, c := range cats {
			limit += c.BudgetMinor
		}
		for _, v := range spend {
			if v.Kind == "expense" {
				if n, ok := categoryAmount(v, rates, a.base); ok {
					used += n
				}
			}
		}
		d["BudgetOverview"] = budgetOverview{Format(limit, a.base), Format(used, a.base), Format(max(0, limit-used), a.base), percentLabel(used, limit), p.Label, limit > 0 && used > limit, max(0, daysUntil(p.To, a.today()))}
		rows := budgetRows(cats, spend, rates, a.base)
		d["BudgetRows"] = rows
		d["BudgetJSON"] = jsonAttr(rows)
	}
	switch page {
	case "dashboard.html", "transaksi.html", "laporan.html", "hutang.html", "anggaran.html":
		txs, err := a.store.Transactions(ctx, id, TxFilter{From: p.From, To: p.To})
		if err != nil {
			return err
		}
		rates, err := a.rates(ctx, id)
		if err != nil {
			return err
		}
		d["Metrics"] = a.periodMetrics(txs, p, rates)
		if page == "transaksi.html" {
			filter := TxFilter{From: p.From, To: p.To, Kinds: kindsForFilter(r.URL.Query().Get("jenis")), Category: r.URL.Query().Get("kategori"), Cari: strings.TrimSpace(r.URL.Query().Get("cari"))}
			filter.WalletID, _ = strconv.ParseInt(r.URL.Query().Get("dompet"), 10, 64)
			filter.RecorderID, _ = strconv.ParseInt(r.URL.Query().Get("pencatat"), 10, 64)
			filter.RecorderID = max(0, filter.RecorderID)
			filtered, err := a.store.Transactions(ctx, id, filter)
			if err != nil {
				return err
			}
			d["DailyTotals"] = a.periodMetrics(filtered, p, rates).Daily
		}
	}
	switch page {
	case "dompet.html", "dashboard.html", "detail.html":
		ws, err := a.store.Wallets(ctx, id)
		if err != nil {
			return err
		}
		rates, err := a.rates(ctx, id)
		if err != nil {
			return err
		}
		d["WalletSummary"] = summarize(ws, rates, a.base)
		equivalents := map[int64]string{}
		rateLabels := map[int64]string{}
		balances := map[int64]string{}
		var debit []WalletView
		for _, w := range ws {
			balances[w.ID] = Format(w.BalanceMinor, w.Currency)
			if !w.IsCredit() {
				debit = append(debit, viewWallet(w))
			}
			if w.Currency != a.base {
				if rate, ok := rates[w.Currency+">"+a.base]; ok && rate.Valid() {
					equivalents[w.ID] = Format(rate.Convert(w.BalanceMinor), a.base)
					rateLabels[w.ID] = rate.String()
				}
			}
		}
		d["WalletEquivalents"] = equivalents
		d["WalletRateLabels"] = rateLabels
		d["WalletBalances"] = balances
		d["DebitWallets"] = debit
	}
	if page == "subscription.html" {
		items, _ := d["Items"].([]Subscription)
		rates, err := a.rates(ctx, id)
		if err != nil {
			return err
		}
		var total int64
		active := 0
		missing := map[string]bool{}
		for _, v := range items {
			if !v.Active {
				continue
			}
			active++
			n := v.AmountMinor / int64(max(1, v.IntervalMonths))
			if v.Currency != a.base {
				rate, ok := rates[v.Currency+">"+a.base]
				if !ok || !rate.Valid() {
					missing[v.Currency] = true
					continue
				}
				n = rate.Convert(n)
			}
			total += n
		}
		d["MonthlyTotal"] = Format(total, a.base)
		d["ActiveCount"] = active
		d["PausedCount"] = len(items) - active
		d["SubscriptionMissingRates"] = missing
	}
	if page == "detail.html" {
		v, ok := d["Tx"].(TxView)
		if ok {
			var count int
			var liked bool
			err := a.store.db.QueryRow(ctx, `SELECT count(*),COALESCE(bool_or(user_id=$2),false) FROM transaction_reactions WHERE transaction_id=$1`, v.ID, userFrom(ctx).ID).Scan(&count, &liked)
			if err != nil {
				return err
			}
			d["ReactionCount"] = count
			d["Liked"] = liked
			for _, b := range d["BudgetRows"].([]BudgetRow) {
				if b.Name == v.Category {
					d["TxBudget"] = b
				}
			}
		}
	}
	if page == "hutang.html" {
		summary, ok := d["Summary"].(DebtSummary)
		if ok {
			all, err := a.store.Transactions(ctx, id, TxFilter{Kinds: []string{"debt_in", "debt_pay", "loan_out", "loan_in"}})
			if err != nil {
				return err
			}
			cards := viewDebtCards(summary.Parties, all)
			d["DebtCards"] = cards
			counts := map[string]int{"hutang": 0, "piutang": 0, "lunas": 0}
			for _, card := range cards {
				counts[card.Direction]++
			}
			d["DebtCounts"] = counts
		}
	}
	if page == "investasi.html" {
		var name string
		var target int64
		var date pgtype.Date
		err := a.store.db.QueryRow(ctx, `SELECT name,target_minor,target_on FROM savings_goals WHERE family_id=$1`, id).Scan(&name, &target, &date)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil {
			summary := d["Summary"].(InvestSummary)
			n := summary.TotalMinor
			dateText := ""
			if date.Valid {
				dateText = date.Time.Format(formatTanggal)
			}
			d["Goal"] = savingsGoal{name, Format(target, a.base), FormatPlain(target, a.base), dateText, Format(n, a.base), percentLabel(n, target)}
		}
	}
	return nil
}

func (a *App) periodMetrics(txs []Tx, p Periode, rates map[string]Rate) periodMetrics {
	m := periodMetrics{Daily: map[string]string{}}
	var income, expense, transfer int64
	daily := map[string]int64{}
	people := map[string]int{}
	var weekIn, weekOut [5]int64
	partnerTotal := 0
	today := a.today()
	for _, t := range txs {
		if t.IsAdjustment || hari(t.Date).After(hari(today)) {
			continue
		}
		if t.Kind == "income" || t.Kind == "expense" {
			people[t.CreatedBy]++
			partnerTotal++
			if t.Kind == "income" {
				m.InCount++
			} else {
				m.OutCount++
			}
		}
		n := t.AmountMinor
		if t.WalletCur != a.base {
			rate, ok := rates[t.WalletCur+">"+a.base]
			if !ok || !rate.Valid() {
				continue
			}
			n = rate.Convert(n)
		}
		index := 0
		if !p.From.IsZero() {
			index = min(4, max(0, daysUntil(t.Date, p.From)/7))
		}
		switch t.Kind {
		case "income":
			income += n
			weekIn[index] += n
			daily[labelTanggal(t.Date, a.today())] += n
		case "expense":
			expense += n
			weekOut[index] += n
			daily[labelTanggal(t.Date, a.today())] -= n
		case "transfer":
			transfer += n
		}
	}
	m.In = Format(income, a.base)
	m.Out = Format(expense, a.base)
	m.Net = Format(income-expense, a.base)
	m.NetTone = "in"
	if income > expense {
		m.Net = "+" + m.Net
	}
	if income < expense {
		m.NetTone = "out"
	}
	m.HasIncome = income > 0
	m.SpendingRatio = percentLabel(expense, income)
	m.PaceLabel = "Belum Ada Pemasukan"
	if m.HasIncome {
		m.PaceLabel = "Pace Aman"
		elapsed, duration := daysUntil(a.today(), p.From)+1, daysUntil(p.To, p.From)
		allowed := 100.0
		if duration > 0 {
			allowed = float64(max(0, min(duration, elapsed))) * 100 / float64(duration)
		}
		if float64(expense)*100/float64(income) > allowed {
			m.PaceLabel = "Perlu Dipantau"
		}
	}
	m.Transfer = Format(transfer, a.base)
	for k, n := range daily {
		prefix := ""
		if n > 0 {
			prefix = "+"
		}
		m.Daily[k] = prefix + Format(n, a.base)
	}
	for i := 0; i < 5; i++ {
		start := p.From.AddDate(0, 0, 7*i)
		if p.From.IsZero() || !start.Before(p.To) {
			break
		}
		end := start.AddDate(0, 0, 6)
		if !end.Before(p.To) {
			end = p.To.AddDate(0, 0, -1)
		}
		total := max(int64(1), weekIn[i]+weekOut[i])
		m.Weeks = append(m.Weeks, weeklyFlow{fmt.Sprintf("Minggu %d (%d–%d %s)", i+1, start.Day(), end.Day(), bulanID[int(end.Month())]), Format(weekIn[i], a.base), Format(weekOut[i], a.base), int(weekIn[i] * 100 / total), int(weekOut[i] * 100 / total)})
	}
	total := partnerTotal
	for name, count := range people {
		if name == "" {
			name = "Tanpa pencatat"
		}
		m.Partners = append(m.Partners, partnerActivity{name, count, count * 100 / max(1, total)})
	}
	sort.Slice(m.Partners, func(i, j int) bool { return m.Partners[i].Name < m.Partners[j].Name })
	return m
}

func readImage(r *http.Request, field string, maxSize int64, allowPDF bool) ([]byte, string, error) {
	f, _, err := r.FormFile(field)
	if errors.Is(err, http.ErrMissingFile) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", errors.New("Lampiran tidak dapat dibaca.")
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxSize+1))
	if err != nil || int64(len(b)) > maxSize {
		return nil, "", errors.New("Ukuran lampiran terlalu besar.")
	}
	mime := http.DetectContentType(b)
	if mime != "image/jpeg" && mime != "image/png" && mime != "image/webp" && !(allowPDF && mime == "application/pdf") {
		return nil, "", errors.New("Gunakan gambar JPG, PNG, WebP atau PDF untuk struk.")
	}
	return b, mime, nil
}
func (a *App) readRecorder(r *http.Request) (int64, error) {
	id, _ := strconv.ParseInt(r.FormValue("pencatat"), 10, 64)
	if id == 0 {
		id = userFrom(r.Context()).ID
	}
	var allowed bool
	err := a.store.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND family_id=$2 AND disabled_at IS NULL)`, id, family(r)).Scan(&allowed)
	if err != nil || !allowed {
		return 0, errors.New("Pencatat bukan anggota keluarga aktif.")
	}
	return id, nil
}
func (a *App) receipt(w http.ResponseWriter, r *http.Request) {
	var b []byte
	var mime string
	err := a.store.db.QueryRow(r.Context(), `SELECT receipt,receipt_mime FROM transactions WHERE id=$1 AND family_id=$2 AND receipt IS NOT NULL`, pathID(r), family(r)).Scan(&b, &mime)
	if errors.Is(err, pgx.ErrNoRows) {
		a.notFound(w)
		return
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", mime)
	if mime == "application/pdf" {
		w.Header().Set("Content-Disposition", `attachment; filename="struk.pdf"`)
	}
	w.Write(b)
}
func (a *App) avatar(w http.ResponseWriter, r *http.Request) {
	var b []byte
	var mime string
	err := a.store.db.QueryRow(r.Context(), `SELECT avatar,avatar_mime FROM users WHERE id=$1 AND family_id=$2 AND avatar IS NOT NULL`, pathID(r), family(r)).Scan(&b, &mime)
	if err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", mime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write(b)
}
func (a *App) updateAvatar(w http.ResponseWriter, r *http.Request) {
	b, mime, err := readImage(r, "avatar", 512<<10, false)
	if err != nil || len(b) == 0 {
		a.renderSettings(w, r, "Pilih foto JPG, PNG atau WebP maksimal 512 KB.", http.StatusBadRequest)
		return
	}
	_, err = a.store.db.Exec(r.Context(), `UPDATE users SET avatar=$1,avatar_mime=$2 WHERE id=$3 AND family_id=$4`, b, mime, userFrom(r.Context()).ID, family(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/pengaturan", http.StatusSeeOther)
}
func (a *App) react(w http.ResponseWriter, r *http.Request) {
	tx, err := a.store.db.Begin(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	defer tx.Rollback(r.Context())
	var id int64
	err = tx.QueryRow(r.Context(), `SELECT id FROM transactions WHERE id=$1 AND family_id=$2 FOR UPDATE`, pathID(r), family(r)).Scan(&id)
	if err != nil {
		a.notFound(w)
		return
	}
	res, err := tx.Exec(r.Context(), `DELETE FROM transaction_reactions WHERE transaction_id=$1 AND user_id=$2`, id, userFrom(r.Context()).ID)
	if err == nil && res.RowsAffected() == 0 {
		_, err = tx.Exec(r.Context(), `INSERT INTO transaction_reactions(transaction_id,user_id) VALUES($1,$2)`, id, userFrom(r.Context()).ID)
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/transaksi/%d", id), http.StatusSeeOther)
}
func (a *App) saveGoal(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("nama"))
	n, err := ParseAmount(r.FormValue("target"), a.base)
	var date pgtype.Date
	if raw := r.FormValue("tanggal"); raw != "" {
		t, e := time.ParseInLocation(formatTanggal, raw, a.loc)
		if e != nil {
			http.Error(w, "Tanggal target tidak valid", 422)
			return
		}
		date = pgtype.Date{Time: t, Valid: true}
	}
	if size := utf8.RuneCountInString(name); size < 2 || size > 80 || err != nil || n <= 0 {
		http.Error(w, "Isi nama dan target dana yang valid", 422)
		return
	}
	_, err = a.store.db.Exec(r.Context(), `INSERT INTO savings_goals(family_id,name,target_minor,target_on) VALUES($1,$2,$3,$4) ON CONFLICT(family_id) DO UPDATE SET name=EXCLUDED.name,target_minor=EXCLUDED.target_minor,target_on=EXCLUDED.target_on`, family(r), name, n, date)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/investasi", http.StatusSeeOther)
}
func (a *App) toggleSubscription(w http.ResponseWriter, r *http.Request) {
	res, err := a.store.db.Exec(r.Context(), `UPDATE subscriptions SET active=NOT active WHERE id=$1 AND family_id=$2`, pathID(r), family(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if res.RowsAffected() == 0 {
		a.notFound(w)
		return
	}
	http.Redirect(w, r, "/subscription", http.StatusSeeOther)
}

func (a *App) notifications(w http.ResponseWriter, r *http.Request) {
	type item struct{ ID, Title, Description, Amount, Href string }
	out := []item{}
	wallets, err := a.store.Wallets(r.Context(), family(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	cards, err := a.cardStatuses(r.Context(), family(r), wallets)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	for _, v := range perhatian(cards) {
		out = append(out, item{fmt.Sprint(v.WalletID), v.Nama, v.Pesan, v.Nominal, fmt.Sprintf("/dompet#kredit-%d", v.WalletID)})
	}
	subs, err := a.store.Subscriptions(r.Context(), family(r), true)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	for _, s := range subs {
		days := daysUntil(s.NextDate.Time, a.today())
		if days <= 7 {
			desc := fmt.Sprintf("Jatuh tempo %s", tanggalPendek(s.NextDate.Time))
			out = append(out, item{"sub-" + fmt.Sprint(s.ID), s.Name, desc, Format(s.AmountMinor, s.Currency), "/subscription"})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-store")
	json.NewEncoder(w).Encode(out)
}

func (a *App) syncStatus(w http.ResponseWriter, r *http.Request) {
	var revision int64
	if err := a.store.db.QueryRow(r.Context(), `SELECT ledger_revision FROM families WHERE id=$1`, family(r)).Scan(&revision); err != nil {
		a.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-store")
	json.NewEncoder(w).Encode(map[string]int64{"revision": revision})
}
func (a *App) savingTarget(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.FormValue("target"))
	if err != nil || n < 1 || n > 100 {
		a.renderSettings(w, r, "Target rasio tersimpan harus antara 1–100%.", 422)
		return
	}
	_, err = a.store.db.Exec(r.Context(), `UPDATE families SET saving_target=$1,ledger_revision=ledger_revision+1 WHERE id=$2`, n, family(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/pengaturan", http.StatusSeeOther)
}

// viewDebtCards keeps settled parties visible until explicitly hidden.
func viewDebtCards(parties []PartyView, txs []Tx) []debtCard {
	var cards []debtCard
	for _, party := range parties {
		if party.Kosong {
			if !party.Hidden {
				last := ""
				for _, tx := range txs {
					if tx.PartyID == party.ID {
						last = kindLabel[tx.Kind] + " " + Format(tx.AmountMinor, tx.WalletCur) + " · " + tanggalPendek(tx.Date)
						break
					}
				}
				cards = append(cards, debtCard{PartyView: party, Direction: "lunas", Remaining: "0", Last: last, Action: "Lihat riwayat", PaidPercent: 100})
			}
			continue
		}
		for _, bal := range party.Saldo {
			for _, direction := range []string{"hutang", "piutang"} {
				remaining, kind, action := bal.HutangMinor, "debt_in", "Bayar Hutang"
				if direction == "piutang" {
					remaining, kind, action = bal.PiutangMinor, "loan_out", "Catat Penerimaan Dana"
				}
				if remaining == 0 {
					continue
				}
				var original int64
				last := ""
				for _, t := range txs {
					if t.PartyID != party.ID || t.WalletCur != bal.Currency {
						continue
					}
					if t.Kind == kind {
						original += t.AmountMinor
					}
					if last == "" {
						last = kindLabel[t.Kind] + " " + Format(t.AmountMinor, t.WalletCur) + " · " + tanggalPendek(t.Date)
					}
				}
				due := ""
				if party.DueOn.Valid {
					due = tanggalPendek(party.DueOn.Time)
				}
				if remaining < 0 {
					action = "Lihat riwayat lebih bayar"
				}
				cards = append(cards, debtCard{
					PartyView: party, Original: Format(original, bal.Currency), Remaining: Format(remaining, bal.Currency),
					Last: last, Due: due, Direction: direction, Action: action, Overpaid: remaining < 0,
					PaidPercent: int(max(0, min(100, (original-remaining)*100/max(1, original)))),
				})
			}
		}
	}
	return cards
}
