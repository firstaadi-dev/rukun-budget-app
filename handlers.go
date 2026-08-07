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
	data["User"] = userFrom(r.Context())
	data["Family"] = a.family
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

func pathID(r *http.Request) int64 {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id
}

// ---------- dashboard ----------

func (a *App) dashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	wallets, err := a.store.Wallets(ctx)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	rates, err := a.store.Rates(ctx)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	txs, err := a.store.Transactions(ctx, "", 5)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.render(w, r, "dashboard.html", map[string]any{
		"Title":   "Dashboard",
		"Nav":     "dashboard",
		"Today":   tanggalPanjang(a.today()),
		"Summary": summarize(wallets, rates, a.base),
		"Wallets": viewWallets(wallets),
		"Recent":  viewTxs(txs, a.today()),
	})
}

// ---------- dompet ----------

var walletProviders = map[string][]string{
	"bank":    {"BCA", "Bank Mandiri", "BNI", "BRI", "CIMB Niaga", "Bank Permata", "BSI", "Danamon", "Jenius"},
	"credit":  {"BCA", "Bank Mandiri", "BNI", "BRI", "CIMB Niaga", "Bank Permata", "Citibank"},
	"ewallet": {"GoPay", "OVO", "DANA", "ShopeePay", "LinkAja", "Jago"},
	"cash":    {},
}

var walletTypes = []struct{ Value, Label string }{
	{"cash", "Tunai"}, {"bank", "Bank"}, {"credit", "Kredit"}, {"ewallet", "E-Wallet"},
}

func (a *App) walletList(w http.ResponseWriter, r *http.Request) {
	wallets, err := a.store.Wallets(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.render(w, r, "dompet.html", map[string]any{
		"Title": "Dompet", "Nav": "dompet", "Wallets": viewWallets(wallets),
	})
}

func (a *App) walletForm(w http.ResponseWriter, r *http.Request) {
	f := map[string]string{"jenis": "bank", "mata_uang": a.base, "saldo_awal": "0"}
	var id int64

	if idStr := r.PathValue("id"); idStr != "" {
		id = pathID(r)
		wl, err := a.store.Wallet(r.Context(), id)
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
		"jenis":      r.FormValue("jenis"),
		"penyedia":   strings.TrimSpace(r.FormValue("penyedia")),
		"nama":       strings.TrimSpace(r.FormValue("nama")),
		"mata_uang":  r.FormValue("mata_uang"),
		"saldo_awal": r.FormValue("saldo_awal"),
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
	return wl, f, nil
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
	if _, err := a.store.CreateWallet(r.Context(), wl); err != nil {
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
	old, err := a.store.Wallet(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		a.notFound(w)
		return
	} else if err != nil {
		a.fail(w, r, err)
		return
	}
	if old.Currency != wl.Currency {
		txs, err := a.store.Transactions(r.Context(), "", 0)
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

	if err := a.store.UpdateWallet(r.Context(), wl); err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/dompet", http.StatusSeeOther)
}

func (a *App) walletDelete(w http.ResponseWriter, r *http.Request) {
	err := a.store.DeleteWallet(r.Context(), pathID(r))
	if errors.Is(err, ErrNotFound) {
		a.notFound(w)
		return
	}
	if err != nil {
		wallets, lerr := a.store.Wallets(r.Context())
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

var categories = map[string][]string{
	"expense": {"Belanja", "Tagihan", "Transportasi", "Makanan", "Kesehatan", "Lainnya"},
	"income":  {"Gaji", "Bonus", "Hadiah", "Investasi", "Lainnya"},
}

var kindLabel = map[string]string{
	"expense": "Pengeluaran", "income": "Pemasukan", "transfer": "Transfer",
}

var txFilters = []struct{ Value, Label string }{
	{"", "Semua"}, {"expense", "Keluar"}, {"income", "Masuk"}, {"transfer", "Transfer"},
}

func (a *App) txList(w http.ResponseWriter, r *http.Request) {
	filter := r.URL.Query().Get("jenis")
	if _, ok := kindLabel[filter]; !ok {
		filter = ""
	}
	txs, err := a.store.Transactions(r.Context(), filter, 200)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.render(w, r, "transaksi.html", map[string]any{
		"Title": "Transaksi", "Nav": "transaksi",
		"Filter": filter, "Filters": txFilters,
		"Groups": groupTxs(viewTxs(txs, a.today())),
	})
}

func (a *App) txDetail(w http.ResponseWriter, r *http.Request) {
	t, err := a.store.Transaction(r.Context(), pathID(r))
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
		rates, err := a.store.Rates(r.Context())
		if err != nil {
			a.fail(w, r, err)
			return
		}
		data["Fee"] = Format(t.AdminFee, t.WalletCur)
		data["AmountIn"] = Format(t.AmountInMino, t.ToWalletCur)
		// "Kustom" kalau kurs transaksi ini beda dari kurs terakhir pasangan
		// mata uang yang sama.
		if last, ok := rates[t.WalletCur+">"+t.ToWalletCur]; ok && !last.SameAs(t.Rate()) {
			data["RateCustom"] = true
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
		t, err = a.store.Transaction(ctx, id)
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
	wallets, err := a.store.Wallets(ctx)
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
		"Wallets": viewWallets(wallets), "Categories": categories[kind],
	}
	page := "transaksi_form.html"

	if kind == "transfer" {
		rates, err := a.store.Rates(ctx)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		data["RatesJSON"] = jsonAttr(ratesForJS(rates))
		data["Base"] = a.base
		page = "transfer_form.html"
	}
	if errMsg != "" {
		w.WriteHeader(http.StatusUnprocessableEntity)
	}
	a.render(w, r, page, data)
}

// ratesForJS meratakan kurs jadi bentuk yang enak dibaca skrip form transfer:
// "IDR>USD" -> berapa unit USD per 1 IDR, sebagai string desimal.
func ratesForJS(rates map[string]Rate) map[string]string {
	out := make(map[string]string, len(rates))
	for k, r := range rates {
		if !r.Valid() {
			continue
		}
		// Kirim rasio mentah; skrip yang membaginya, jadi tidak ada presisi
		// yang hilang di sisi Go.
		out[k] = strconv.FormatInt(r.FromMinor, 10) + ":" + strconv.FormatInt(r.ToMinor, 10) +
			":" + strconv.FormatInt(pow10(Exp(r.From)), 10) + ":" + strconv.FormatInt(pow10(Exp(r.To)), 10)
	}
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
	from, err := a.store.Wallet(ctx, walletID)
	if err != nil {
		return t, f, errors.New("Dompet tidak valid.")
	}
	t.WalletID = from.ID
	t.WalletCur = from.Currency

	if kind != "transfer" {
		if !slicesContains(categories[kind], t.Category) {
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
	to, err := a.store.Wallet(ctx, toID)
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
	if strings.TrimSpace(f["biaya_admin"]) != "" {
		fee, err := ParseAmount(f["biaya_admin"], from.Currency)
		if err != nil {
			return t, f, errors.New("Biaya admin tidak valid.")
		}
		t.AdminFee = fee
	} else if from.Currency == to.Currency {
		t.AdminFee = out - in
	} else if s := strings.TrimSpace(f["kurs"]); s != "" {
		priceCur, perCur := a.quoteDirection(ctx, from.Currency, to.Currency)
		rate, err := ParseUnitRate(s, priceCur, perCur, from.Currency, to.Currency)
		if err != nil {
			return t, f, errors.New("Kurs tidak valid.")
		}
		t.AdminFee = AdminFee(out, in, from.Currency, to.Currency, rate)
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
func (a *App) quoteDirection(ctx context.Context, from, to string) (string, string) {
	if rates, err := a.store.Rates(ctx); err == nil {
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

// checkBalance menolak transaksi yang membuat dompet non-kartu-kredit minus.
// Kartu kredit memang dirancang bersaldo negatif, jadi dilewati.
func (a *App) checkBalance(r *http.Request, t Tx, excludeTxID int64) error {
	wl, err := a.store.Wallet(r.Context(), t.WalletID)
	if err != nil {
		return err
	}
	if wl.IsCredit() || t.Kind == "income" {
		return nil
	}
	after := wl.BalanceMinor - t.AmountMinor
	if excludeTxID != 0 {
		if old, err := a.store.Transaction(r.Context(), excludeTxID); err == nil && old.WalletID == wl.ID {
			after += old.AmountMinor
		}
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
	id, err := a.store.CreateTx(r.Context(), t, userFrom(r.Context()).ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/transaksi/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (a *App) txUpdate(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	old, err := a.store.Transaction(r.Context(), id)
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
	if err := a.store.UpdateTx(r.Context(), t); err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/transaksi/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (a *App) txDelete(w http.ResponseWriter, r *http.Request) {
	err := a.store.DeleteTx(r.Context(), pathID(r))
	if errors.Is(err, ErrNotFound) {
		a.notFound(w)
		return
	} else if err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/transaksi", http.StatusSeeOther)
}
