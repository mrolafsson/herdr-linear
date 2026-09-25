package main

import (
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// These run against a real Secret Service, which needs one running and
// unlocked, so only with HERDR_LINEAR_KEYRING_TEST=1 (scripts/test-linux.sh
// and CI's Linux job start GNOME Keyring for them).
func needKeyring(t *testing.T) string {
	t.Helper()
	if os.Getenv("HERDR_LINEAR_KEYRING_TEST") != "1" {
		t.Skip("set HERDR_LINEAR_KEYRING_TEST=1 with a Secret Service running")
	}
	old := keyringWait
	keyringWait = 15 * time.Second // no one's there to answer a prompt
	t.Cleanup(func() { keyringWait = old })
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir()) // any file copy stays here
	acct := "oauth:test-" + time.Now().Format("150405.000000")
	t.Cleanup(func() { _ = keyringDelete(acct) })
	return acct
}

// count is how many items acct has, locked or not.
func count(t *testing.T, acct string) int {
	t.Helper()
	n := 0
	if err := withSecretService(func(s *secretService) error {
		u, l, err := s.search(acct)
		n = len(u) + len(l)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

// setLocked locks or unlocks the default keyring, as the desktop would.
// With no display for an unlock prompt, unlocking goes through GNOME
// Keyring's interface for tests, with the password scripts/test-linux.sh
// started it with.
func setLocked(t *testing.T, locked bool) {
	t.Helper()
	if err := withSecretService(func(s *secretService) error {
		var coll dbus.ObjectPath
		if err := s.call(ssPath, ssService+".ReadAlias", []any{&coll}, "default"); err != nil {
			return err
		}
		if !locked {
			password := ssSecret{Session: s.session, Parameters: []byte{}, Value: []byte("test"), ContentType: "text/plain"}
			return s.call(ssPath, "org.gnome.keyring.InternalUnsupportedGuiltRiddenInterface.UnlockWithMasterPassword", nil, coll, password)
		}
		var done []dbus.ObjectPath
		var prompt dbus.ObjectPath
		return s.call(ssPath, ssService+".Lock", []any{&done, &prompt}, []dbus.ObjectPath{coll})
	}); err != nil {
		t.Fatalf("setting the keyring locked=%v: %v", locked, err)
	}
}

func TestSecretServiceRoundTrip(t *testing.T) {
	acct := needKeyring(t)
	if _, err := loadTokens(acct); !errors.Is(err, errSignedOut) {
		t.Fatalf("an item that isn't there: %v", err)
	}
	first := &tokens{AccessToken: "a1", RefreshToken: "r1", ExpiresAt: time.Now().Add(time.Hour).Round(time.Second)}
	if err := saveTokens(acct, first); err != nil {
		t.Fatal(err)
	}
	got, err := loadTokens(acct)
	if err != nil || got.AccessToken != "a1" || got.RefreshToken != "r1" || !got.ExpiresAt.Equal(first.ExpiresAt) {
		t.Fatalf("read back %+v, %v", got, err)
	}
	// A refresh replaces the item rather than adding a second.
	if err := saveTokens(acct, &tokens{AccessToken: "a2", RefreshToken: "r2", ExpiresAt: first.ExpiresAt}); err != nil {
		t.Fatal(err)
	}
	if got, err := loadTokens(acct); err != nil || got.RefreshToken != "r2" {
		t.Fatalf("after replacing: %+v, %v", got, err)
	}
	if n := count(t, acct); n != 1 {
		t.Fatalf("%d items after storing twice, want 1", n)
	}
	// Other accounts are other items.
	if _, err := loadTokens(acct + "-other"); !errors.Is(err, errSignedOut) {
		t.Fatalf("another account: %v", err)
	}
	if err := deleteTokens(acct); err != nil {
		t.Fatal(err)
	}
	if _, err := loadTokens(acct); !errors.Is(err, errSignedOut) {
		t.Fatalf("after clearing: %v", err)
	}
	if err := deleteTokens(acct); err != nil {
		t.Fatalf("clearing what's gone: %v", err)
	}
}

// A locked keyring is never "signed out": that would sign out without
// revoking, and forget the workspace while its tokens stay behind.
func TestALockedKeyringIsNotSignedOut(t *testing.T) {
	acct := needKeyring(t)
	if err := saveTokens(acct, &tokens{AccessToken: "a", RefreshToken: "r", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	setLocked(t, true)
	t.Cleanup(func() { setLocked(t, false) })

	// No display here, so the unlock prompt can't be shown: that's a locked
	// keyring, and an error, not an absence.
	if _, err := loadTokens(acct); !errors.Is(err, errKeyringLocked) {
		t.Fatalf("read of a locked item: %v", err)
	}
	if err := deleteTokens(acct); !errors.Is(err, errKeyringLocked) {
		t.Fatalf("deleting a locked item: %v", err)
	}
	if n := count(t, acct); n != 1 {
		t.Fatalf("%d items, want the locked one still there", n)
	}

	setLocked(t, false)
	if got, err := loadTokens(acct); err != nil || got.RefreshToken != "r" {
		t.Fatalf("after unlocking: %+v, %v", got, err)
	}
}

// ── without a keyring: the bus and the browser ────────────────────────────────

func TestNoSessionBusIsAnErrorNotALaunch(t *testing.T) {
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir()) // no bus socket in it
	start := time.Now()
	_, err := keyringLoad("oauth:x")
	if err == nil || errors.Is(err, errSignedOut) || !strings.Contains(err.Error(), "no session bus") {
		t.Fatalf("got %v", err)
	}
	if !errors.Is(err, errNoSecretService) {
		t.Errorf("no bus isn't errNoSecretService: %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("took %v", d)
	}
}

func TestASilentBusIsCutOff(t *testing.T) {
	// A bus that takes the connection and never says a word.
	sock := t.TempDir() + "/bus"
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+sock)
	old := keyringWait
	keyringWait = 500 * time.Millisecond
	t.Cleanup(func() { keyringWait = old })
	start := time.Now()
	if _, err := loadTokens("oauth:x"); err == nil || errors.Is(err, errSignedOut) {
		t.Fatalf("got %v", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("waited %v on a silent bus", d)
	}
}

func TestOpenBrowserReportsAQuickFailure(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	write := func(script string) {
		if err := os.WriteFile(dir+"/xdg-open", []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	old := browserGrace
	browserGrace = 300 * time.Millisecond
	t.Cleanup(func() { browserGrace = old })

	write("exit 3") // "no browser configured"
	if err := openBrowser("https://linear.app"); err == nil {
		t.Error("a failed xdg-open said nothing")
	}
	write("exec /bin/sleep 5") // the browser, in the foreground
	start := time.Now()
	if err := openBrowser("https://linear.app"); err != nil {
		t.Errorf("a browser still open: %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("waited %v for the browser", d)
	}
}

// ── token_store ───────────────────────────────────────────────────────────────

func withTokenStore(t *testing.T, mode string) {
	t.Helper()
	old := tokenStore
	tokenStore = mode
	t.Cleanup(func() { tokenStore = old })
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
}

func noBus(t *testing.T) {
	t.Helper()
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
}

// Without a Secret Service, "auto" keeps the sign-in in the file.
func TestAutoWithoutKeyringUsesTheFile(t *testing.T) {
	withTokenStore(t, "auto")
	noBus(t)
	acct := "oauth:ws"
	if _, err := loadTokens(acct); !errors.Is(err, errSignedOut) {
		t.Fatalf("before: %v", err)
	}
	if err := saveTokens(acct, &tokens{AccessToken: "a", RefreshToken: "r"}); err != nil {
		t.Fatal(err)
	}
	if got, err := loadTokens(acct); err != nil || got.RefreshToken != "r" {
		t.Fatalf("%+v %v", got, err)
	}
	if tokenPlace(acct) != tokenFile(acct) {
		t.Errorf("place %q", tokenPlace(acct))
	}
	if err := deleteTokens(acct); err != nil {
		t.Fatal(err)
	}
	if fileExists(acct) {
		t.Error("still there after deleting")
	}
}

// "keyring" never falls back: no Secret Service is an error.
func TestKeyringModeNeverUsesTheFile(t *testing.T) {
	withTokenStore(t, "keyring")
	noBus(t)
	if err := saveTokens("oauth:ws", &tokens{AccessToken: "a"}); err == nil || !errors.Is(err, errNoSecretService) {
		t.Fatalf("got %v", err)
	}
	if fileExists("oauth:ws") {
		t.Error("wrote the file")
	}
}

// "file" doesn't touch the bus at all.
func TestFileModeSkipsTheKeyring(t *testing.T) {
	withTokenStore(t, "file")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/nonexistent/bus")
	if err := saveTokens("oauth:ws", &tokens{AccessToken: "a"}); err != nil {
		t.Fatal(err)
	}
	if got, err := loadTokens("oauth:ws"); err != nil || got.AccessToken != "a" {
		t.Fatalf("%+v %v", got, err)
	}
}

// With a keyring, "auto" saves there and drops a file copy from an SSH
// sign-in, whose refresh token is now spent.
func TestAutoWithKeyringMovesOffTheFile(t *testing.T) {
	acct := needKeyring(t)
	withTokenStore(t, "auto")
	if err := fileSave(acct, &tokens{AccessToken: "ssh", RefreshToken: "ssh-r"}); err != nil {
		t.Fatal(err)
	}
	if got, err := loadTokens(acct); err != nil || got.AccessToken != "ssh" {
		t.Fatalf("the SSH sign-in isn't read: %+v %v", got, err)
	}
	if err := saveTokens(acct, &tokens{AccessToken: "k", RefreshToken: "k-r"}); err != nil {
		t.Fatal(err)
	}
	if fileExists(acct) {
		t.Error("the file copy is still there")
	}
	if got, err := loadTokens(acct); err != nil || got.AccessToken != "k" {
		t.Fatalf("%+v %v", got, err)
	}
	if tokenPlace(acct) != "your keyring" {
		t.Errorf("place %q", tokenPlace(acct))
	}
}
