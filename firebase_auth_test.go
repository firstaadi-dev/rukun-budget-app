package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFirebaseAuthEmailFlow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		switch r.URL.Path {
		case "/accounts:signInWithPassword":
			if body["email"] != "tester@example.com" || body["password"] != "secret123" || body["returnSecureToken"] != true {
				t.Errorf("unexpected sign-in payload: %#v", body)
			}
			json.NewEncoder(w).Encode(firebaseToken{IDToken: "id-token", LocalID: "uid-1", Email: body["email"].(string)})
		case "/accounts:lookup":
			if body["idToken"] != "id-token" {
				t.Errorf("unexpected lookup payload: %#v", body)
			}
			json.NewEncoder(w).Encode(map[string]any{"users": []firebaseAccount{{LocalID: "uid-1", Email: "tester@example.com", EmailVerified: true}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	firebase := &firebaseAuth{apiKey: "test-key", endpoint: server.URL, client: server.Client()}
	token, err := firebase.signIn(context.Background(), "tester@example.com", "secret123")
	if err != nil {
		t.Fatal(err)
	}
	account, err := firebase.account(context.Background(), token.IDToken)
	if err != nil || account.LocalID != token.LocalID || !account.EmailVerified {
		t.Fatalf("account = %#v, err = %v", account, err)
	}
}

func TestNormalizeEmail(t *testing.T) {
	got, err := normalizeEmail("  TESTER@Example.com  ")
	if err != nil || got != "tester@example.com" {
		t.Fatalf("normalizeEmail() = %q, %v", got, err)
	}
	if _, err := normalizeEmail("Name <tester@example.com>"); err == nil {
		t.Fatal("expected display-name email to be rejected")
	}
}
