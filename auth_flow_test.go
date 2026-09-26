package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInviteWithoutSessionGoesToRegistration(t *testing.T) {
	a := &App{pages: parsePages()}
	r := httptest.NewRequest("GET", "/mulai?kode=kode-undangan", nil)
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)

	if w.Code != 303 || w.Header().Get("Location") != "/daftar" {
		t.Fatalf("invite redirect = %d %q", w.Code, w.Header().Get("Location"))
	}
	var remembered bool
	for _, c := range w.Result().Cookies() {
		if c.Name == pendingInviteCookie && c.Value == "kode-undangan" {
			remembered = true
		}
	}
	if !remembered {
		t.Fatal("invite code was not preserved for registration and joining a family")
	}
}

func TestLoginPageShowsOnlyNewAccountLogin(t *testing.T) {
	a := &App{pages: parsePages()}
	w := httptest.NewRecorder()
	a.loginForm(w, httptest.NewRequest("GET", "/masuk", nil))
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, `name="email"`) {
		t.Fatalf("email account login form missing: %d", w.Code)
	}
	if strings.Contains(body, "Akun lama") || strings.Contains(body, `name="username"`) {
		t.Fatal("public login page still exposes legacy login")
	}
}
