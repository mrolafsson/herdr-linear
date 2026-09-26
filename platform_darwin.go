package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// macOS: tokens in the login keychain through `security`, the browser through
// `open`, the clipboard through `pbcopy`.

// keyringWait bounds each use of the keychain, an unlock prompt included, so
// nothing waits forever on it (while holding the token lock, say).
var keyringWait = 90 * time.Second

// helperWait bounds `open` and `pbcopy`, which run while the picker waits.
const helperWait = 5 * time.Second

// runBounded runs a command with stdin, killed after wait. combined returns stderr
// with stdout, for `security -i`, which reports failures there.
func runBounded(wait time.Duration, stdin string, combined bool, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var out []byte
	var err error
	if combined {
		out, err = cmd.CombinedOutput()
	} else {
		out, err = cmd.Output()
	}
	if ctx.Err() != nil {
		return out, fmt.Errorf("%s didn't finish within %v", name, wait)
	}
	return out, err
}

func loadTokens(acct string) (*tokens, error) {
	out, err := runBounded(keyringWait, "", false, "security", "find-generic-password", "-s", keychainService, "-a", acct, "-w")
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
func saveTokens(acct string, t *tokens) error {
	data, err := json.Marshal(t)
	if err != nil {
		return err
	}
	out, err := runBounded(keyringWait, fmt.Sprintf("add-generic-password -U -s %s -a %s -l \"herdr Linear\" -X %s\n",
		keychainService, acct, hex.EncodeToString(data)), true, "security", "-i")
	// `security -i` exits 0 even when a command fails; it reports on output.
	if err != nil || strings.Contains(string(out), "returned") {
		msg := strings.TrimSpace(string(out))
		if err != nil && msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("keychain write failed: %s", msg)
	}
	return nil
}

func deleteTokens(acct string) error {
	_, err := runBounded(keyringWait, "", false, "security", "delete-generic-password", "-s", keychainService, "-a", acct)
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 44 {
		return nil
	}
	return err
}

// tokenPlace says where acct's sign-in is kept, for status.
func tokenPlace(string) string { return "your login keychain" }

// canStore: the login keychain is always there (a locked one asks).
func canStore() error { return nil }

// strayTokens: a Mac keeps one copy, in the keychain.
func strayTokens(string) *tokens { return nil }

// remoteSession: a browser can't be opened where you are, so sign-in shows
// the link instead. On a Mac, that's over SSH.
func remoteSession() bool { return overSSH() }

func openBrowser(u string) error {
	_, err := runBounded(helperWait, "", false, "open", u)
	return err
}

func copyLocal(s string) error {
	_, err := runBounded(helperWait, s, false, "pbcopy")
	return err
}
