package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"strings"
	"time"
)

type firebaseConfig struct {
	APIKey  string `json:"apiKey"`
	Project string `json:"projectId"`
}

type firebaseAuth struct {
	apiKey   string
	endpoint string
	client   *http.Client
}

type firebaseError struct{ Code string }

func (e firebaseError) Error() string { return e.Code }

func loadFirebaseAuth() (*firebaseAuth, error) {
	cfg := firebaseConfig{}
	if b, err := os.ReadFile("firebaseconfig.json"); err == nil {
		if err := json.Unmarshal(b, &cfg); err != nil {
			return nil, fmt.Errorf("firebaseconfig.json tidak valid: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if v := strings.TrimSpace(os.Getenv("FIREBASE_API_KEY")); v != "" {
		cfg.APIKey = v
	}
	if v := strings.TrimSpace(os.Getenv("FIREBASE_PROJECT_ID")); v != "" {
		cfg.Project = v
	}
	if cfg.APIKey == "" {
		return nil, nil
	}
	if cfg.Project == "" {
		return nil, errors.New("projectId tidak ada di firebaseconfig.json atau FIREBASE_PROJECT_ID")
	}
	return &firebaseAuth{apiKey: cfg.APIKey, client: &http.Client{Timeout: 12 * time.Second}}, nil
}

func (f *firebaseAuth) call(ctx context.Context, action string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	base := f.endpoint
	if base == "" {
		base = "https://identitytoolkit.googleapis.com/v1"
	}
	u := strings.TrimRight(base, "/") + "/accounts:" + action + "?key=" + url.QueryEscape(f.apiKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var payload struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		return firebaseError{Code: payload.Error.Message}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type firebaseToken struct {
	IDToken string `json:"idToken"`
	LocalID string `json:"localId"`
	Email   string `json:"email"`
}

type firebaseAccount struct {
	LocalID       string `json:"localId"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"emailVerified"`
}

func (f *firebaseAuth) signUp(ctx context.Context, email, password string) (firebaseToken, error) {
	var out firebaseToken
	err := f.call(ctx, "signUp", map[string]any{"email": email, "password": password, "returnSecureToken": true}, &out)
	return out, err
}

func (f *firebaseAuth) signIn(ctx context.Context, email, password string) (firebaseToken, error) {
	var out firebaseToken
	err := f.call(ctx, "signInWithPassword", map[string]any{"email": email, "password": password, "returnSecureToken": true}, &out)
	return out, err
}

func (f *firebaseAuth) account(ctx context.Context, idToken string) (firebaseAccount, error) {
	var out struct {
		Users []firebaseAccount `json:"users"`
	}
	if err := f.call(ctx, "lookup", map[string]string{"idToken": idToken}, &out); err != nil {
		return firebaseAccount{}, err
	}
	if len(out.Users) != 1 {
		return firebaseAccount{}, errors.New("akun Firebase tidak ditemukan")
	}
	return out.Users[0], nil
}

func (f *firebaseAuth) sendVerification(ctx context.Context, idToken, continueURL string) error {
	return f.call(ctx, "sendOobCode", map[string]string{
		"requestType": "VERIFY_EMAIL", "idToken": idToken, "continueUrl": continueURL,
	}, nil)
}

func (f *firebaseAuth) updatePassword(ctx context.Context, idToken, password string) error {
	return f.call(ctx, "update", map[string]any{"idToken": idToken, "password": password, "returnSecureToken": true}, nil)
}

func (a *App) firebaseContinueURL(r *http.Request) string {
	if a.publicURL != "" {
		return a.publicURL + "/masuk?verified=1"
	}
	if r.Host != "localhost" && !strings.HasPrefix(r.Host, "localhost:") &&
		r.Host != "127.0.0.1" && !strings.HasPrefix(r.Host, "127.0.0.1:") {
		return ""
	}
	scheme := "http"
	if https(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host + "/masuk?verified=1"
}

func normalizeEmail(raw string) (string, error) {
	if len(raw) > 254 {
		return "", errors.New("alamat email terlalu panjang")
	}
	a, err := mail.ParseAddress(strings.TrimSpace(raw))
	if err != nil || a.Address != strings.TrimSpace(raw) {
		return "", errors.New("alamat email tidak valid")
	}
	return strings.ToLower(a.Address), nil
}
