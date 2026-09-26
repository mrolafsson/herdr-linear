package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConcurrentRefreshesTakeTurns(t *testing.T) {
	fakeStore(t, &tokens{AccessToken: "old", RefreshToken: "r0", ExpiresAt: time.Now()})
	var refreshes atomic.Int32
	fakeTokenServer(t, func(form url.Values) (int, any) {
		n := refreshes.Add(1)
		time.Sleep(50 * time.Millisecond) // a slow Linear widens the race
		return 200, map[string]any{"access_token": fmt.Sprint("a", n), "refresh_token": fmt.Sprint("r", n), "expires_in": 86400}
	})
	var wg sync.WaitGroup
	got := make([]string, 8)
	for i := range got {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i], _ = accessToken(context.Background(), config{}, "a", false)
		}(i)
	}
	wg.Wait()
	if n := refreshes.Load(); n != 1 {
		t.Fatalf("%d refreshes for one expired token; each consumes a refresh token", n)
	}
	for _, g := range got {
		if g != "a1" {
			t.Fatalf("callers got %v, want all a1", got)
		}
	}
}

// fakeRevoke serves the revoke endpoint with the given status (0 = refuse to connect).
func fakeRevoke(t *testing.T, status int) *[]url.Values {
	t.Helper()
	var seen []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		seen = append(seen, r.PostForm)
		w.WriteHeader(status)
	}))
	old := revokeURL
	revokeURL = srv.URL
	if status == 0 {
		srv.Close()
	}
	t.Cleanup(func() { revokeURL = old; srv.Close() })
	return &seen
}

func signedIn(t *testing.T) **tokens {
	return fakeStore(t, &tokens{AccessToken: "a", RefreshToken: "r", ExpiresAt: time.Now().Add(time.Hour)})
}

func TestLogoutRevokesThenForgets(t *testing.T) {
	stored := signedIn(t)
	seen := fakeRevoke(t, 200)
	if _, err := logout(context.Background(), config{}, "", false); err != nil {
		t.Fatal(err)
	}
	if *stored != nil || len(*seen) != 1 || (*seen)[0].Get("token") != "r" {
		t.Fatalf("stored %v, revoke calls %v", *stored, *seen)
	}
}

func TestLogoutKeepsTokensWhenRevokeFails(t *testing.T) {
	// 400 "unable to revoke" and 401 "unable to authenticate" don't mean the
	// grant is gone either (round 2).
	for name, status := range map[string]int{"Linear down": 503, "unreachable": 0, "unable to revoke": 400, "unable to authenticate": 401} {
		t.Run(name, func(t *testing.T) {
			stored := signedIn(t)
			fakeRevoke(t, status)
			_, err := logout(context.Background(), config{}, "", false)
			if err == nil || !strings.Contains(err.Error(), "still signed in") {
				t.Fatalf("err %v", err)
			}
			if *stored == nil {
				t.Fatal("tokens forgotten although access wasn't revoked")
			}
		})
	}
}

func TestLogoutLocalForgetsWithoutRevoking(t *testing.T) {
	stored := signedIn(t)
	seen := fakeRevoke(t, 200)
	if _, err := logout(context.Background(), config{}, "", true); err != nil || *stored != nil || len(*seen) != 0 {
		t.Fatalf("err %v stored %v calls %d", err, *stored, len(*seen))
	}
}

// invalid_grant doesn't prove the grant is gone (the token may belong to
// another client_id), so it isn't a revoke: keep the tokens, point to --local.
func TestLogoutKeepsTokensOnInvalidGrant(t *testing.T) {
	stored := fakeStore(t, &tokens{AccessToken: "a", RefreshToken: "r", ExpiresAt: time.Now()})
	fakeTokenServer(t, func(url.Values) (int, any) { return 400, map[string]any{"error": "invalid_grant"} })
	seen := fakeRevoke(t, 200)
	_, err := logout(context.Background(), config{}, "", false)
	if err == nil || !strings.Contains(err.Error(), "--local") || *stored == nil || len(*seen) != 0 {
		t.Fatalf("err %v stored %v revoke calls %d", err, *stored, len(*seen))
	}
}

func TestCancelledCommandChangesNoTokens(t *testing.T) {
	stored := signedIn(t)
	fakeRevoke(t, 200)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := logout(ctx, config{}, "", true); err == nil {
		t.Fatal("a cancelled sign-out went ahead")
	}
	if *stored == nil {
		t.Fatal("tokens deleted by a cancelled command")
	}
}

// Round 2: a refresh in flight must not write tokens back after sign-out.
func TestLogoutDuringARefreshStaysSignedOut(t *testing.T) {
	stored := fakeStore(t, &tokens{AccessToken: "old", RefreshToken: "r0", ExpiresAt: time.Now()})
	inFlight := make(chan struct{})
	fakeTokenServer(t, func(url.Values) (int, any) {
		close(inFlight)
		time.Sleep(150 * time.Millisecond) // the refresh is slow
		return 200, map[string]any{"access_token": "new", "refresh_token": "r1", "expires_in": 86400}
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = accessToken(context.Background(), config{}, "a", false)
	}()
	<-inFlight
	if _, err := logout(context.Background(), config{}, "", true); err != nil {
		t.Fatal(err)
	}
	<-done
	if *stored != nil {
		t.Fatalf("signed back in by a refresh that finished after sign-out: %+v", *stored)
	}
}

func TestTokenLockFailsClosed(t *testing.T) {
	fakeStore(t, &tokens{AccessToken: "old", RefreshToken: "r", ExpiresAt: time.Now()})
	// A state "directory" that is a file: the lock can't be created.
	f := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(f, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_PLUGIN_STATE_DIR", f)
	fakeTokenServer(t, func(url.Values) (int, any) { t.Error("refreshed without the lock"); return 500, nil })
	if _, err := accessToken(context.Background(), config{}, "a", false); err == nil || !strings.Contains(err.Error(), "token lock") {
		t.Fatalf("want a lock error, got %v", err)
	}
}

func TestTokenLockGivesUpInsteadOfWaitingForever(t *testing.T) {
	fakeStore(t, &tokens{AccessToken: "old", RefreshToken: "r", ExpiresAt: time.Now()})
	old := lockWait
	lockWait = 200 * time.Millisecond
	t.Cleanup(func() { lockWait = old })
	unlock, err := lockTokens(context.Background()) // a stuck process holds it
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	start := time.Now()
	if _, err := accessToken(context.Background(), config{}, "a", false); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("want a busy error, got %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("waited %v", d)
	}
}

func TestLogoutWhenNotSignedIn(t *testing.T) {
	fakeStore(t, nil)
	if _, err := logout(context.Background(), config{}, "", false); !errors.Is(err, errNotSignedIn) {
		t.Fatalf("%v", err)
	}
}

// fakeStore swaps the keychain for memory for one test: one item, whatever
// the account. Linear's answer to which workspace a token is for is Acme.
func fakeStore(t *testing.T, initial *tokens) **tokens {
	t.Helper()
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir()) // for the refresh lock
	var mu sync.Mutex
	cur := initial
	oldR, oldW, oldD, oldWho := readStore, writeStore, removeStore, whoami
	whoami = func(context.Context, config, string) (workspace, error) { return acme, nil }
	readStore = func(string) (*tokens, error) {
		mu.Lock()
		defer mu.Unlock()
		if cur == nil {
			return nil, errSignedOut
		}
		c := *cur
		return &c, nil
	}
	writeStore = func(_ string, nt *tokens) error { mu.Lock(); defer mu.Unlock(); c := *nt; cur = &c; return nil }
	removeStore = func(string) error { mu.Lock(); defer mu.Unlock(); cur = nil; return nil }
	t.Cleanup(func() { readStore, writeStore, removeStore, whoami = oldR, oldW, oldD, oldWho })
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
	old, oldNo := browse, noBrowser
	noBrowser = func() bool { return false }
	browse = func(u string) error {
		parsed, err := url.Parse(u)
		if err != nil {
			t.Fatal(err)
		}
		go visit(parsed)
		return nil
	}
	t.Cleanup(func() { browse, noBrowser = old, oldNo })
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
			"scope": "read,write", "code_challenge_method": "S256", "prompt": "consent",
		} {
			if q.Get(k) != want {
				t.Errorf("authorize %s = %q, want %q", k, q.Get(k), want)
			}
		}
		challenge = q.Get("code_challenge")
		callback(t, url.Values{"code": {"the-code"}, "state": {q.Get("state")}})
	})

	if _, err := login(context.Background(), config{ClientID: "cid"}, loginUI{}); err != nil {
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
	if _, err := login(context.Background(), config{ClientID: "cid"}, loginUI{}); err != nil {
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
	_, err := login(context.Background(), config{ClientID: "cid"}, loginUI{})
	if err == nil || *stored != nil {
		t.Fatalf("err=%v stored=%v", err, *stored)
	}
}

func TestLoginCancel(t *testing.T) {
	fakeStore(t, nil)
	fakeBrowser(t, func(*url.URL) {})
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	if _, err := login(ctx, config{ClientID: "cid"}, loginUI{}); err == nil {
		t.Fatal("want an error after cancel")
	}
	// The port must be free again for the next attempt.
	fakeTokenServer(t, func(url.Values) (int, any) {
		return 200, map[string]any{"access_token": "at", "refresh_token": "rt"}
	})
	fakeBrowser(t, func(u *url.URL) { callback(t, url.Values{"code": {"c"}, "state": {u.Query().Get("state")}}) })
	if _, err := login(context.Background(), config{ClientID: "cid"}, loginUI{}); err != nil {
		t.Fatalf("second sign-in after cancel: %v", err)
	}
}

// remoteLogin signs in as over SSH: no browser, and the link handed over.
// answer gets the link and returns what's pasted, a line at a time.
func remoteLogin(t *testing.T, answer func(authURL *url.URL) []string) (loginUI, *[]string) {
	t.Helper()
	old, oldNo := browse, noBrowser
	noBrowser = func() bool { return true }
	browse = func(string) error { t.Error("opened a browser over SSH"); return nil }
	t.Cleanup(func() { browse, noBrowser = old, oldNo })
	pasted := make(chan string)
	var said []string
	return loginUI{
		status: func(s string) { said = append(said, s) },
		link: func(u string, opened bool) {
			if opened {
				t.Error("link says a browser opened")
			}
			parsed, err := url.Parse(u)
			if err != nil {
				t.Fatal(err)
			}
			go func() {
				for _, p := range answer(parsed) {
					pasted <- p
				}
			}()
		},
		pasted: pasted,
	}, &said
}

func TestLoginTakesThePastedAddress(t *testing.T) {
	stored := fakeStore(t, nil)
	seen := fakeTokenServer(t, func(url.Values) (int, any) {
		return 200, map[string]any{"access_token": "at-p", "refresh_token": "rt-p", "expires_in": 3600}
	})
	ui, said := remoteLogin(t, func(u *url.URL) []string {
		back := redirectURI + "?" + url.Values{"code": {"pasted-code"}, "state": {u.Query().Get("state")}}.Encode()
		return []string{
			"nonsense",
			redirectURI + "?code=evil&state=not-ours", // someone else's link
			// As copied off a wrapped screen: broken across lines.
			"  " + back[:30] + "\n" + back[30:] + "  ",
		}
	})
	if _, err := login(context.Background(), config{ClientID: "cid"}, ui); err != nil {
		t.Fatal(err)
	}
	if got := (*seen)[0].Get("code"); got != "pasted-code" {
		t.Errorf("exchanged code %q", got)
	}
	if *stored == nil || (*stored).AccessToken != "at-p" {
		t.Fatalf("stored %+v", *stored)
	}
	rejected := 0
	for _, s := range *said {
		if s == errPasted.Error() {
			rejected++
		}
	}
	if rejected != 2 {
		t.Errorf("rejected %d pastes, want 2: %q", rejected, *said)
	}
	if last := (*said)[len(*said)-1]; last != "Signing in…" {
		t.Errorf("last said %q, not that it's signing in", last)
	}
}

func TestLoginPastedDenial(t *testing.T) {
	stored := fakeStore(t, nil)
	fakeTokenServer(t, func(url.Values) (int, any) {
		t.Error("no token exchange after a denial")
		return 500, nil
	})
	ui, _ := remoteLogin(t, func(u *url.URL) []string {
		return []string{redirectURI + "?error=access_denied&state=" + url.QueryEscape(u.Query().Get("state"))}
	})
	if _, err := login(context.Background(), config{ClientID: "cid"}, ui); err == nil || *stored != nil {
		t.Fatalf("err=%v stored=%v", err, *stored)
	}
}

// Over SSH with the port forwarded, the callback still finishes it.
func TestLoginRemoteCallbackStillWorks(t *testing.T) {
	fakeStore(t, nil)
	fakeTokenServer(t, func(url.Values) (int, any) {
		return 200, map[string]any{"access_token": "at", "refresh_token": "rt"}
	})
	ui, _ := remoteLogin(t, func(u *url.URL) []string {
		callback(t, url.Values{"code": {"c"}, "state": {u.Query().Get("state")}})
		return nil
	})
	if _, err := login(context.Background(), config{ClientID: "cid"}, ui); err != nil {
		t.Fatal(err)
	}
}

func TestPastedResult(t *testing.T) {
	for in, want := range map[string]string{
		redirectURI + "?code=c1&state=s":           "c1",
		redirectURI + "?state=s&code=c2#frag":      "c2",
		"code=c3&state=s":                          "c3",
		"localhost:47821/callback?code=c4&state=s": "c4",
	} {
		if got, err := pastedResult(in, "s"); err != nil || got != want {
			t.Errorf("%q: %q, %v", in, got, err)
		}
	}
	signInLink := authorizeURL + "?client_id=cid&redirect_uri=" + url.QueryEscape(redirectURI) + "&state=s"
	for _, in := range []string{"", "c1", "code=c1", "code=c1&state=other", "https://linear.app/", signInLink, redirectURI + "?state=s"} {
		if _, err := pastedResult(in, "s"); !errors.Is(err, errPasted) {
			t.Errorf("%q: %v, want errPasted", in, err)
		}
	}
}

func TestAccessTokenFreshIsNotRefreshed(t *testing.T) {
	fakeStore(t, &tokens{AccessToken: "fresh", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	fakeTokenServer(t, func(url.Values) (int, any) { t.Error("refreshed a fresh token"); return 500, nil })
	if got, err := accessToken(context.Background(), config{ClientID: "cid"}, "a", false); err != nil || got != "fresh" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestAccessTokenRefreshesNearExpiryAndRotates(t *testing.T) {
	stored := fakeStore(t, &tokens{AccessToken: "old", RefreshToken: "rt-old", ExpiresAt: time.Now().Add(time.Minute)})
	seen := fakeTokenServer(t, func(url.Values) (int, any) {
		return 200, map[string]any{"access_token": "new", "refresh_token": "rt-new", "expires_in": 86399}
	})
	got, err := accessToken(context.Background(), config{ClientID: "cid"}, "a", false)
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
	if _, err := accessToken(context.Background(), config{}, "a", false); err != nil {
		t.Fatal(err)
	}
	if (*stored).RefreshToken != "rt-keep" {
		t.Errorf("refresh token lost: %+v", *stored)
	}
}

func TestRevokedRefreshTokenMeansSignedOut(t *testing.T) {
	fakeStore(t, &tokens{AccessToken: "old", RefreshToken: "rt", ExpiresAt: time.Now()})
	fakeTokenServer(t, func(url.Values) (int, any) { return 400, map[string]any{"error": "invalid_grant"} })
	if _, err := accessToken(context.Background(), config{}, "a", false); !errors.Is(err, errSignedOut) {
		t.Fatalf("got %v, want errSignedOut", err)
	}
}

func TestNoStoredTokensMeansSignedOut(t *testing.T) {
	fakeStore(t, nil)
	if _, err := accessToken(context.Background(), config{}, "a", false); !errors.Is(err, errSignedOut) {
		t.Fatalf("got %v", err)
	}
}
