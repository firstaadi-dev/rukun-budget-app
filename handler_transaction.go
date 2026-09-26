package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ---------- transaksi ----------

var kindLabel = map[string]string{
	"expense": "Pengeluaran", "income": "Pemasukan", "transfer": "Transfer",
	"debt_in": "Hutang", "debt_pay": "Bayar Hutang",
	"loan_out": "Piutang", "loan_in": "Terima Piutang",
	"invest_buy":  "Beli Investasi",
	"invest_sell": "Jual Investasi",
}

var txFilters = []struct{ Value, Label string }{
	{"", "Semua"}, {"expense", "Keluar"}, {"income", "Masuk"},
	{"transfer", "Transfer"}, {"hutang", "Hutang"}, {"invest_buy", "Investasi"},
}

// txListLimit membatasi satu halaman daftar transaksi. Sebulan yang menembus
// angka ini praktis tidak ada, tapi "Seluruh waktu" akan menembusnya cepat —
// dan yang penting bukan batasnya, melainkan bahwa pemotongannya dikatakan,
// bukan diam-diam seperti sebelumnya.
const txListLimit = 200

// TxFilterOpt: satu pilihan jenis di atas daftar, lengkap dengan URL-nya.
type TxFilterOpt struct {
	Value, Label, URL string
	On                bool
}

// txParams: penyaring yang boleh menempel di URL daftar transaksi. Didaftar
// tertutup supaya parameter asing tidak ikut terbawa dari satu tautan ke
// tautan berikutnya.
var txParams = []string{"jenis", "kategori", "dompet", "periode", "dari", "sampai", "cari", "cursor"}

// txWaktu: penyaring waktu, yang ketiganya menjawab pertanyaan yang sama lewat
// jalan berbeda. Mengganti salah satunya harus melepas dua sisanya — kalau
// tidak, rentang khusus yang masih menempel akan mengalahkan bulan yang baru
// saja dipilih, dan tautannya seolah tidak melakukan apa-apa.
var txWaktu = map[string]bool{"periode": true, "dari": true, "sampai": true}

// txURL merakit URL daftar transaksi dengan satu penyaring diganti. Halaman ini
// punya penyaring yang saling menumpuk, dan menautkan salah satunya tanpa
// membawa yang lain akan diam-diam melepas penyaring yang sedang dipakai.
func txURL(q url.Values, key, val string) string {
	out := url.Values{}
	for _, k := range txParams {
		if k == key || (txWaktu[key] && txWaktu[k]) || (k == "cursor" && key != "cursor") {
			continue
		}
		if v := q.Get(k); v != "" {
			out.Set(k, v)
		}
	}
	if val != "" {
		out.Set(key, val)
	}
	if len(out) == 0 {
		return "/transaksi"
	}
	return "/transaksi?" + out.Encode()
}

func txFilterOptions(q url.Values, aktif string) []TxFilterOpt {
	out := make([]TxFilterOpt, len(txFilters))
	for i, f := range txFilters {
		out[i] = TxFilterOpt{Value: f.Value, Label: f.Label,
			URL: txURL(q, "jenis", f.Value), On: f.Value == aktif}
	}
	return out
}

func (a *App) txList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	filter := q.Get("jenis")
	if _, ok := kindLabel[filter]; !ok && filter != "hutang" {
		filter = ""
	}
	kategori := q.Get("kategori")
	cari := strings.TrimSpace(q.Get("cari"))
	dompetID, _ := strconv.ParseInt(q.Get("dompet"), 10, 64)
	if dompetID < 0 {
		dompetID = 0
	}

	// Transfer, hutang piutang, dan pembelian investasi tidak punya kategori:
	// menyaring keduanya sekaligus selalu kosong, jadi memilih kategori melepas
	// filter jenis itu.
	if kategori != "" && (filter == "transfer" || filter == "hutang" || filter == "invest_buy") {
		filter = ""
	}
	// Nilai yang sudah dibetulkan dikembalikan ke q, karena dari sanalah semua
	// tautan di halaman ini dirakit.
	q.Set("jenis", filter)
	q.Set("cari", cari)
	if dompetID > 0 {
		q.Set("dompet", strconv.FormatInt(dompetID, 10))
	} else {
		q.Del("dompet")
	}

	periode := bacaPeriode(q.Get("periode"), q.Get("dari"), q.Get("sampai"), a.today())
	var beforeDate time.Time
	var beforeID int64
	if parts := strings.Split(q.Get("cursor"), ":"); len(parts) == 2 {
		beforeDate, _ = time.Parse(formatTanggal, parts[0])
		beforeID, _ = strconv.ParseInt(parts[1], 10, 64)
	}
	if beforeDate.IsZero() || beforeID <= 0 {
		q.Del("cursor")
	}
	// Penyaring waktu ikut dirapikan sebelum tautannya dirakit. Yang menang
	// dipasang dalam bentuk yang sudah dibetulkan — tanggal tertukar sudah
	// dibalik di bacaPeriode — dan yang kalah dibuang supaya tidak diam-diam
	// terbawa lalu mengalahkan pilihan berikutnya.
	for k := range txWaktu {
		q.Del(k)
	}
	switch {
	case periode.Rentang:
		q.Set("dari", periode.Dari)
		q.Set("sampai", periode.Sampai)
	case periode.Semua:
		q.Set("periode", periodeSemua)
	case !periode.BulanIni:
		// Bulan berjalan sengaja tidak dipasang: tanpa parameter, tautannya
		// tetap menunjuk "bulan ini" juga setelah tanggal berganti bulan.
		q.Set("periode", periode.Nilai)
	}

	// Diminta satu lebih banyak dari batasnya: kelebihan satu baris itulah yang
	// memberi tahu bahwa daftarnya terpotong. Tanpa itu, 200 hasil pas tidak
	// bisa dibedakan dari 200 hasil pertama dari seribu.
	txs, err := a.store.Transactions(ctx, family(r), TxFilter{
		Kinds: kindsForFilter(filter), Category: kategori, Cari: cari,
		WalletID: dompetID, From: periode.From, To: periode.To, Limit: txListLimit + 1,
		BeforeDate: beforeDate, BeforeID: beforeID,
	})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	terpotong := len(txs) > txListLimit
	if terpotong {
		txs = txs[:txListLimit]
	}
	nextPage := ""
	if terpotong {
		last := txs[len(txs)-1]
		nextPage = txURL(q, "cursor", last.Date.Format(formatTanggal)+":"+strconv.FormatInt(last.ID, 10))
	}
	q.Del("cursor")

	cats, err := a.store.Categories(ctx, family(r), "")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	wallets, err := a.store.Wallets(ctx, family(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.render(w, r, "transaksi.html", map[string]any{
		"Title": "Transaksi", "Nav": "transaksi",
		"Filter": filter, "Filters": txFilterOptions(q, filter),
		"Kategori": kategori, "Categories": cats,
		"DompetID": dompetID, "Wallets": wallets,
		"Cari": cari, "Periode": periode,
		"URLPrev":          txURL(q, "periode", periode.Prev),
		"URLNext":          txURL(q, "periode", periode.Next),
		"URLSemua":         txURL(q, "periode", periodeSemua),
		"URLBulanIni":      txURL(q, "periode", ""),
		"URLTanpaCari":     txURL(q, "cari", ""),
		"URLTanpaKategori": txURL(q, "kategori", ""),
		"Terpotong":        terpotong, "Batas": txListLimit, "NextPage": nextPage,
		"ExportQuery": q.Encode(),
		"Groups":      groupTxs(viewTxs(txs, a.today())),
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
	case "invest_buy":
		return []string{"invest_buy", "invest_sell"}
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
		"Waktu":       tanggalPendek(t.Date) + ", " + t.CreatedAt.In(a.loc).Format("15:04"),
		"InvestTrade": t.Kind == "invest_buy" || t.Kind == "invest_sell",
		"QtyLabel":    FormatQty(t.QtyE8),
	}
	if t.Kind == "invest_sell" {
		data["CostBasis"] = Format(t.CostBasisMinor, t.WalletCur)
		data["Realized"] = Format(t.AmountMinor-t.CostBasisMinor, t.WalletCur)
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
		if t.Kind == "invest_buy" || t.Kind == "invest_sell" {
			http.Redirect(w, r, "/investasi/"+strconv.FormatInt(t.InvestmentID, 10), http.StatusSeeOther)
			return
		}
	} else {
		t.Kind = r.URL.Query().Get("jenis")
		if t.Kind != "income" && t.Kind != "transfer" {
			t.Kind = "expense"
		}
		t.Date = a.today()
	}

	f := map[string]string{
		"tanggal":  t.Date.Format("2006-01-02"),
		"catatan":  t.Note,
		"kategori": t.Category,
		// Mode hanya berarti saat mencatat baru: yang sudah tersimpan adalah
		// transaksi biasa satuan, apa pun asalnya.
		"mode": bacaMode(r.URL.Query().Get("mode")),
	}
	if id != 0 {
		f["mode"] = ""
	} else {
		f["kembali"] = localReturnURL(r, r.Referer())
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
	if id == 0 && f["kembali"] != "" {
		action += "&kembali=" + url.QueryEscape(f["kembali"])
	}
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
		"Modes":   modeCicilan, "ModeLabel": modeLabel(f["mode"]),
		"ModeHint": modeHint(f["mode"]), "CicilanMaks": cicilanMaks,
	}
	page := "transaksi_form.html"

	// Dibaca ulang di sini, bukan dititipkan lewat f: form yang tergambar ulang
	// setelah isian ditolak harus tetap menawarkan edit massal, dan f saat itu
	// datang dari isian user, bukan dari yang tersimpan.
	if id != 0 {
		if old, err := a.store.Transaction(ctx, family(r), id); err == nil && old.InSeries() {
			data["Series"] = old
		}
	}

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
		"cicilan":          strings.TrimSpace(r.FormValue("cicilan")),
		"mode":             bacaMode(r.FormValue("mode")),
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
	// Bertanggal maju: belum menyentuh saldo hari ini, jadi belum ada yang bisa
	// ditembusnya. Pemeriksaannya menyusul sendiri lewat transaksi berikutnya
	// yang dicatat setelah tanggalnya lewat.
	if t.Date.After(a.today()) {
		return nil
	}

	after := wl.BalanceMinor - t.AmountMinor
	if excludeTxID != 0 {
		if old, err := a.store.Transaction(r.Context(), family(r), excludeTxID); err == nil && old.WalletID == wl.ID {
			after += old.AmountMinor
		}
	}

	if wl.IsCredit() {
		if wl.HasLimit() && wl.CicilanMendatangMinor-after > wl.LimitMinor {
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
	if kind != "income" && kind != "transfer" {
		kind = "expense"
	}
	t, f, err := a.readTx(r, kind)
	if err != nil {
		f["kembali"] = localReturnURL(r, r.URL.Query().Get("kembali"))
		a.renderTxForm(w, r, kind, 0, f, err.Error())
		return
	}
	n, bagi, err := readCicilan(r, t)
	if err != nil {
		f["kembali"] = localReturnURL(r, r.URL.Query().Get("kembali"))
		a.renderTxForm(w, r, kind, 0, f, err.Error())
		return
	}
	// Yang diperiksa cuma angsuran yang sudah jatuh: saldo hari ini tidak
	// menghitung yang bertanggal maju, jadi tidak ada yang bisa ditembus olehnya.
	jadwal := jadwalCicilan(t, n, bagi)
	jatuh := t
	jatuh.AmountMinor = totalSampai(jadwal, a.today())
	if err := a.checkBalance(r, jatuh, 0); err != nil {
		f["kembali"] = localReturnURL(r, r.URL.Query().Get("kembali"))
		a.renderTxForm(w, r, kind, 0, f, err.Error())
		return
	}
	_, err = a.store.CreateTxs(r.Context(), family(r), jadwal, userFrom(r.Context()).ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	to := localReturnURL(r, r.URL.Query().Get("kembali"))
	if to == "" {
		to = "/transaksi"
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// localReturnURL hanya menerima URL internal agar parameter kembali tidak
// berubah menjadi open redirect.
func localReturnURL(r *http.Request, raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Path == "" || strings.HasPrefix(u.Path, "//") ||
		(u.Host != "" && u.Host != r.Host) {
		return ""
	}
	return u.RequestURI()
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
	if old.Kind == "invest_buy" || old.Kind == "invest_sell" {
		http.Error(w, "Transaksi investasi hanya dapat dibatalkan, lalu dicatat ulang.", http.StatusBadRequest)
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
	// Seluruh rangkaian lebih dulu, baru barisnya sendiri: keduanya menulis
	// kategori dan catatan yang sama, jadi urutan ini membuat baris ini menang
	// kalau yang kedua ternyata gagal.
	if old.InSeries() && r.FormValue("seluruh") == "1" {
		if err := a.store.UpdateSeries(r.Context(), family(r), old.SeriesID,
			t.Category, t.Note); err != nil {
			a.fail(w, r, err)
			return
		}
	}
	if err := a.store.UpdateTx(r.Context(), family(r), t); err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/transaksi/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (a *App) txDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	old, err := a.store.Transaction(ctx, family(r), pathID(r))
	if errors.Is(err, ErrNotFound) {
		a.notFound(w)
		return
	} else if err != nil {
		a.fail(w, r, err)
		return
	}

	if old.Kind == "invest_buy" || old.Kind == "invest_sell" {
		err = a.store.DeleteInvestmentTrade(ctx, family(r), old)
	} else if old.InSeries() && r.FormValue("seluruh") == "1" {
		err = a.store.DeleteSeries(ctx, family(r), old.SeriesID)
	} else {
		err = a.store.DeleteTx(ctx, family(r), old.ID)
	}
	if errors.Is(err, ErrNotFound) {
		a.notFound(w)
		return
	} else if errors.Is(err, ErrTradeLocked) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	} else if err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/transaksi", http.StatusSeeOther)
}
