package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ---------- render & error ----------

func (a *App) render(w http.ResponseWriter, r *http.Request, page string, data map[string]any) {
	t, ok := a.pages[page]
	if !ok {
		a.fail(w, r, errors.New("template tidak ada: "+page))
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	u := userFrom(r.Context())
	data["User"] = u
	data["Family"] = u.FamilyName
	data["Path"] = r.URL.Path
	data["V"] = a.ver

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout.html", data); err != nil {
		// Header sudah terkirim di titik ini; cuma bisa dicatat.
		log.Printf("render %s: %v", page, err)
	}
}

func (a *App) fail(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
	http.Error(w, "Terjadi kesalahan di server.", http.StatusInternalServerError)
}

func (a *App) notFound(w http.ResponseWriter) {
	http.Error(w, "Halaman tidak ditemukan.", http.StatusNotFound)
}

func (a *App) today() time.Time {
	n := time.Now().In(a.loc)
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, a.loc)
}

// family mengambil id keluarga dari sesi. Ini satu-satunya sumbernya: tidak
// pernah dari parameter URL atau isian form, yang bisa dikarang siapa saja.
func family(r *http.Request) int64 {
	return userFrom(r.Context()).FamilyID
}

func pathID(r *http.Request) int64 {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id
}

// ---------- dashboard ----------

func (a *App) dashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	wallets, err := a.store.Wallets(ctx, family(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	rates, err := a.rates(ctx, family(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	txs, err := a.store.Transactions(ctx, family(r), nil, "", 5)
	if err != nil {
		a.fail(w, r, err)
		return
	}

	// Ringkasan per kategori dibatasi bulan berjalan: itu rentang yang dipakai
	// keluarga saat menilai "bulan ini boros di mana", dan membuat angkanya
	// bisa dibandingkan antar bulan.
	awal := awalBulan(a.today())
	spend, err := a.store.CategorySpending(ctx, family(r), awal, awal.AddDate(0, 1, 0))
	if err != nil {
		a.fail(w, r, err)
		return
	}

	cards, err := a.cardStatuses(ctx, family(r), wallets)
	if err != nil {
		a.fail(w, r, err)
		return
	}

	a.render(w, r, "dashboard.html", map[string]any{
		"Cards":     cards,
		"Title":     "Dashboard",
		"Nav":       "dashboard",
		"Today":     tanggalPanjang(a.today()),
		"Summary":   summarize(wallets, rates, a.base),
		"Wallets":   viewWallets(wallets),
		"Recent":    viewTxs(txs, a.today()),
		"Breakdown": breakdown(spend, rates, a.base, namaBulan(awal)),
	})
}

// ---------- dompet ----------

var walletProviders = map[string][]string{
	"bank":     {"BCA", "Bank Mandiri", "BNI", "BRI", "CIMB Niaga", "Bank Permata", "BSI", "Danamon", "Jenius"},
	"credit":   {"BCA", "Bank Mandiri", "BNI", "BRI", "CIMB Niaga", "Bank Permata", "Citibank"},
	"ewallet":  {"GoPay", "OVO", "DANA", "ShopeePay", "LinkAja", "Jago"},
	"paylater": {"GoPayLater", "SPayLater", "Kredivo", "Akulaku", "Traveloka PayLater", "Indodana", "Atome"},
	"cash":     {},
}

var walletTypes = []struct{ Value, Label string }{
	{"cash", "Tunai"}, {"bank", "Bank"}, {"credit", "Kredit"},
	{"ewallet", "E-Wallet"}, {"paylater", "PayLater"},
}

// kreditType: jenis dompet yang punya limit dan siklus tagihan.
func kreditType(t string) bool { return t == "credit" || t == "paylater" }

func (a *App) walletList(w http.ResponseWriter, r *http.Request) {
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
	a.render(w, r, "dompet.html", map[string]any{
		"Title": "Dompet", "Nav": "dompet",
		"Wallets": viewWallets(wallets), "Cards": cards,
	})
}

// cardStatuses merangkum tiap akun berbasis kredit. Akun yang siklusnya belum
// diisi tetap ditampilkan — limit dan sisa pemakaiannya sudah berguna sendiri —
// hanya bagian tagihannya yang disembunyikan, karena tanpa tanggal cetak
// "tagihan" tidak punya arti dan menebaknya lebih menyesatkan daripada diam.
func (a *App) cardStatuses(ctx context.Context, familyID int64, wallets []Wallet) ([]CardView, error) {
	var out []CardView
	for _, wl := range wallets {
		if !wl.IsCredit() {
			continue
		}
		st := CardStatus{Wallet: wl, OutstandingMinor: wl.BalanceMinor}

		if wl.HasCycle() {
			settlement := SettlementTerakhir(a.today(), wl.SettlementDay, a.loc)
			atSettlement, creditsSince, err := a.store.CardBalances(ctx, familyID, wl.ID, settlement)
			if err != nil {
				return nil, err
			}
			st.HasCycle = true
			st.PayableMinor = TagihanKartu(atSettlement, creditsSince)
			st.Settlement = settlement
			st.Due = JatuhTempo(settlement, wl.PaymentDay, a.loc)
		}
		out = append(out, viewCard(st, a.today()))
	}
	return out, nil
}

func (a *App) walletForm(w http.ResponseWriter, r *http.Request) {
	f := map[string]string{"jenis": "bank", "mata_uang": a.base, "saldo_awal": "0"}
	var id int64

	if idStr := r.PathValue("id"); idStr != "" {
		id = pathID(r)
		wl, err := a.store.Wallet(r.Context(), family(r), id)
		if errors.Is(err, ErrNotFound) {
			a.notFound(w)
			return
		} else if err != nil {
			a.fail(w, r, err)
			return
		}
		f = map[string]string{
			"jenis": wl.Type, "penyedia": wl.Provider, "nama": wl.Name,
			"mata_uang": wl.Currency, "saldo_awal": FormatPlain(wl.InitialMinor, wl.Currency),
		}
		if wl.SettlementDay > 0 {
			f["tanggal_cetak"] = strconv.Itoa(wl.SettlementDay)
		}
		if wl.PaymentDay > 0 {
			f["tanggal_bayar"] = strconv.Itoa(wl.PaymentDay)
		}
		if wl.HasLimit() {
			f["limit"] = FormatPlain(wl.LimitMinor, wl.Currency)
		}
	}
	a.renderWalletForm(w, r, id, f, "")
}

func (a *App) renderWalletForm(w http.ResponseWriter, r *http.Request, id int64, f map[string]string, errMsg string) {
	action := "/dompet/baru"
	title := "Dompet Baru"
	if id != 0 {
		action = "/dompet/" + strconv.FormatInt(id, 10) + "/ubah"
		title = "Ubah Dompet"
	}
	if errMsg != "" {
		w.WriteHeader(http.StatusUnprocessableEntity)
	}
	a.render(w, r, "dompet_form.html", map[string]any{
		"Title": title, "Nav": "dompet", "Back": "/dompet",
		"Action": action, "ID": id, "Form": f, "Error": errMsg,
		"Types": walletTypes, "ProvidersJSON": jsonAttr(walletProviders), "Currencies": Currencies,
	})
}

// readWallet memvalidasi input form dompet.
func readWallet(r *http.Request) (Wallet, map[string]string, error) {
	f := map[string]string{
		"jenis":         r.FormValue("jenis"),
		"penyedia":      strings.TrimSpace(r.FormValue("penyedia")),
		"nama":          strings.TrimSpace(r.FormValue("nama")),
		"mata_uang":     r.FormValue("mata_uang"),
		"saldo_awal":    r.FormValue("saldo_awal"),
		"tanggal_cetak": strings.TrimSpace(r.FormValue("tanggal_cetak")),
		"tanggal_bayar": strings.TrimSpace(r.FormValue("tanggal_bayar")),
		"limit":         strings.TrimSpace(r.FormValue("limit")),
	}
	wl := Wallet{Type: f["jenis"], Provider: f["penyedia"], Name: f["nama"], Currency: f["mata_uang"]}

	if _, ok := walletProviders[wl.Type]; !ok {
		return wl, f, errors.New("Jenis dompet tidak dikenal.")
	}
	if wl.Type == "cash" {
		wl.Provider = ""
	}
	if len(wl.Name) < 2 {
		return wl, f, errors.New("Nama dompet minimal 2 karakter.")
	}
	if !knownCurrency(wl.Currency) {
		return wl, f, errors.New("Mata uang tidak dikenal.")
	}
	saldo, err := ParseAmount(f["saldo_awal"], wl.Currency)
	if err != nil {
		return wl, f, errors.New("Saldo awal tidak valid.")
	}
	wl.InitialMinor = saldo

	// Limit dan siklus tagihan hanya berarti untuk kartu kredit dan PayLater.
	// Jenis lain yang kebetulan mengirim kolomnya diabaikan, karena database
	// pun menolaknya.
	if kreditType(wl.Type) {
		cetak, errCetak := hariBulan(f["tanggal_cetak"])
		bayar, errBayar := hariBulan(f["tanggal_bayar"])
		if errCetak != nil || errBayar != nil {
			return wl, f, errors.New("Tanggal cetak dan tanggal bayar harus antara 1 sampai 31.")
		}
		if (cetak == 0) != (bayar == 0) {
			return wl, f, errors.New("Isi tanggal cetak dan tanggal bayar sekaligus, atau kosongkan keduanya.")
		}
		wl.SettlementDay, wl.PaymentDay = cetak, bayar

		if f["limit"] != "" {
			batas, err := ParseAmount(f["limit"], wl.Currency)
			if err != nil || batas <= 0 {
				return wl, f, errors.New("Limit harus angka lebih dari nol, atau dikosongkan.")
			}
			wl.LimitMinor = batas
		}
	} else {
		f["tanggal_cetak"], f["tanggal_bayar"], f["limit"] = "", "", ""
	}
	return wl, f, nil
}

// hariBulan membaca tanggal dalam bulan. Kosong berarti belum diisi, bukan
// salah — siklus tagihan boleh dilewati sampai user tahu tanggalnya.
func hariBulan(s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 31 {
		return 0, errors.New("tanggal di luar rentang")
	}
	return n, nil
}

func knownCurrency(code string) bool {
	for _, c := range Currencies {
		if c.Code == code {
			return true
		}
	}
	return false
}

func (a *App) walletCreate(w http.ResponseWriter, r *http.Request) {
	wl, f, err := readWallet(r)
	if err != nil {
		a.renderWalletForm(w, r, 0, f, err.Error())
		return
	}
	if _, err := a.store.CreateWallet(r.Context(), family(r), wl); err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/dompet", http.StatusSeeOther)
}

func (a *App) walletUpdate(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	wl, f, err := readWallet(r)
	if err != nil {
		a.renderWalletForm(w, r, id, f, err.Error())
		return
	}
	wl.ID = id

	// Mengubah mata uang dompet yang sudah punya transaksi akan mengubah arti
	// semua nominal yang tersimpan, jadi ditolak.
	old, err := a.store.Wallet(r.Context(), family(r), id)
	if errors.Is(err, ErrNotFound) {
		a.notFound(w)
		return
	} else if err != nil {
		a.fail(w, r, err)
		return
	}
	if old.Currency != wl.Currency {
		txs, err := a.store.Transactions(r.Context(), family(r), nil, "", 0)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		for _, t := range txs {
			if t.WalletID == id || t.ToWalletID == id {
				a.renderWalletForm(w, r, id, f,
					"Dompet ini sudah punya transaksi, mata uangnya tidak bisa diganti.")
				return
			}
		}
	}

	if err := a.store.UpdateWallet(r.Context(), family(r), wl); err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/dompet", http.StatusSeeOther)
}

func (a *App) walletDelete(w http.ResponseWriter, r *http.Request) {
	err := a.store.DeleteWallet(r.Context(), family(r), pathID(r))
	if errors.Is(err, ErrNotFound) {
		a.notFound(w)
		return
	}
	if err != nil {
		wallets, lerr := a.store.Wallets(r.Context(), family(r))
		if lerr != nil {
			a.fail(w, r, lerr)
			return
		}
		w.WriteHeader(http.StatusConflict)
		a.render(w, r, "dompet.html", map[string]any{
			"Title": "Dompet", "Nav": "dompet",
			"Wallets": viewWallets(wallets), "Error": err.Error(),
		})
		return
	}
	http.Redirect(w, r, "/dompet", http.StatusSeeOther)
}

// ---------- transaksi ----------

var kindLabel = map[string]string{
	"expense": "Pengeluaran", "income": "Pemasukan", "transfer": "Transfer",
	"debt_in": "Hutang", "debt_pay": "Bayar Hutang",
	"loan_out": "Piutang", "loan_in": "Terima Piutang",
}

var txFilters = []struct{ Value, Label string }{
	{"", "Semua"}, {"expense", "Keluar"}, {"income", "Masuk"},
	{"transfer", "Transfer"}, {"hutang", "Hutang"},
}

func (a *App) txList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	filter := r.URL.Query().Get("jenis")
	if _, ok := kindLabel[filter]; !ok && filter != "hutang" {
		filter = ""
	}
	kategori := r.URL.Query().Get("kategori")

	// Transfer dan hutang piutang tidak punya kategori: menyaring keduanya
	// sekaligus selalu kosong, jadi memilih kategori melepas filter jenis itu.
	if kategori != "" && (filter == "transfer" || filter == "hutang") {
		filter = ""
	}

	txs, err := a.store.Transactions(ctx, family(r), kindsForFilter(filter), kategori, 200)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	cats, err := a.store.Categories(ctx, family(r), "")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.render(w, r, "transaksi.html", map[string]any{
		"Title": "Transaksi", "Nav": "transaksi",
		"Filter": filter, "Filters": txFilters,
		"Kategori": kategori, "Categories": cats,
		"Groups": groupTxs(viewTxs(txs, a.today())),
	})
}

// kindsForFilter menerjemahkan pilihan filter jadi daftar jenis. "hutang"
// mewakili empat jenis sekaligus, karena bagi user hutang dan piutang adalah
// satu urusan meski tersimpan sebagai jenis terpisah.
func kindsForFilter(filter string) []string {
	switch filter {
	case "":
		return nil
	case "hutang":
		return []string{"debt_in", "debt_pay", "loan_out", "loan_in"}
	default:
		return []string{filter}
	}
}

func (a *App) txDetail(w http.ResponseWriter, r *http.Request) {
	t, err := a.store.Transaction(r.Context(), family(r), pathID(r))
	if errors.Is(err, ErrNotFound) {
		a.notFound(w)
		return
	} else if err != nil {
		a.fail(w, r, err)
		return
	}

	v := viewTx(t, a.today())
	data := map[string]any{
		"Title": "Detail Transaksi", "Nav": "transaksi", "Back": "/transaksi",
		"Tx": v, "KindLabel": kindLabel[t.Kind],
		"Waktu": tanggalPendek(t.Date) + ", " + t.CreatedAt.In(a.loc).Format("15:04"),
	}

	if t.IsCrossCur() {
		rates, err := a.rates(r.Context(), family(r))
		if err != nil {
			a.fail(w, r, err)
			return
		}
		data["Fee"] = Format(t.AdminFee, t.WalletCur)
		data["AmountIn"] = Format(t.AmountInMino, t.ToWalletCur)
		// "Kustom" kalau kurs transaksi ini beda dari kurs acuan. Perbandingannya
		// lewat nominal yang diterima, bukan rasio kurs: membandingkan rasio
		// secara eksak akan menandai hampir semua transfer sebagai kustom, karena
		// nominal diterima selalu dibulatkan ke sen terdekat lebih dulu.
		if ref, ok := rates[t.WalletCur+">"+t.ToWalletCur]; ok && ref.Valid() {
			if diff := ref.Convert(t.NetOutMinor()) - t.AmountInMino; diff > 1 || diff < -1 {
				data["RateCustom"] = true
			}
		}
	} else if t.IsTransfer() {
		data["Fee"] = Format(t.AdminFee, t.WalletCur)
		data["AmountIn"] = Format(t.AmountInMino, t.ToWalletCur)
	}
	data["Amount"] = Format(t.AmountMinor, t.WalletCur)
	a.render(w, r, "detail.html", data)
}

// txForm melayani tambah (GET /transaksi/baru?jenis=...) dan ubah.
func (a *App) txForm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var t Tx
	var id int64

	if r.PathValue("id") != "" {
		id = pathID(r)
		var err error
		t, err = a.store.Transaction(ctx, family(r), id)
		if errors.Is(err, ErrNotFound) {
			a.notFound(w)
			return
		} else if err != nil {
			a.fail(w, r, err)
			return
		}
	} else {
		t.Kind = r.URL.Query().Get("jenis")
		if _, ok := kindLabel[t.Kind]; !ok {
			t.Kind = "expense"
		}
		t.Date = a.today()
	}

	f := map[string]string{
		"tanggal":  t.Date.Format("2006-01-02"),
		"catatan":  t.Note,
		"kategori": t.Category,
	}
	// Tombol "Bayar Tagihan" di kartu kredit membuka form transfer ini dengan
	// dompet tujuan dan nominal sudah terisi. Pembayaran kartu memang transfer:
	// belanjanya sudah tercatat sebagai pengeluaran waktu kartu dipakai, jadi
	// mencatatnya lagi sebagai pengeluaran akan menghitungnya dua kali.
	if id == 0 && t.Kind == "transfer" {
		if ke := r.URL.Query().Get("ke"); ke != "" {
			f["ke_dompet"] = ke
		}
		if nominal := r.URL.Query().Get("nominal"); nominal != "" {
			f["nominal_keluar"] = nominal
			f["nominal_diterima"] = nominal
		}
	}
	if id != 0 {
		f["dompet"] = strconv.FormatInt(t.WalletID, 10)
		f["nominal"] = FormatPlain(t.AmountMinor, t.WalletCur)
		f["nominal_keluar"] = f["nominal"]
		if t.IsTransfer() {
			f["ke_dompet"] = strconv.FormatInt(t.ToWalletID, 10)
			f["nominal_diterima"] = FormatPlain(t.AmountInMino, t.ToWalletCur)
			f["biaya_admin"] = FormatPlain(t.AdminFee, t.WalletCur)
		}
	}
	a.renderTxForm(w, r, t.Kind, id, f, "")
}

func (a *App) renderTxForm(w http.ResponseWriter, r *http.Request, kind string, id int64, f map[string]string, errMsg string) {
	ctx := r.Context()
	wallets, err := a.store.Wallets(ctx, family(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if len(wallets) == 0 {
		http.Redirect(w, r, "/dompet/baru", http.StatusSeeOther)
		return
	}

	action := "/transaksi/baru?jenis=" + kind
	title := "Catat " + kindLabel[kind]
	if kind == "transfer" {
		title = "Transfer Antar Dompet"
	}
	if id != 0 {
		action = "/transaksi/" + strconv.FormatInt(id, 10) + "/ubah"
		title = "Ubah " + kindLabel[kind]
	}

	data := map[string]any{
		"Title": title, "Nav": "transaksi", "Back": "/transaksi",
		"Action": action, "Kind": kind, "KindLabel": kindLabel[kind], "ID": id,
		"Form": f, "Error": errMsg,
		"Wallets": viewWallets(wallets),
	}
	page := "transaksi_form.html"

	if kind != "transfer" {
		cats, err := a.store.CategoryNames(ctx, family(r), kind)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		data["Categories"] = cats
	}

	if kind == "transfer" {
		if len(wallets) < 2 {
			http.Redirect(w, r, "/dompet/baru", http.StatusSeeOther)
			return
		}
		// Tanpa nilai awal, kedua dropdown akan menunjuk dompet pertama dan form
		// terbuka dalam keadaan tidak valid (transfer ke diri sendiri). Lebih
		// buruk lagi, skrip akan menganggapnya transfer sesama mata uang dan
		// menyalin nominal keluar ke nominal diterima.
		if f["dompet"] == "" {
			f["dompet"] = strconv.FormatInt(wallets[0].ID, 10)
		}
		if f["ke_dompet"] == "" || f["ke_dompet"] == f["dompet"] {
			f["ke_dompet"] = strconv.FormatInt(firstOtherWallet(wallets, f["dompet"]), 10)
		}

		storeRates, err := a.store.Rates(ctx, family(r))
		if err != nil {
			a.fail(w, r, err)
			return
		}
		apiRates := a.rateSrc.pairs(currencyCodes())
		data["RatesJSON"] = jsonAttr(ratesForJS(apiRates, storeRates))
		data["Base"] = a.base
		if fetched, _ := a.rateSrc.status(); !fetched.IsZero() {
			data["RateUpdated"] = tanggalPendek(fetched.In(a.loc))
		}
		page = "transfer_form.html"
	}
	if errMsg != "" {
		w.WriteHeader(http.StatusUnprocessableEntity)
	}
	a.render(w, r, page, data)
}

// firstOtherWallet memilih dompet mana pun selain yang sudah jadi sumber,
// diutamakan yang mata uangnya berbeda supaya form transfer lintas mata uang
// langsung menampilkan kolom kurs.
func firstOtherWallet(wallets []Wallet, fromID string) int64 {
	var fallback int64
	var fromCur string
	for _, w := range wallets {
		if strconv.FormatInt(w.ID, 10) == fromID {
			fromCur = w.Currency
			break
		}
	}
	for _, w := range wallets {
		if strconv.FormatInt(w.ID, 10) == fromID {
			continue
		}
		if fallback == 0 {
			fallback = w.ID
		}
		if w.Currency != fromCur {
			return w.ID
		}
	}
	return fallback
}

func currencyCodes() []string {
	out := make([]string, 0, len(Currencies))
	for _, c := range Currencies {
		out = append(out, c.Code)
	}
	return out
}

// ratesForJS meratakan kurs jadi bentuk yang dibaca skrip form transfer:
// "IDR>USD" -> "fromMinor:toMinor:powFrom:powTo:sumber".
// Rasionya dikirim mentah supaya pembagiannya terjadi di skrip dan tidak ada
// presisi yang hilang dua kali. Kurs pasar menimpa kurs transfer terakhir
// karena lebih baru, tapi asalnya ikut dikirim agar bisa disebut ke user.
func ratesForJS(api, store map[string]Rate) map[string]string {
	out := make(map[string]string, len(api)+len(store))
	add := func(rates map[string]Rate, source string) {
		for k, r := range rates {
			if !r.Valid() {
				continue
			}
			out[k] = strconv.FormatInt(r.FromMinor, 10) + ":" + strconv.FormatInt(r.ToMinor, 10) +
				":" + strconv.FormatInt(pow10(Exp(r.From)), 10) + ":" + strconv.FormatInt(pow10(Exp(r.To)), 10) +
				":" + source
		}
	}
	add(store, "catatan")
	add(api, "pasar")
	return out
}

// jsonAttr menyiapkan data untuk atribut data-* di HTML. html/template yang
// meng-escape-nya; skrip di sisi klien tinggal JSON.parse.
func jsonAttr(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// readTx memvalidasi input form transaksi.
func (a *App) readTx(r *http.Request, kind string) (Tx, map[string]string, error) {
	ctx := r.Context()
	f := map[string]string{
		"dompet":           r.FormValue("dompet"),
		"ke_dompet":        r.FormValue("ke_dompet"),
		"kategori":         r.FormValue("kategori"),
		"nominal":          r.FormValue("nominal"),
		"nominal_keluar":   r.FormValue("nominal_keluar"),
		"nominal_diterima": r.FormValue("nominal_diterima"),
		"kurs":             r.FormValue("kurs"),
		"biaya_admin":      r.FormValue("biaya_admin"),
		"tanggal":          r.FormValue("tanggal"),
		"catatan":          strings.TrimSpace(r.FormValue("catatan")),
	}
	t := Tx{Kind: kind, Note: f["catatan"], Category: f["kategori"]}

	tanggal, err := time.ParseInLocation("2006-01-02", f["tanggal"], a.loc)
	if err != nil {
		return t, f, errors.New("Tanggal tidak valid.")
	}
	t.Date = tanggal

	walletID, _ := strconv.ParseInt(f["dompet"], 10, 64)
	from, err := a.store.Wallet(ctx, family(r), walletID)
	if err != nil {
		return t, f, errors.New("Dompet tidak valid.")
	}
	t.WalletID = from.ID
	t.WalletCur = from.Currency

	if kind != "transfer" {
		known, err := a.store.CategoryNames(ctx, family(r), kind)
		if err != nil {
			return t, f, errors.New("Gagal membaca daftar kategori.")
		}
		if !slicesContains(known, t.Category) {
			return t, f, errors.New("Kategori belum dipilih.")
		}
		amount, err := ParseAmount(f["nominal"], from.Currency)
		if err != nil || amount <= 0 {
			return t, f, errors.New("Nominal harus lebih dari nol.")
		}
		t.AmountMinor = amount
		return t, f, nil
	}

	// --- transfer ---
	t.Category = ""
	toID, _ := strconv.ParseInt(f["ke_dompet"], 10, 64)
	if toID == from.ID {
		return t, f, errors.New("Dompet asal dan tujuan tidak boleh sama.")
	}
	to, err := a.store.Wallet(ctx, family(r), toID)
	if err != nil {
		return t, f, errors.New("Dompet tujuan tidak valid.")
	}
	t.ToWalletID = to.ID
	t.ToWalletCur = to.Currency
	t.ToWalletName = to.Name

	out, err := ParseAmount(f["nominal_keluar"], from.Currency)
	if err != nil || out <= 0 {
		return t, f, errors.New("Nominal keluar harus lebih dari nol.")
	}
	in, err := ParseAmount(f["nominal_diterima"], to.Currency)
	if err != nil || in <= 0 {
		return t, f, errors.New("Nominal diterima harus lebih dari nol.")
	}
	t.AmountMinor = out
	t.AmountInMino = in

	// Biaya admin: pakai isian user kalau ada, kalau kosong hitung dari kurs.
	var rate Rate
	if strings.TrimSpace(f["biaya_admin"]) != "" {
		fee, err := ParseAmount(f["biaya_admin"], from.Currency)
		if err != nil {
			return t, f, errors.New("Biaya admin tidak valid.")
		}
		t.AdminFee = fee
	} else if from.Currency == to.Currency {
		t.AdminFee = out - in
	} else if s := strings.TrimSpace(f["kurs"]); s != "" {
		priceCur, perCur := a.quoteDirection(ctx, family(r), from.Currency, to.Currency)
		rate, err = ParseUnitRate(s, priceCur, perCur, from.Currency, to.Currency)
		if err != nil {
			return t, f, errors.New("Kurs tidak valid.")
		}
		t.AdminFee = AdminFee(out, in, from.Currency, to.Currency, rate)
	}

	// Nominal diterima hanya bisa dicatat sampai satuan terkecil mata uangnya:
	// Rp15.500.000 dibagi kurs jatuh di $864,1697, dan sen terdekat yang bisa
	// benar-benar diterima nilainya berbeda beberapa rupiah dari yang keluar.
	// Selisih sekecil itu adalah sisa pembulatan, bukan input yang keliru, jadi
	// biaya adminnya dinolkan. Di atas itu tetap ditolak.
	if t.AdminFee < 0 && rate.Valid() {
		if slack := rate.Invert().Convert(1); slack > 0 && -t.AdminFee <= slack {
			t.AdminFee = 0
		}
	}
	if t.AdminFee < 0 {
		return t, f, errors.New("Nominal diterima lebih besar dari nilai yang keluar. Periksa kurs atau nominalnya.")
	}
	if t.AdminFee >= out {
		return t, f, errors.New("Biaya admin tidak boleh sebesar atau melebihi nominal keluar.")
	}
	return t, f, nil
}

// quoteDirection memilih arah tampilan kurs yang enak dibaca:
// "1 USD = Rp15.500", bukan "1 IDR = $0,000064".
// Mengembalikan (mata uang harga, mata uang yang dihargai).
func (a *App) quoteDirection(ctx context.Context, familyID int64, from, to string) (string, string) {
	if rates, err := a.rates(ctx, familyID); err == nil {
		if r, ok := rates[from+">"+to]; ok && r.Valid() {
			_, priceCur, perCur := r.Unit()
			return priceCur, perCur
		}
	}
	// Belum ada catatan kurs: harga mata uang asing dalam mata uang dasar.
	if to == a.base {
		return a.base, from
	}
	return from, to
}

func slicesContains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// checkBalance menolak transaksi yang tidak mungkin terjadi: dompet biasa yang
// jadi minus, atau akun kredit yang menembus limitnya.
//
// Akun kredit memang dirancang bersaldo negatif, jadi yang dijaga bukan nol
// melainkan pagunya. Tanpa limit terisi, tidak ada yang bisa dijaga dan
// pemakaian dibiarkan — menebak pagunya lebih berbahaya daripada diam.
func (a *App) checkBalance(r *http.Request, t Tx, excludeTxID int64) error {
	wl, err := a.store.Wallet(r.Context(), family(r), t.WalletID)
	if err != nil {
		return err
	}
	if t.Kind == "income" {
		return nil
	}

	after := wl.BalanceMinor - t.AmountMinor
	if excludeTxID != 0 {
		if old, err := a.store.Transaction(r.Context(), family(r), excludeTxID); err == nil && old.WalletID == wl.ID {
			after += old.AmountMinor
		}
	}

	if wl.IsCredit() {
		if wl.HasLimit() && -after > wl.LimitMinor {
			return errors.New("Melebihi limit " + wl.Name + ". Sisa limit " +
				Format(wl.SisaLimitMinor(), wl.Currency) + " dari " +
				Format(wl.LimitMinor, wl.Currency) + ".")
		}
		return nil
	}
	if after < 0 {
		return errors.New("Saldo " + wl.Name + " tidak cukup. Tersedia " + Format(wl.BalanceMinor, wl.Currency) + ".")
	}
	return nil
}

func (a *App) txCreate(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("jenis")
	if _, ok := kindLabel[kind]; !ok {
		kind = "expense"
	}
	t, f, err := a.readTx(r, kind)
	if err != nil {
		a.renderTxForm(w, r, kind, 0, f, err.Error())
		return
	}
	if err := a.checkBalance(r, t, 0); err != nil {
		a.renderTxForm(w, r, kind, 0, f, err.Error())
		return
	}
	id, err := a.store.CreateTx(r.Context(), family(r), t, userFrom(r.Context()).ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/transaksi/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (a *App) txUpdate(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	old, err := a.store.Transaction(r.Context(), family(r), id)
	if errors.Is(err, ErrNotFound) {
		a.notFound(w)
		return
	} else if err != nil {
		a.fail(w, r, err)
		return
	}

	t, f, err := a.readTx(r, old.Kind)
	if err != nil {
		a.renderTxForm(w, r, old.Kind, id, f, err.Error())
		return
	}
	t.ID = id
	if err := a.checkBalance(r, t, id); err != nil {
		a.renderTxForm(w, r, old.Kind, id, f, err.Error())
		return
	}
	if err := a.store.UpdateTx(r.Context(), family(r), t); err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/transaksi/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (a *App) txDelete(w http.ResponseWriter, r *http.Request) {
	err := a.store.DeleteTx(r.Context(), family(r), pathID(r))
	if errors.Is(err, ErrNotFound) {
		a.notFound(w)
		return
	} else if err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/transaksi", http.StatusSeeOther)
}

// ---------- kategori ----------

func (a *App) categoryList(w http.ResponseWriter, r *http.Request) {
	a.renderCategories(w, r, "", 0)
}

// renderCategories menampilkan daftar kategori. errMsg kosong berarti tidak ada
// masalah; status dipakai supaya kegagalan hapus tidak dijawab 200.
func (a *App) renderCategories(w http.ResponseWriter, r *http.Request, errMsg string, status int) {
	cats, err := a.store.Categories(r.Context(), family(r), "")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var expense, income []Category
	for _, c := range cats {
		if c.Kind == "income" {
			income = append(income, c)
		} else {
			expense = append(expense, c)
		}
	}
	if status != 0 {
		w.WriteHeader(status)
	}
	a.render(w, r, "kategori.html", map[string]any{
		"Title": "Kategori", "Nav": "kategori",
		"Expense": expense, "Income": income, "Error": errMsg,
	})
}

func (a *App) categoryForm(w http.ResponseWriter, r *http.Request) {
	f := map[string]string{"jenis": r.URL.Query().Get("jenis")}
	if _, ok := kindLabel[f["jenis"]]; !ok || f["jenis"] == "transfer" {
		f["jenis"] = "expense"
	}
	var id int64

	if r.PathValue("id") != "" {
		id = pathID(r)
		c, err := a.store.Category(r.Context(), family(r), id)
		if errors.Is(err, ErrNotFound) {
			a.notFound(w)
			return
		} else if err != nil {
			a.fail(w, r, err)
			return
		}
		f["jenis"], f["nama"] = c.Kind, c.Name
	}
	a.renderCategoryForm(w, r, id, f, "")
}

func (a *App) renderCategoryForm(w http.ResponseWriter, r *http.Request, id int64, f map[string]string, errMsg string) {
	action, title := "/kategori/baru", "Kategori Baru"
	if id != 0 {
		action = "/kategori/" + strconv.FormatInt(id, 10) + "/ubah"
		title = "Ubah Kategori"
	}
	if errMsg != "" {
		w.WriteHeader(http.StatusUnprocessableEntity)
	}
	a.render(w, r, "kategori_form.html", map[string]any{
		"Title": title, "Nav": "kategori", "Back": "/kategori",
		"Action": action, "ID": id, "Form": f, "Error": errMsg,
		"Kinds": []struct{ Value, Label string }{
			{"expense", "Pengeluaran"}, {"income", "Pemasukan"},
		},
	})
}

func readCategory(r *http.Request) (kind, name string, f map[string]string, err error) {
	f = map[string]string{
		"jenis": r.FormValue("jenis"),
		"nama":  strings.TrimSpace(r.FormValue("nama")),
	}
	if f["jenis"] != "expense" && f["jenis"] != "income" {
		return "", "", f, errors.New("Jenis kategori tidak dikenal.")
	}
	if len(f["nama"]) < 2 {
		return "", "", f, errors.New("Nama kategori minimal 2 karakter.")
	}
	if len(f["nama"]) > 40 {
		return "", "", f, errors.New("Nama kategori terlalu panjang.")
	}
	return f["jenis"], f["nama"], f, nil
}

func (a *App) categoryCreate(w http.ResponseWriter, r *http.Request) {
	kind, name, f, err := readCategory(r)
	if err != nil {
		a.renderCategoryForm(w, r, 0, f, err.Error())
		return
	}
	if err := a.store.CreateCategory(r.Context(), family(r), kind, name); err != nil {
		// Satu-satunya kegagalan yang wajar di sini adalah nama kembar.
		a.renderCategoryForm(w, r, 0, f, "Kategori dengan nama itu sudah ada.")
		return
	}
	http.Redirect(w, r, "/kategori", http.StatusSeeOther)
}

func (a *App) categoryUpdate(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	_, name, f, err := readCategory(r)
	if err != nil {
		a.renderCategoryForm(w, r, id, f, err.Error())
		return
	}
	// Jenis kategori tidak bisa diubah: transaksi lama dicocokkan lewat pasangan
	// jenis dan nama, jadi memindahkan kategori antar jenis akan memutus
	// kaitannya dengan transaksi yang sudah ada.
	if err := a.store.RenameCategory(r.Context(), family(r), id, name); errors.Is(err, ErrNotFound) {
		a.notFound(w)
		return
	} else if err != nil {
		a.renderCategoryForm(w, r, id, f, "Kategori dengan nama itu sudah ada.")
		return
	}
	http.Redirect(w, r, "/kategori", http.StatusSeeOther)
}

func (a *App) categoryDelete(w http.ResponseWriter, r *http.Request) {
	err := a.store.DeleteCategory(r.Context(), family(r), pathID(r))
	if errors.Is(err, ErrNotFound) {
		a.notFound(w)
		return
	}
	if err != nil {
		a.renderCategories(w, r, err.Error(), http.StatusConflict)
		return
	}
	http.Redirect(w, r, "/kategori", http.StatusSeeOther)
}
