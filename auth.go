package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"
)

const (
	authorizeURL = "https://linear.app/oauth/authorize"
	revokeURL    = "https://api.linear.app/oauth/revoke"
	// The OAuth app's registered callback. Linear matches it exactly, so the
	// port is fixed rather than picked at random.
	callbackPort = 47821
	redirectURI  = "http://localhost:47821/callback"
	scopes       = "read,write"

	keychainService = "herdr-linear"
	keychainAccount = "oauth"
)

// Seams for tests: the token endpoint, the browser, and the keychain.
var (
	tokenURL   = "https://api.linear.app/oauth/token"
	browse     = openBrowser
	readStore  = loadTokens
	writeStore = saveTokens
)

// errSignedOut means there are no usable credentials: sign in again.
var errSignedOut = errors.New("not signed in to Linear")

type tokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

// ── storage: the macOS login keychain ─────────────────────────────────────────

func loadTokens() (*tokens, error) {
	out, err := exec.Command("security", "find-generic-password", "-s", keychainService, "-a", keychainAccount, "-w").Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 44 { // errSecItemNotFound
			return nil, errSignedOut
		}
		return nil, fmt.Errorf("keychain read: %w", err)
	}
	var t tokens
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &t); err != nil || t.AccessToken == "" {
		return nil, errSignedOut
	}
	return &t, nil
}

// saveTokens writes through `security -i` so the secret travels over stdin,
// never argv (which every local user can read with ps).
func saveTokens(t *tokens) error {
	data, err := json.Marshal(t)
	if err != nil {
		return err
	}
	cmd := exec.Command("security", "-i")
	cmd.Stdin = strings.NewReader(fmt.Sprintf("add-generic-password -U -s %s -a %s -l \"herdr Linear\" -X %s\n",
		keychainService, keychainAccount, hex.EncodeToString(data)))
	out, err := cmd.CombinedOutput()
	// `security -i` exits 0 even when a command fails; it reports on output.
	if err != nil || strings.Contains(string(out), "returned") {
		return fmt.Errorf("keychain write failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func deleteTokens() error {
	err := exec.Command("security", "delete-generic-password", "-s", keychainService, "-a", keychainAccount).Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 44 {
		return nil
	}
	return err
}

// ── access: hand out a valid access token, refreshing when due ────────────────

func accessToken(ctx context.Context, cfg config, forceRefresh bool) (string, error) {
	t, err := readStore()
	if err != nil {
		return "", err
	}
	if !forceRefresh && time.Until(t.ExpiresAt) > 5*time.Minute {
		return t.AccessToken, nil
	}
	if t.RefreshToken == "" {
		return "", errSignedOut
	}
	// Linear rotates the refresh token on every use, with a 30-minute grace
	// window — so two processes refreshing at once both come away valid.
	resp, err := postToken(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {t.RefreshToken},
		"client_id":     {cfg.ClientID},
	})
	if err != nil {
		return "", err
	}
	nt := resp.toTokens(t.RefreshToken)
	if err := writeStore(nt); err != nil {
		return "", err
	}
	return nt.AccessToken, nil
}

func (r *tokenResponse) toTokens(previousRefresh string) *tokens {
	refresh := r.RefreshToken
	if refresh == "" {
		refresh = previousRefresh
	}
	expires := r.ExpiresIn
	if expires <= 0 {
		expires = 24 * 3600
	}
	return &tokens{AccessToken: r.AccessToken, RefreshToken: refresh, ExpiresAt: time.Now().Add(time.Duration(expires) * time.Second)}
}

type oauthError struct {
	Status int
	Code   string
	Desc   string
}

func (e *oauthError) Error() string {
	if e.Desc != "" {
		return fmt.Sprintf("Linear OAuth %d: %s (%s)", e.Status, e.Code, e.Desc)
	}
	return fmt.Sprintf("Linear OAuth %d: %s", e.Status, e.Code)
}

func postToken(ctx context.Context, form url.Values) (*tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
			Desc  string `json:"error_description"`
		}
		_ = json.Unmarshal(body, &e)
		if e.Error == "invalid_grant" {
			// The refresh token was revoked or expired: only a new sign-in helps.
			return nil, errSignedOut
		}
		if e.Error == "" {
			e.Error = strings.TrimSpace(string(body))
		}
		return nil, &oauthError{res.StatusCode, e.Error, e.Desc}
	}
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil || tr.AccessToken == "" {
		return nil, fmt.Errorf("Linear OAuth: unexpected token response")
	}
	return &tr, nil
}

// ── sign in: authorization code + PKCE through a loopback callback ────────────

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// login runs the browser sign-in and stores the result. status reports progress
// lines for whoever is watching (the popup, or stdout).
func login(ctx context.Context, cfg config, status func(string)) error {
	verifier := randomToken(48)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	state := randomToken(24)

	// "localhost" may resolve to either loopback family in the browser, so
	// listen on both; IPv4 is required, IPv6 best-effort.
	v4, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", callbackPort))
	if err != nil {
		return fmt.Errorf("can't listen on port %d for the sign-in callback (another sign-in open?): %w", callbackPort, err)
	}
	listeners := []net.Listener{v4}
	if v6, err := net.Listen("tcp", fmt.Sprintf("[::1]:%d", callbackPort)); err == nil {
		listeners = append(listeners, v6)
	}

	type result struct {
		code string
		err  error
	}
	done := make(chan result, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		var res result
		switch {
		case q.Get("state") != state:
			// Not our request (or a stale tab): ignore it and keep waiting.
			http.Error(w, "Unknown sign-in request. Start again from herdr.", http.StatusBadRequest)
			return
		case q.Get("error") != "":
			res.err = fmt.Errorf("Linear declined the sign-in: %s", q.Get("error"))
		case q.Get("code") == "":
			res.err = errors.New("Linear sent no authorization code")
		default:
			res.code = q.Get("code")
		}
		msg := "Signed in to Linear. You can close this tab and go back to herdr."
		if res.err != nil {
			msg = "Sign-in failed: " + res.err.Error()
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>herdr · Linear</title>`+
			`<body style="font:16px system-ui;margin:4rem auto;max-width:32rem">%s</body>`, html.EscapeString(msg))
		select {
		case done <- res:
		default:
		}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	for _, l := range listeners {
		go func(l net.Listener) { _ = srv.Serve(l) }(l)
	}
	defer srv.Close()

	q := url.Values{
		"client_id":             {cfg.ClientID},
		"redirect_uri":          {redirectURI},
		"response_type":         {"code"},
		"scope":                 {scopes},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	authURL := authorizeURL + "?" + q.Encode()
	status("Opening Linear in your browser…")
	if err := browse(authURL); err != nil {
		status("Couldn't open a browser. Open this URL:\n" + authURL)
	}
	status("Waiting for you to approve access in the browser (esc cancels)…")

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var res result
	select {
	case res = <-done:
	case <-ctx.Done():
		return errors.New("sign-in timed out or was cancelled")
	}
	if res.err != nil {
		return res.err
	}

	tr, err := postToken(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {res.code},
		"redirect_uri":  {redirectURI},
		"client_id":     {cfg.ClientID},
		"code_verifier": {verifier},
	})
	if err != nil {
		return err
	}
	return writeStore(tr.toTokens(""))
}

// logout revokes the grant at Linear (best effort) and forgets it locally.
func logout(ctx context.Context) error {
	if t, err := loadTokens(); err == nil {
		for _, tok := range []struct{ value, hint string }{{t.RefreshToken, "refresh_token"}, {t.AccessToken, "access_token"}} {
			if tok.value == "" {
				continue
			}
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, revokeURL,
				strings.NewReader(url.Values{"token": {tok.value}, "token_type_hint": {tok.hint}}.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Authorization", "Bearer "+t.AccessToken)
			if res, err := http.DefaultClient.Do(req); err == nil {
				res.Body.Close()
			}
		}
	}
	return deleteTokens()
}

// macOS only for now, like the keychain storage above.
func openBrowser(u string) error {
	return exec.Command("open", u).Run()
}
