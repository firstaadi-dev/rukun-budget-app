package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

func TestAccountFamilyFlow(t *testing.T) {
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
	store := &Store{db: pool, loc: time.UTC}
	a := &App{store: store, pages: parsePages(), loc: time.UTC, base: "IDR"}
	h := a.routes()
	post := func(path string, values url.Values, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "http://rukun.test"+path, strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", "http://rukun.test")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	username := fmt.Sprintf("flow%d", time.Now().UnixNano())
	defer pool.Exec(ctx, `DELETE FROM users WHERE username IN ($1, $2, $3)`, username, username+"b", username+"c")
	first := post("/daftar", url.Values{"username": {username}, "nama": {"Nia"}, "sandi": {"rahasia-aman-123"}}, nil)
	if first.Code != http.StatusSeeOther || first.Header().Get("Location") != "/mulai" {
		t.Fatalf("signup: %d %s", first.Code, first.Body.String())
	}
	var firstCookie *http.Cookie
	for _, c := range first.Result().Cookies() {
		if c.Name == sessionCookie {
			firstCookie = c
		}
	}
	if firstCookie == nil {
		t.Fatal("session cookie missing")
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "http://rukun.test/", nil)
	r.AddCookie(firstCookie)
	h.ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/mulai" {
		t.Fatalf("unlinked root: %d %s", w.Code, w.Header().Get("Location"))
	}
	created := post("/mulai/buat", url.Values{"nama": {"Keluarga Flow"}}, firstCookie)
	if created.Code != http.StatusSeeOther || created.Header().Get("Location") != "/" {
		t.Fatalf("create family: %d %s", created.Code, created.Body.String())
	}
	u, _, err := store.UserByUsername(ctx, username)
	if err != nil {
		t.Fatal(err)
	}
	f, err := store.FamilyByID(ctx, u.FamilyID)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `DELETE FROM families WHERE id = $1`, f.ID)
	if !u.Kepala {
		t.Fatal("creator is not family head")
	}
	second := post("/daftar", url.Values{"username": {username + "b"}, "nama": {"Nia"}, "sandi": {"rahasia-aman-123"}}, nil)
	if second.Code != http.StatusSeeOther {
		t.Fatalf("second signup: %d %s", second.Code, second.Body.String())
	}
	var secondCookie *http.Cookie
	for _, c := range second.Result().Cookies() {
		if c.Name == sessionCookie {
			secondCookie = c
		}
	}
	invite := httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodGet, "http://rukun.test/mulai?kode="+url.QueryEscape(f.SignupCode), nil)
	h.ServeHTTP(invite, r)
	if invite.Code != http.StatusSeeOther || invite.Header().Get("Location") != "/daftar" {
		t.Fatalf("anonymous invite: %d %s", invite.Code, invite.Header().Get("Location"))
	}
	var inviteCookie *http.Cookie
	for _, c := range invite.Result().Cookies() {
		if c.Name == pendingInviteCookie {
			inviteCookie = c
		}
	}
	if inviteCookie == nil {
		t.Fatal("invite cookie missing")
	}
	start := httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodGet, "http://rukun.test/mulai", nil)
	r.AddCookie(secondCookie)
	r.AddCookie(inviteCookie)
	h.ServeHTTP(start, r)
	if start.Code != http.StatusOK || !strings.Contains(start.Body.String(), `value="`+f.SignupCode+`"`) {
		t.Fatalf("invite not preserved after login: %d", start.Code)
	}
	joined := post("/mulai/gabung", url.Values{"nama": {"Bima"}, "kode": {f.SignupCode}}, secondCookie)
	if joined.Code != http.StatusSeeOther || joined.Header().Get("Location") != "/" {
		t.Fatalf("join: %d %s", joined.Code, joined.Body.String())
	}
	u2, _, err := store.UserByUsername(ctx, username+"b")
	if err != nil || u2.FamilyID != f.ID {
		t.Fatalf("joined user: %+v %v", u2, err)
	}
	if err := store.JoinFamily(ctx, u2.ID, f.SignupCode, "Bima"); !errors.Is(err, ErrAlreadyLinked) {
		t.Fatalf("second join: %v", err)
	}
	if _, err := store.CreateFamilyForUser(ctx, u2.ID, "Another", newSignupCode()); !errors.Is(err, ErrAlreadyLinked) {
		t.Fatalf("second family: %v", err)
	}
	login := post("/masuk", url.Values{"username": {username + "b"}, "sandi": {"rahasia-aman-123"}}, nil)
	if login.Code != http.StatusSeeOther || login.Header().Get("Location") != "/" {
		t.Fatalf("account login: %d %s", login.Code, login.Body.String())
	}
	legacyHash, err := bcrypt.GenerateFromPassword([]byte("rahasia-aman-123"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	var legacyID int64
	if err := pool.QueryRow(ctx, `INSERT INTO users (username, name, password_hash, family_id)
		VALUES ($1, 'Nia lama', $2, $3) RETURNING id`, "legacy-temp-"+username, string(legacyHash), f.ID).Scan(&legacyID); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, legacyID)
	if _, err := pool.Exec(ctx, `UPDATE users SET username = 'rukun:' || id WHERE id = $1`, legacyID); err != nil {
		t.Fatal(err)
	}
	candidates, err := store.LegacyAccounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, candidate := range candidates {
		if candidate.ID == legacyID {
			found = true
		}
	}
	if !found {
		t.Fatal("legacy placeholder missing from admin migration list")
	}
	if updated, err := store.AdminSetLegacyUsername(ctx, legacyID, username+"c"); err != nil || !updated {
		t.Fatalf("admin account migration: updated=%v err=%v", updated, err)
	}
	legacyUser, hash, err := store.UserByUsername(ctx, username+"c")
	if err != nil || legacyUser.FamilyID != f.ID || bcrypt.CompareHashAndPassword([]byte(hash), []byte("rahasia-aman-123")) != nil {
		t.Fatalf("migrated account lost access or family link: user=%+v err=%v", legacyUser, err)
	}
	migratedLogin := post("/masuk", url.Values{"username": {username + "c"}, "sandi": {"rahasia-aman-123"}}, nil)
	if migratedLogin.Code != http.StatusSeeOther || migratedLogin.Header().Get("Location") != "/" {
		t.Fatalf("migrated account login: %d %s", migratedLogin.Code, migratedLogin.Body.String())
	}
	settings := httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodGet, "http://rukun.test/pengaturan", nil)
	r.AddCookie(firstCookie)
	h.ServeHTTP(settings, r)
	if settings.Code != http.StatusOK || !strings.Contains(settings.Body.String(), f.SignupCode) {
		t.Fatalf("invite settings: %d", settings.Code)
	}
	denied := post("/pengaturan/kode", url.Values{"sandi": {"rahasia-aman-123"}}, secondCookie)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("member rotated code: %d", denied.Code)
	}
	wrongPassword := post("/pengaturan/kode", url.Values{"sandi": {"wrong-password"}}, firstCookie)
	if wrongPassword.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password rotated code: %d", wrongPassword.Code)
	}
	thirdID, err := store.CreateUser(ctx, username+"c", "Cici", "unused-hash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE families SET signup_code_expires_at = now() - interval '1 second' WHERE id = $1`, f.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.JoinFamily(ctx, thirdID, f.SignupCode, "Cici"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired code accepted: %v", err)
	}
	rotated := post("/pengaturan/kode", url.Values{"sandi": {"rahasia-aman-123"}}, firstCookie)
	if rotated.Code != http.StatusSeeOther {
		t.Fatalf("rotate code: %d %s", rotated.Code, rotated.Body.String())
	}
	newFamily, err := store.FamilyByID(ctx, f.ID)
	if err != nil || newFamily.SignupCode == f.SignupCode {
		t.Fatalf("code not rotated: %+v %v", newFamily, err)
	}
	if _, err := store.FamilyByCode(ctx, f.SignupCode); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old code still active: %v", err)
	}
	if err := store.JoinFamily(ctx, thirdID, newFamily.SignupCode, "Cici"); err != nil {
		t.Fatalf("renewed code rejected: %v", err)
	}
	categories, err := store.Categories(ctx, f.ID, "expense")
	if err != nil {
		t.Fatal(err)
	}
	var belanjaID int64
	for _, c := range categories {
		if c.Name == "Belanja" {
			belanjaID = c.ID
		}
	}
	if belanjaID == 0 {
		t.Fatal("default category missing")
	}
	budget := post(fmt.Sprintf("/kategori/%d/anggaran", belanjaID), url.Values{"anggaran": {"50.000"}}, firstCookie)
	if budget.Code != http.StatusSeeOther {
		t.Fatalf("budget: %d %s", budget.Code, budget.Body.String())
	}
	categories, err = store.Categories(ctx, f.ID, "expense")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range categories {
		if c.ID == belanjaID && c.BudgetMinor != 5_000_000 {
			t.Fatalf("budget stored: %d", c.BudgetMinor)
		}
	}
	walletID, err := store.CreateWallet(ctx, f.ID, Wallet{Name: "Tunai", Type: "cash", Currency: "IDR", InitialMinor: 10_000_000})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateTx(ctx, f.ID, Tx{Kind: "expense", Date: store.today(), WalletID: walletID, WalletCur: "IDR", AmountMinor: 2_000_000, Category: "Belanja", Note: "=HYPERLINK(\"https://example.test\")"}, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateTx(ctx, f.ID, Tx{Kind: "expense", Date: store.today().AddDate(0, 0, 1), WalletID: walletID, WalletCur: "IDR", AmountMinor: 1_000_000, Category: "Belanja"}, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	currentSpend, err := store.CategorySpending(ctx, f.ID, store.today(), store.today().AddDate(0, 0, 2))
	if err != nil || len(currentSpend) != 1 || currentSpend[0].Minor != 2_000_000 {
		t.Fatalf("future expense counted: %+v %v", currentSpend, err)
	}
	for _, tc := range []struct{ path, want string }{
		{"/", "Sisa Rp30.000"},
		{"/laporan", "Rp20.000"},
		{"/transaksi/ekspor?periode=semua&kategori=Belanja", "'=HYPERLINK"},
	} {
		page := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "http://rukun.test"+tc.path, nil)
		r.AddCookie(firstCookie)
		h.ServeHTTP(page, r)
		if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), tc.want) {
			t.Fatalf("%s: %d, missing %q, body: %s", tc.path, page.Code, tc.want, page.Body.String())
		}
	}
}
