package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// macOS: tokens in the login keychain through `security`, the browser through
// `open`, the clipboard through `pbcopy`.

func loadTokens(acct string) (*tokens, error) {
	out, err := exec.Command("security", "find-generic-password", "-s", keychainService, "-a", acct, "-w").Output()
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
	cmd := exec.Command("security", "-i")
	cmd.Stdin = strings.NewReader(fmt.Sprintf("add-generic-password -U -s %s -a %s -l \"herdr Linear\" -X %s\n",
		keychainService, acct, hex.EncodeToString(data)))
	out, err := cmd.CombinedOutput()
	// `security -i` exits 0 even when a command fails; it reports on output.
	if err != nil || strings.Contains(string(out), "returned") {
		return fmt.Errorf("keychain write failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func deleteTokens(acct string) error {
	err := exec.Command("security", "delete-generic-password", "-s", keychainService, "-a", acct).Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 44 {
		return nil
	}
	return err
}

func openBrowser(u string) error {
	return exec.Command("open", u).Run()
}

func copyText(s string) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(s)
	return cmd.Run()
}
