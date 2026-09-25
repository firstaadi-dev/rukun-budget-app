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
