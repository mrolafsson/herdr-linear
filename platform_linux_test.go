package main

import (
	"errors"
	"os"
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
	acct := "oauth:test-" + time.Now().Format("150405.000000")
	t.Cleanup(func() { _ = deleteTokens(acct) })
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
	if _, err := loadTokens(acct); err == nil || errors.Is(err, errSignedOut) {
		t.Fatalf("read of a locked item: %v", err)
	}
	if err := deleteTokens(acct); err == nil {
		t.Fatal("deleting a locked item said it worked")
	}
	if n := count(t, acct); n != 1 {
		t.Fatalf("%d items, want the locked one still there", n)
	}

	setLocked(t, false)
	if got, err := loadTokens(acct); err != nil || got.RefreshToken != "r" {
		t.Fatalf("after unlocking: %+v, %v", got, err)
	}
}
