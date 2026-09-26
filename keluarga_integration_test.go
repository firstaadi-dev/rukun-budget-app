package main

import (
	"context"
	"encoding/json"
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
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	firstUID, secondUID, thirdUID := "flow-a-"+suffix, "flow-b-"+suffix, "flow-c-"+suffix
	firstEmail, secondEmail, thirdEmail := "flow-a-"+suffix+"@example.test", "flow-b-"+suffix+"@example.test", "flow-c-"+suffix+"@example.test"
	uidByEmail := map[string]string{firstEmail: firstUID, secondEmail: secondUID}
	emailByUID := map[string]string{firstUID: firstEmail, secondUID: secondEmail}
	firebase := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Email    string `json:"email"`
			Password string `json:"password"`
			IDToken  string `json:"idToken"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
			return
		}
		switch r.URL.Path {
		case "/accounts:signInWithPassword":
			uid := uidByEmail[in.Email]
			if uid == "" || in.Password != "rahasia-aman-123" {
				http.Error(w, `{"error":{"message":"INVALID_LOGIN_CREDENTIALS"}}`, http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(firebaseToken{IDToken: uid, LocalID: uid, Email: in.Email})
		case "/accounts:lookup":
			_ = json.NewEncoder(w).Encode(map[string]any{"users": []firebaseAccount{{LocalID: in.IDToken, Email: emailByUID[in.IDToken], EmailVerified: true}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer firebase.Close()
	a := &App{store: store, pages: parsePages(), loc: time.UTC, base: "IDR", firebase: &firebaseAuth{apiKey: "test", endpoint: firebase.URL, client: firebase.Client()}}
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
	defer pool.Exec(ctx, `DELETE FROM users WHERE firebase_uid IN ($1, $2, $3)`, firstUID, secondUID, thirdUID)
	firstID, err := store.CreateFirebaseUser(ctx, firstUID, firstEmail, "Nia")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkFirebaseEmailVerified(ctx, firstUID); err != nil {
		t.Fatal(err)
	}
	firstToken := newToken()
	if err := store.CreateSession(ctx, firstToken, firstID, time.Now().Add(sessionTTL)); err != nil {
		t.Fatal(err)
	}
	firstCookie := &http.Cookie{Name: sessionCookie, Value: firstToken}
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
	u, err := store.FirebaseUser(ctx, firstUID)
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
	var billingOwner int64
	var betaCohort string
	var betaStarted time.Time
	if err := pool.QueryRow(ctx, `SELECT billing_owner_user_id, beta_cohort, beta_started_at FROM families WHERE id = $1`, f.ID).
		Scan(&billingOwner, &betaCohort, &betaStarted); err != nil {
		t.Fatal(err)
	}
	if billingOwner != u.ID || betaCohort != "beta" || betaStarted.IsZero() {
		t.Fatalf("family monetization metadata: owner=%d cohort=%q started=%v", billingOwner, betaCohort, betaStarted)
	}
	secondID, err := store.CreateFirebaseUser(ctx, secondUID, secondEmail, "Nia")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkFirebaseEmailVerified(ctx, secondUID); err != nil {
		t.Fatal(err)
	}
	secondToken := newToken()
	if err := store.CreateSession(ctx, secondToken, secondID, time.Now().Add(sessionTTL)); err != nil {
		t.Fatal(err)
	}
	secondCookie := &http.Cookie{Name: sessionCookie, Value: secondToken}
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
	u2, err := store.FirebaseUser(ctx, secondUID)
	if err != nil || u2.FamilyID != f.ID {
		t.Fatalf("joined user: %+v %v", u2, err)
	}
	if err := store.JoinFamily(ctx, u2.ID, f.SignupCode, "Bima"); !errors.Is(err, ErrAlreadyLinked) {
		t.Fatalf("second join: %v", err)
	}
	if _, err := store.CreateFamilyForUser(ctx, u2.ID, "Another", newSignupCode()); !errors.Is(err, ErrAlreadyLinked) {
		t.Fatalf("second family: %v", err)
	}
	login := post("/masuk", url.Values{"email": {secondEmail}, "sandi": {"rahasia-aman-123"}}, nil)
	if login.Code != http.StatusSeeOther || login.Header().Get("Location") != "/" {
		t.Fatalf("account login: %d %s", login.Code, login.Body.String())
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
	thirdID, err := store.CreateFirebaseUser(ctx, thirdUID, thirdEmail, "Cici")
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
	emailLogin := post("/masuk", url.Values{"email": {firstEmail}, "sandi": {"rahasia-aman-123"}}, nil)
	if emailLogin.Code != http.StatusSeeOther || emailLogin.Header().Get("Location") != "/" {
		t.Fatalf("linked email login: %d %s", emailLogin.Code, emailLogin.Body.String())
	}
	if err := store.SetMemberActive(ctx, f.ID, u2.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SessionUser(ctx, secondToken); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled member kept session access: %v", err)
	}
}
