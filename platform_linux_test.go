package main

import (
	"errors"
	"os"
	"testing"
	"time"
)

// TestSecretServiceRoundTrip stores, reads, replaces and clears tokens in a
// real Secret Service through secret-tool. It needs one running and
// unlocked, so it only runs with HERDR_LINEAR_KEYRING_TEST=1 (CI's Linux job
// starts GNOME Keyring for it).
func TestSecretServiceRoundTrip(t *testing.T) {
	if os.Getenv("HERDR_LINEAR_KEYRING_TEST") != "1" {
		t.Skip("set HERDR_LINEAR_KEYRING_TEST=1 with a Secret Service running")
	}
	acct := "oauth:test-" + time.Now().Format("150405.000000")
	t.Cleanup(func() { _ = deleteTokens(acct) })

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
