package main

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Hutang piutang dicatat di tabel transactions yang sama dengan pengeluaran dan
// pemasukan, memakai empat jenis: debt_in, debt_pay, loan_out, loan_in.
//
// Alasannya saldo dompet dihitung dari satu query GetWallets. Kalau hutang
// piutang punya tabel sendiri yang juga menggerakkan saldo, query itu harus
// menggabungkan dua sumber, dan keduanya bisa menyimpang tanpa ketahuan.
//
// Yang membedakan dari pengeluaran biasa: dompetnya boleh kosong. Meminjam uang
// yang langsung dipakai tanpa pernah masuk rekening tetap menambah kewajiban,
// dan memaksa memilih dompet akan membuat saldo dompet itu salah.

var debtKinds = []struct{ Value, Label, Hint string }{
	{"debt_in", "Hutang", "Kita menerima pinjaman"},
	{"loan_out", "Piutang", "Kita memberi pinjaman"},
}

// arahPembayaran: jenis transaksi yang mengurangi saldo tiap arah.
var arahPembayaran = map[string]string{
	"hutang":  "debt_pay", // kita membayar
	"piutang": "loan_in",  // kita menerima pelunasan
}

// ---------- daftar dan ringkasan ----------

func (a *App) debtList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	parties, err := a.store.Parties(ctx, family(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	rates, err := a.rates(ctx, family(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.render(w, r, "hutang.html", map[string]any{
		"Title": "Hutang & Piutang", "Nav": "hutang",
		"Summary": summarizeDebts(parties, rates, a.base),
	})
}

func (a *App) partyDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, err := a.store.Party(ctx, family(r), pathID(r))
	if errors.Is(err, ErrNotFound) {
		a.notFound(w)
		return
	} else if err != nil {
		a.fail(w, r, err)
		return
	}
	txs, err := a.store.PartyTxs(ctx, family(r), p.ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.render(w, r, "pihak.html", map[string]any{
		"Title": p.Name, "Nav": "hutang", "Back": "/hutang",
		"Party": viewParty(p), "Groups": groupTxs(viewTxs(txs, a.today())),
	})
}

// ---------- catat hutang atau piutang ----------

func (a *App) debtForm(w http.ResponseWriter, r *http.Request) {
	f := map[string]string{
		"jenis":     r.URL.Query().Get("jenis"),
		"pihak":     r.URL.Query().Get("pihak"),
		"tanggal":   a.today().Format("2006-01-02"),
		"mata_uang": a.base,
	}
	if f["jenis"] != "debt_in" && f["jenis"] != "loan_out" {
		f["jenis"] = "debt_in"
	}
	a.renderDebtForm(w, r, f, "")
}

func (a *App) renderDebtForm(w http.ResponseWriter, r *http.Request, f map[string]string, errMsg string) {
	ctx := r.Context()
	wallets, err := a.store.Wallets(ctx, family(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	parties, err := a.store.Parties(ctx, family(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if errMsg != "" {
		w.WriteHeader(http.StatusUnprocessableEntity)
	}
	a.render(w, r, "hutang_form.html", map[string]any{
		"Title": "Catat " + map[string]string{"debt_in": "Hutang", "loan_out": "Piutang"}[f["jenis"]],
		"Nav":   "hutang", "Back": "/hutang",
		"Form": f, "Error": errMsg, "Kinds": debtKinds,
		"Wallets": viewWallets(wallets), "Parties": parties, "Currencies": Currencies,
	})
}

// readDebt memvalidasi form hutang piutang, termasuk kasus tanpa dompet.
func (a *App) readDebt(r *http.Request, kinds map[string]bool) (Tx, map[string]string, error) {
	ctx := r.Context()
	f := map[string]string{
		"jenis":     r.FormValue("jenis"),
		"pihak":     strings.TrimSpace(r.FormValue("pihak")),
		"dompet":    r.FormValue("dompet"),
		"mata_uang": r.FormValue("mata_uang"),
		"nominal":   r.FormValue("nominal"),
		"tanggal":   r.FormValue("tanggal"),
		"catatan":   strings.TrimSpace(r.FormValue("catatan")),
	}
	t := Tx{Kind: f["jenis"], Note: f["catatan"]}

	if !kinds[t.Kind] {
		return t, f, errors.New("Jenis catatan tidak dikenal.")
	}
	tanggal, err := time.ParseInLocation("2006-01-02", f["tanggal"], a.loc)
	if err != nil {
		return t, f, errors.New("Tanggal tidak valid.")
	}
	t.Date = tanggal

	if len(f["pihak"]) < 2 {
		return t, f, errors.New("Nama pihak minimal 2 karakter.")
	}
	partyID, err := a.store.EnsureParty(ctx, family(r), f["pihak"])
	if err != nil {
		return t, f, errors.New("Gagal menyimpan pihak.")
	}
	t.PartyID = partyID
	t.PartyName = f["pihak"]

	// Tanpa dompet, mata uangnya harus dipilih sendiri; dengan dompet, ia
	// mengikuti dompetnya supaya tidak ada dua sumber kebenaran.
	walletID, _ := strconv.ParseInt(f["dompet"], 10, 64)
	if walletID == 0 {
		if !knownCurrency(f["mata_uang"]) {
			return t, f, errors.New("Pilih mata uang untuk catatan tanpa dompet.")
		}
		t.WalletCur = f["mata_uang"]
	} else {
		wl, err := a.store.Wallet(ctx, family(r), walletID)
		if err != nil {
			return t, f, errors.New("Dompet tidak valid.")
		}
		t.WalletID = wl.ID
		t.WalletCur = wl.Currency
	}

	amount, err := ParseAmount(f["nominal"], t.WalletCur)
	if err != nil || amount <= 0 {
		return t, f, errors.New("Nominal harus lebih dari nol.")
	}
	t.AmountMinor = amount
	return t, f, nil
}

func (a *App) debtCreate(w http.ResponseWriter, r *http.Request) {
	t, f, err := a.readDebt(r, map[string]bool{"debt_in": true, "loan_out": true})
	if err != nil {
		a.renderDebtForm(w, r, f, err.Error())
		return
	}
	// Memberi pinjaman mengeluarkan uang dari dompet, jadi saldonya harus cukup.
	// Menerima pinjaman menambah saldo, tidak perlu diperiksa.
	if t.Kind == "loan_out" && t.HasWallet() {
		if err := a.checkBalance(r, t, 0); err != nil {
			a.renderDebtForm(w, r, f, err.Error())
			return
		}
	}
	if _, err := a.store.CreateTx(r.Context(), family(r), t, userFrom(r.Context()).ID); err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/hutang/pihak/"+strconv.FormatInt(t.PartyID, 10), http.StatusSeeOther)
}

// ---------- pembayaran per pihak ----------

func (a *App) payForm(w http.ResponseWriter, r *http.Request) {
	p, err := a.store.Party(r.Context(), family(r), pathID(r))
	if errors.Is(err, ErrNotFound) {
		a.notFound(w)
		return
	} else if err != nil {
		a.fail(w, r, err)
		return
	}
	arah := r.URL.Query().Get("arah")
	if _, ok := arahPembayaran[arah]; !ok {
		arah = "hutang"
	}
	f := map[string]string{
		"arah":      arah,
		"tanggal":   a.today().Format("2006-01-02"),
		"mata_uang": a.base,
		"catatan":   "",
	}
	// Isi nominal dengan sisa saldo arah itu: pelunasan penuh adalah yang
	// paling sering, dan angkanya tetap bisa dikurangi untuk cicilan.
	for _, b := range p.Saldo {
		sisa := b.HutangMinor
		if arah == "piutang" {
			sisa = b.PiutangMinor
		}
		if sisa > 0 {
			f["mata_uang"] = b.Currency
			f["nominal"] = FormatPlain(sisa, b.Currency)
			break
		}
	}
	a.renderPayForm(w, r, p, f, "")
}

func (a *App) renderPayForm(w http.ResponseWriter, r *http.Request, p Party, f map[string]string, errMsg string) {
	wallets, err := a.store.Wallets(r.Context(), family(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	judul := "Bayar Hutang ke " + p.Name
	if f["arah"] == "piutang" {
		judul = "Terima Piutang dari " + p.Name
	}
	if errMsg != "" {
		w.WriteHeader(http.StatusUnprocessableEntity)
	}
	a.render(w, r, "bayar_form.html", map[string]any{
		"Title": judul, "Nav": "hutang",
		"Back":  "/hutang/pihak/" + strconv.FormatInt(p.ID, 10),
		"Party": viewParty(p), "Form": f, "Error": errMsg,
		"Wallets": viewWallets(wallets), "Currencies": Currencies,
	})
}

func (a *App) payCreate(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	p, err := a.store.Party(r.Context(), family(r), id)
	if errors.Is(err, ErrNotFound) {
		a.notFound(w)
		return
	} else if err != nil {
		a.fail(w, r, err)
		return
	}

	arah := r.FormValue("arah")
	kind, ok := arahPembayaran[arah]
	if !ok {
		a.renderPayForm(w, r, p, map[string]string{"arah": "hutang"}, "Arah pembayaran tidak dikenal.")
		return
	}

	// readDebt membaca kolom yang sama, jadi jenisnya dititipkan lewat form.
	r.Form.Set("jenis", kind)
	r.Form.Set("pihak", p.Name)
	t, f, err := a.readDebt(r, map[string]bool{kind: true})
	f["arah"] = arah
	if err != nil {
		a.renderPayForm(w, r, p, f, err.Error())
		return
	}

	// Membayar hutang mengeluarkan uang; menerima pelunasan memasukkannya.
	if kind == "debt_pay" && t.HasWallet() {
		if err := a.checkBalance(r, t, 0); err != nil {
			a.renderPayForm(w, r, p, f, err.Error())
			return
		}
	}
	txs, err := paymentTxs(p, t, arah)
	if err != nil {
		a.renderPayForm(w, r, p, f, err.Error())
		return
	}

	if _, err := a.store.CreateTxs(r.Context(), family(r), txs, userFrom(r.Context()).ID); err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/hutang/pihak/"+strconv.FormatInt(p.ID, 10), http.StatusSeeOther)
}

// paymentTxs menambah piutang sebesar kelebihan penerimaan sebelum melunasinya.
// Penyesuaiannya tidak memakai dompet: seluruh uang memang diterima, sedangkan
// baris tambahan ini hanya menjaga saldo piutang berakhir tepat nol.
func paymentTxs(p Party, t Tx, arah string) ([]Tx, error) {
	for _, b := range p.Saldo {
		if b.Currency != t.WalletCur {
			continue
		}
		sisa := b.HutangMinor
		label := "Hutang"
		if arah == "piutang" {
			sisa, label = b.PiutangMinor, "Piutang"
		}
		if t.AmountMinor <= sisa {
			return []Tx{t}, nil
		}
		if arah != "piutang" {
			return nil, errors.New(label + " ke " + p.Name + " tinggal " +
				Format(sisa, b.Currency) + ", tidak bisa dibayar lebih dari itu.")
		}
		adjustment := t
		adjustment.Kind = "loan_out"
		adjustment.WalletID = 0
		adjustment.WalletName = ""
		adjustment.AmountMinor = t.AmountMinor - sisa
		adjustment.Note = "Penyesuaian kelebihan pembayaran piutang"
		return []Tx{adjustment, t}, nil
	}
	return nil, errors.New("Tidak ada catatan dalam mata uang itu untuk pihak ini.")
}

// ---------- kelola pihak ----------

func (a *App) partyUpdate(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	name := strings.TrimSpace(r.FormValue("nama"))
	if len(name) < 2 {
		http.Redirect(w, r, "/hutang/pihak/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
		return
	}
	if err := a.store.UpdateParty(r.Context(), family(r), id, name,
		strings.TrimSpace(r.FormValue("catatan"))); errors.Is(err, ErrNotFound) {
		a.notFound(w)
		return
	} else if err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/hutang/pihak/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (a *App) partyDelete(w http.ResponseWriter, r *http.Request) {
	err := a.store.DeleteParty(r.Context(), family(r), pathID(r))
	if errors.Is(err, ErrNotFound) {
		a.notFound(w)
		return
	}
	if err != nil {
		parties, lerr := a.store.Parties(r.Context(), family(r))
		if lerr != nil {
			a.fail(w, r, lerr)
			return
		}
		rates, _ := a.rates(r.Context(), family(r))
		w.WriteHeader(http.StatusConflict)
		a.render(w, r, "hutang.html", map[string]any{
			"Title": "Hutang & Piutang", "Nav": "hutang",
			"Summary": summarizeDebts(parties, rates, a.base), "Error": err.Error(),
		})
		return
	}
	http.Redirect(w, r, "/hutang", http.StatusSeeOther)
}
