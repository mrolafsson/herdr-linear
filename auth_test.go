package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// fakeStore swaps the keychain for memory for one test.
func fakeStore(t *testing.T, initial *tokens) **tokens {
	t.Helper()
	cur := initial
	oldR, oldW := readStore, writeStore
	readStore = func() (*tokens, error) {
		if cur == nil {
			return nil, errSignedOut
		}
		c := *cur
		return &c, nil
	}
	writeStore = func(nt *tokens) error { c := *nt; cur = &c; return nil }
	t.Cleanup(func() { readStore, writeStore = oldR, oldW })
	return &cur
}

func fakeTokenServer(t *testing.T, handle func(form url.Values) (int, any)) *[]url.Values {
	t.Helper()
	var seen []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Errorf("token request: %s %s", r.Method, r.Header.Get("Content-Type"))
		}
		_ = r.ParseForm()
		seen = append(seen, r.PostForm)
		status, body := handle(r.PostForm)
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}))
	old := tokenURL
	tokenURL = srv.URL
	t.Cleanup(func() { tokenURL = old; srv.Close() })
	return &seen
}

func fakeBrowser(t *testing.T, visit func(authURL *url.URL)) {
	t.Helper()
	old := browse
	browse = func(u string) error {
		parsed, err := url.Parse(u)
		if err != nil {
			t.Fatal(err)
		}
		go visit(parsed)
		return nil
	}
	t.Cleanup(func() { browse = old })
}

func callback(t *testing.T, q url.Values) int {
	t.Helper()
	res, err := http.Get(redirectURI + "?" + q.Encode())
	if err != nil {
		t.Errorf("callback: %v", err)
		return 0
	}
	res.Body.Close()
	return res.StatusCode
}

func TestLoginExchangesCodeWithPKCE(t *testing.T) {
	stored := fakeStore(t, nil)
	var challenge string
	seen := fakeTokenServer(t, func(form url.Values) (int, any) {
		sum := sha256.Sum256([]byte(form.Get("code_verifier")))
		if got := base64.RawURLEncoding.EncodeToString(sum[:]); got != challenge {
			t.Errorf("verifier doesn't match the challenge sent to /authorize")
		}
		return 200, map[string]any{"access_token": "at-1", "refresh_token": "rt-1", "expires_in": 86399}
	})
	fakeBrowser(t, func(u *url.URL) {
		q := u.Query()
		for k, want := range map[string]string{
			"client_id": "cid", "redirect_uri": redirectURI, "response_type": "code",
			"scope": "read,write", "code_challenge_method": "S256",
		} {
			if q.Get(k) != want {
				t.Errorf("authorize %s = %q, want %q", k, q.Get(k), want)
			}
		}
		challenge = q.Get("code_challenge")
		callback(t, url.Values{"code": {"the-code"}, "state": {q.Get("state")}})
	})

	if err := login(context.Background(), config{ClientID: "cid"}, func(string) {}); err != nil {
		t.Fatal(err)
	}
	form := (*seen)[0]
	if form.Get("grant_type") != "authorization_code" || form.Get("code") != "the-code" ||
		form.Get("redirect_uri") != redirectURI || form.Get("client_id") != "cid" {
		t.Errorf("token exchange form = %v", form)
	}
	if form.Get("client_secret") != "" {
		t.Error("a public PKCE client must not send a secret")
	}
	got := *stored
	if got == nil || got.AccessToken != "at-1" || got.RefreshToken != "rt-1" {
		t.Fatalf("stored %+v", got)
	}
	if d := time.Until(got.ExpiresAt); d < 23*time.Hour || d > 25*time.Hour {
		t.Errorf("expiry %v from now, want ~24h", d)
	}
}

func TestLoginIgnoresForeignStateThenCompletes(t *testing.T) {
	fakeStore(t, nil)
	fakeTokenServer(t, func(url.Values) (int, any) {
		return 200, map[string]any{"access_token": "at", "refresh_token": "rt", "expires_in": 60}
	})
	fakeBrowser(t, func(u *url.URL) {
		if code := callback(t, url.Values{"code": {"evil"}, "state": {"not-ours"}}); code != http.StatusBadRequest {
			t.Errorf("foreign state answered %d, want 400", code)
		}
		callback(t, url.Values{"code": {"good"}, "state": {u.Query().Get("state")}})
	})
	if err := login(context.Background(), config{ClientID: "cid"}, func(string) {}); err != nil {
		t.Fatal(err)
	}
}

func TestLoginReportsDenial(t *testing.T) {
	stored := fakeStore(t, nil)
	fakeTokenServer(t, func(url.Values) (int, any) {
		t.Error("no token exchange after a denial")
		return 500, nil
	})
	fakeBrowser(t, func(u *url.URL) {
		callback(t, url.Values{"error": {"access_denied"}, "state": {u.Query().Get("state")}})
	})
	err := login(context.Background(), config{ClientID: "cid"}, func(string) {})
	if err == nil || *stored != nil {
		t.Fatalf("err=%v stored=%v", err, *stored)
	}
}

func TestLoginCancel(t *testing.T) {
	fakeStore(t, nil)
	fakeBrowser(t, func(*url.URL) {})
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	if err := login(ctx, config{ClientID: "cid"}, func(string) {}); err == nil {
		t.Fatal("want an error after cancel")
	}
	// The port must be free again for the next attempt.
	fakeTokenServer(t, func(url.Values) (int, any) {
		return 200, map[string]any{"access_token": "at", "refresh_token": "rt"}
	})
	fakeBrowser(t, func(u *url.URL) { callback(t, url.Values{"code": {"c"}, "state": {u.Query().Get("state")}}) })
	if err := login(context.Background(), config{ClientID: "cid"}, func(string) {}); err != nil {
		t.Fatalf("second sign-in after cancel: %v", err)
	}
}

func TestAccessTokenFreshIsNotRefreshed(t *testing.T) {
	fakeStore(t, &tokens{AccessToken: "fresh", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	fakeTokenServer(t, func(url.Values) (int, any) { t.Error("refreshed a fresh token"); return 500, nil })
	if got, err := accessToken(context.Background(), config{ClientID: "cid"}, false); err != nil || got != "fresh" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestAccessTokenRefreshesNearExpiryAndRotates(t *testing.T) {
	stored := fakeStore(t, &tokens{AccessToken: "old", RefreshToken: "rt-old", ExpiresAt: time.Now().Add(time.Minute)})
	seen := fakeTokenServer(t, func(url.Values) (int, any) {
		return 200, map[string]any{"access_token": "new", "refresh_token": "rt-new", "expires_in": 86399}
	})
	got, err := accessToken(context.Background(), config{ClientID: "cid"}, false)
	if err != nil || got != "new" {
		t.Fatalf("got %q, %v", got, err)
	}
	form := (*seen)[0]
	if form.Get("grant_type") != "refresh_token" || form.Get("refresh_token") != "rt-old" || form.Get("client_id") != "cid" {
		t.Errorf("refresh form = %v", form)
	}
	if (*stored).RefreshToken != "rt-new" {
		t.Errorf("rotated refresh token not stored: %+v", *stored)
	}
}

func TestRefreshKeepsOldRefreshTokenWhenNoneReturned(t *testing.T) {
	stored := fakeStore(t, &tokens{AccessToken: "old", RefreshToken: "rt-keep", ExpiresAt: time.Now()})
	fakeTokenServer(t, func(url.Values) (int, any) { return 200, map[string]any{"access_token": "new"} })
	if _, err := accessToken(context.Background(), config{}, false); err != nil {
		t.Fatal(err)
	}
	if (*stored).RefreshToken != "rt-keep" {
		t.Errorf("refresh token lost: %+v", *stored)
	}
}

func TestRevokedRefreshTokenMeansSignedOut(t *testing.T) {
	fakeStore(t, &tokens{AccessToken: "old", RefreshToken: "rt", ExpiresAt: time.Now()})
	fakeTokenServer(t, func(url.Values) (int, any) { return 400, map[string]any{"error": "invalid_grant"} })
	if _, err := accessToken(context.Background(), config{}, false); !errors.Is(err, errSignedOut) {
		t.Fatalf("got %v, want errSignedOut", err)
	}
}

func TestNoStoredTokensMeansSignedOut(t *testing.T) {
	fakeStore(t, nil)
	if _, err := accessToken(context.Background(), config{}, false); !errors.Is(err, errSignedOut) {
		t.Fatalf("got %v", err)
	}
}
