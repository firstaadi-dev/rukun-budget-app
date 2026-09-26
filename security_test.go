package main

import (
	"net/http/httptest"
	"testing"
)

func TestSameOrigin(t *testing.T) {
	for _, tc := range []struct {
		origin, forwarded string
		want              bool
	}{
		{"https://rukun.example", "https", true},
		{"https://evil.example", "https", false},
		{"http://rukun.example", "https", false},
		{"", "https", false},
	} {
		r := httptest.NewRequest("POST", "http://rukun.example/transaksi/baru", nil)
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("X-Forwarded-Proto", tc.forwarded)
		if got := sameOrigin(r); got != tc.want {
			t.Errorf("origin %q forwarded %q: got %v, want %v", tc.origin, tc.forwarded, got, tc.want)
		}
	}
	r := httptest.NewRequest("POST", "http://internal:8080/mulai/gabung", nil)
	r.Header.Set("Origin", "https://rukun.firsta.my.id")
	r.Header.Set("X-Forwarded-Host", "rukun.firsta.my.id")
	r.Header.Set("X-Forwarded-Proto", "https")
	if !sameOrigin(r) {
		t.Fatal("forwarded public host was rejected")
	}
	r = httptest.NewRequest("POST", "https://rukun.firsta.my.id/mulai/gabung", nil)
	r.Header.Set("Origin", "null")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	if !sameOrigin(r) {
		t.Fatal("same-origin opaque origin was rejected")
	}
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	if sameOrigin(r) {
		t.Fatal("cross-site opaque origin was accepted")
	}
}

func TestLoginLimit(t *testing.T) {
	a := &App{}
	for i := 0; i < 10; i++ {
		if !a.loginAllowed("client|family|user") {
			t.Fatal("blocked before 10 failed attempts")
		}
		a.loginFailed("client|family|user")
	}
	if a.loginAllowed("client|family|user") || !a.loginAllowed("other|family|user") {
		t.Fatal("login limit did not isolate the key")
	}
	a.loginSucceeded("client|family|user")
	if !a.loginAllowed("client|family|user") {
		t.Fatal("successful login did not clear failures")
	}
}
