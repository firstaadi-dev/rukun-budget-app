package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ---------- dompet ----------

var walletProviders = map[string][]string{
	"bank":     {"BCA", "Bank Mandiri", "BNI", "BRI", "CIMB Niaga", "Bank Permata", "BSI", "Danamon", "Jenius"},
	"credit":   {"BCA", "Bank Mandiri", "BNI", "BRI", "CIMB Niaga", "Bank Permata", "Citibank"},
	"ewallet":  {"GoPay", "OVO", "DANA", "ShopeePay", "LinkAja", "Jago"},
	"paylater": {"GoPayLater", "SPayLater", "Kredivo", "Akulaku", "Traveloka PayLater", "Indodana", "Atome"},
	"cash":     {},
	// Saldo yang mengendap di broker sebelum jadi aset. Daftarnya campuran
	// broker saham luar negeri dan penyedia reksadana lokal, karena keduanya
	// sama saja bentuknya: uang yang sudah keluar dari bank tapi belum jadi
	// apa-apa.
	"broker": {"Interactive Brokers", "Charles Schwab", "Pluang", "Nanovest", "Stockbit Sekuritas",
		"Bibit", "Bareksa", "Ajaib"},
}

var walletTypes = []struct{ Value, Label string }{
	{"cash", "Tunai"}, {"bank", "Bank"}, {"credit", "Kredit"},
	{"ewallet", "E-Wallet"}, {"paylater", "PayLater"}, {"broker", "Broker"},
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
		"Wallets": withCards(viewWallets(wallets), cards),
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
	urutkanKartu(out)
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
		txs, err := a.store.Transactions(r.Context(), family(r), TxFilter{})
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

const walletAdjustmentCategory = "Penyesuaian Saldo"

func adjustmentTx(w Wallet, target int64, today time.Time) (Tx, bool) {
	diff := target - w.BalanceMinor
	if diff == 0 {
		return Tx{}, false
	}
	kind := "income"
	if diff < 0 {
		kind, diff = "expense", -diff
	}
	return Tx{
		Kind: kind, Date: today, WalletID: w.ID, WalletCur: w.Currency,
		AmountMinor: diff, Category: walletAdjustmentCategory,
		Note: walletAdjustmentCategory, IsAdjustment: true,
	}, true
}

func (a *App) walletAdjustForm(w http.ResponseWriter, r *http.Request) {
	wallet, err := a.store.Wallet(r.Context(), family(r), pathID(r))
	if errors.Is(err, ErrNotFound) {
		a.notFound(w)
		return
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.render(w, r, "dompet_adjust.html", map[string]any{
		"Title": "Sesuaikan Saldo", "Nav": "dompet", "Back": "/dompet",
		"Wallet": viewWallet(wallet), "Form": map[string]string{
			"saldo": FormatPlain(wallet.BalanceMinor, wallet.Currency),
		},
	})
}

func (a *App) walletAdjust(w http.ResponseWriter, r *http.Request) {
	wallet, err := a.store.Wallet(r.Context(), family(r), pathID(r))
	if errors.Is(err, ErrNotFound) {
		a.notFound(w)
		return
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	f := map[string]string{"saldo": r.FormValue("saldo")}
	target, err := ParseAmount(f["saldo"], wallet.Currency)
	if err != nil {
		a.renderWalletAdjustError(w, r, wallet, f, "Saldo tidak valid.")
		return
	}
	t, ok := adjustmentTx(wallet, target, a.today())
	if !ok {
		http.Redirect(w, r, "/dompet", http.StatusSeeOther)
		return
	}
	if err := a.store.EnsureCategory(r.Context(), family(r), t.Kind, walletAdjustmentCategory); err != nil {
		a.fail(w, r, err)
		return
	}
	if _, err := a.store.CreateTx(r.Context(), family(r), t, userFrom(r.Context()).ID); err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/dompet", http.StatusSeeOther)
}

func (a *App) renderWalletAdjustError(w http.ResponseWriter, r *http.Request, wallet Wallet, f map[string]string, msg string) {
	w.WriteHeader(http.StatusUnprocessableEntity)
	a.render(w, r, "dompet_adjust.html", map[string]any{
		"Title": "Sesuaikan Saldo", "Nav": "dompet", "Back": "/dompet",
		"Wallet": viewWallet(wallet), "Form": f, "Error": msg,
	})
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
