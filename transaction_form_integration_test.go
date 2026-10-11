package main

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func transactionFormPost(t *testing.T, path string, values url.Values) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, entries := range values {
		for _, value := range entries {
			if err := writer.WriteField(key, value); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, path, &body)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	return r
}

func TestTransactionFormsAndWalletlessDebtEdits(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	s := &Store{db: pool, loc: time.UTC}
	uid := fmt.Sprintf("tx-form-%d", time.Now().UnixNano())
	userID, err := s.CreateFirebaseUser(ctx, uid, uid+"@example.test", "Tester")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID)
	f, err := s.CreateFamilyForUser(ctx, userID, "Form Test", newSignupCode())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `DELETE FROM families WHERE id=$1`, f.ID)
	u, err := s.FirebaseUser(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	partyID, err := s.EnsureParty(ctx, f.ID, "Pihak Test")
	if err != nil {
		t.Fatal(err)
	}
	app := &App{store: s, pages: parsePages(), loc: time.UTC, base: "IDR", rateSrc: &rateSource{}}
	request := func(method, path string, id int64, values url.Values) *http.Request {
		r := httptest.NewRequest(method, path, nil)
		if method == http.MethodPost {
			r = transactionFormPost(t, path, values)
		}
		if id != 0 {
			r.SetPathValue("id", strconv.FormatInt(id, 10))
		}
		return r.WithContext(context.WithValue(r.Context(), ctxKey{}, u))
	}
	for kind := range kindHutangPiutang {
		for _, currency := range []string{"IDR", "USD"} {
			t.Run(kind+"_"+currency, func(t *testing.T) {
				id, err := s.CreateTx(ctx, f.ID, Tx{Kind: kind, Date: s.today(), WalletCur: currency, AmountMinor: 100, PartyID: partyID}, u.ID)
				if err != nil {
					t.Fatal(err)
				}
				defer s.DeleteTx(ctx, f.ID, id)
				path := "/transaksi/" + strconv.FormatInt(id, 10) + "/ubah"
				w := httptest.NewRecorder()
				app.txForm(w, request(http.MethodGet, path, id, nil))
				if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `data-name="Tanpa dompet"`) || !strings.Contains(w.Body.String(), `selected>`+currency+` —`) {
					t.Fatalf("form without any wallets: status %d, body %s", w.Code, w.Body.String())
				}
				values := url.Values{"tanggal": {s.today().Format(formatTanggal)}, "dompet": {""}, "mata_uang": {currency}, "nominal": {"2,50"}, "catatan": {"Diperbarui"}, "party_id": {"999999"}}
				for _, invalid := range []struct{ field, value, message string }{
					{"mata_uang", "", "Pilih mata uang"},
					{"mata_uang", "XYZ", "Pilih mata uang"},
					{"dompet", "rusak", "Dompet tidak valid"},
					{"dompet", "-1", "Dompet tidak valid"},
					{"nominal", "0", "Nominal harus lebih dari nol"},
				} {
					previous := values.Get(invalid.field)
					values.Set(invalid.field, invalid.value)
					w = httptest.NewRecorder()
					app.txUpdate(w, request(http.MethodPost, path, id, values))
					if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), invalid.message) {
						t.Fatalf("invalid %s: status %d, body %s", invalid.field, w.Code, w.Body.String())
					}
					values.Set(invalid.field, previous)
				}
				w = httptest.NewRecorder()
				app.txUpdate(w, request(http.MethodPost, path, id, values))
				if w.Code != http.StatusSeeOther {
					t.Fatalf("edit without wallet: status %d, body %s", w.Code, w.Body.String())
				}
				updated, err := s.Transaction(ctx, f.ID, id)
				if err != nil || updated.HasWallet() || updated.WalletCur != currency || updated.AmountMinor != 250 || updated.PartyID != partyID || updated.Kind != kind || updated.Category != "" || updated.Note != "Diperbarui" {
					t.Fatalf("updated walletless transaction: %+v, %v", updated, err)
				}
			})
		}
	}
	for _, kind := range []string{"expense", "income", "transfer"} {
		values := url.Values{"tanggal": {s.today().Format(formatTanggal)}, "dompet": {""}, "mata_uang": {"IDR"}, "nominal": {"2,50"}}
		if _, _, err := app.readTx(request(http.MethodPost, "/transaksi/baru", 0, values), kind); err == nil || err.Error() != "Dompet tidak valid." {
			t.Errorf("%s tanpa dompet: %v", kind, err)
		}
	}
	fromID, err := s.CreateWallet(ctx, f.ID, Wallet{Name: "Sumber", Type: "cash", Currency: "IDR", InitialMinor: 1_000_000})
	if err != nil {
		t.Fatal(err)
	}
	toID, err := s.CreateWallet(ctx, f.ID, Wallet{Name: "Tujuan", Type: "cash", Currency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	for kind := range kindHutangPiutang {
		t.Run("change_wallet_"+kind, func(t *testing.T) {
			before, err := s.Wallet(ctx, f.ID, fromID)
			if err != nil {
				t.Fatal(err)
			}
			id, err := s.CreateTx(ctx, f.ID, Tx{Kind: kind, Date: s.today(), WalletID: fromID, WalletCur: "IDR", AmountMinor: 100, PartyID: partyID}, u.ID)
			if err != nil {
				t.Fatal(err)
			}
			defer s.DeleteTx(ctx, f.ID, id)
			path := "/transaksi/" + strconv.FormatInt(id, 10) + "/ubah"
			values := url.Values{"tanggal": {s.today().Format(formatTanggal)}, "dompet": {""}, "mata_uang": {"USD"}, "nominal": {"2,50"}}
			for _, walletID := range []int64{0, fromID} {
				values.Set("dompet", strconv.FormatInt(walletID, 10))
				w := httptest.NewRecorder()
				app.txUpdate(w, request(http.MethodPost, path, id, values))
				if w.Code != http.StatusSeeOther {
					t.Fatalf("change wallet to %d: status %d, body %s", walletID, w.Code, w.Body.String())
				}
				updated, err := s.Transaction(ctx, f.ID, id)
				currency, balance := "USD", before.BalanceMinor
				if walletID != 0 {
					currency = "IDR"
					if kindMenambahSaldo[kind] {
						balance += 250
					} else {
						balance -= 250
					}
				}
				if err != nil || updated.WalletID != walletID || updated.WalletCur != currency || updated.AmountMinor != 250 || updated.PartyID != partyID {
					t.Fatalf("changed wallet transaction: %+v, %v", updated, err)
				}
				wallet, err := s.Wallet(ctx, f.ID, fromID)
				if err != nil || wallet.BalanceMinor != balance {
					t.Fatalf("changed wallet balance: %+v, want %d, %v", wallet, balance, err)
				}
			}
		})
	}
	t.Run("transfer", func(t *testing.T) {
		path := "/transaksi/baru?jenis=transfer&ke=" + strconv.FormatInt(toID, 10) + "&nominal=100"
		w := httptest.NewRecorder()
		app.txForm(w, request(http.MethodGet, path, 0, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("transfer form: status %d, body %s", w.Code, w.Body.String())
		}
		for _, marker := range []string{`aria-label="Jenis transaksi"`, `class="seg-opt on" href="/transaksi/baru?jenis=transfer"`, `name="nominal_keluar"`, `value="100"`, `data-live-amount`, `Simpan Transaksi`} {
			if !strings.Contains(w.Body.String(), marker) {
				t.Errorf("transfer form missing %s", marker)
			}
		}
		values := url.Values{"tanggal": {s.today().Format(formatTanggal)}, "dompet": {strconv.FormatInt(fromID, 10)}, "ke_dompet": {strconv.FormatInt(toID, 10)}, "nominal_keluar": {"500"}, "nominal_diterima": {""}, "biaya_admin": {"0"}}
		w = httptest.NewRecorder()
		app.txCreate(w, request(http.MethodPost, path, 0, values))
		if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), `aria-label="Jenis transaksi"`) {
			t.Fatalf("invalid transfer lost shared form: status %d, body %s", w.Code, w.Body.String())
		}
		values.Set("nominal_diterima", "0,03")
		w = httptest.NewRecorder()
		app.txCreate(w, request(http.MethodPost, path, 0, values))
		if w.Code != http.StatusSeeOther {
			t.Fatalf("save transfer: status %d, body %s", w.Code, w.Body.String())
		}
		transfers, err := s.Transactions(ctx, f.ID, TxFilter{Kinds: []string{"transfer"}})
		if err != nil || len(transfers) != 1 || transfers[0].WalletID != fromID || transfers[0].ToWalletID != toID || transfers[0].AmountMinor != 50_000 || transfers[0].AmountInMino != 3 {
			t.Fatalf("saved transfer: %+v, %v", transfers, err)
		}
	})
}
