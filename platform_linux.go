package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Linux: tokens in the Secret Service (GNOME Keyring, KWallet, KeePassXC…)
// through `secret-tool`, the browser through `xdg-open`, the clipboard
// through whichever of wl-copy, xclip and xsel is there.

// secretTool runs secret-tool with the item's attributes. What it can't
// do is an error saying why; lookup's "no such item" (exit 1, nothing on
// stderr) is not, and comes back as found == false.
func secretTool(stdin string, args ...string) (out string, found bool, err error) {
	cmd := exec.Command("secret-tool", args...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	if errors.Is(err, exec.ErrNotFound) {
		return "", false, errors.New("secret-tool isn't installed: install libsecret-tools (Debian, Ubuntu) or libsecret (Fedora, Arch), and run a Secret Service such as GNOME Keyring or KWallet")
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 && strings.TrimSpace(stderr.String()) == "" {
		return "", false, nil
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", false, fmt.Errorf("Secret Service: %s", msg)
	}
	return stdout.String(), true, nil
}

func loadTokens(acct string) (*tokens, error) {
	out, found, err := secretTool("", "lookup", "service", keychainService, "account", acct)
	if err != nil {
		return nil, fmt.Errorf("keyring read: %w", err)
	}
	if !found {
		return nil, errSignedOut
	}
	var t tokens
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &t); err != nil || t.AccessToken == "" {
		return nil, errSignedOut
	}
	return &t, nil
}

// saveTokens hands the secret to secret-tool on stdin, never argv (which
// every local user can read in /proc).
func saveTokens(acct string, t *tokens) error {
	data, err := json.Marshal(t)
	if err != nil {
		return err
	}
	if _, _, err := secretTool(string(data), "store", "--label=herdr Linear", "service", keychainService, "account", acct); err != nil {
		return fmt.Errorf("keyring write failed: %w", err)
	}
	return nil
}

// deleteTokens: clearing an item that isn't there succeeds.
func deleteTokens(acct string) error {
	if _, _, err := secretTool("", "clear", "service", keychainService, "account", acct); err != nil {
		return fmt.Errorf("keyring delete failed: %w", err)
	}
	return nil
}

func openBrowser(u string) error {
	return exec.Command("xdg-open", u).Run()
}

func copyText(s string) error {
	var tries [][]string
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		tries = append(tries, []string{"wl-copy"})
	}
	tries = append(tries, []string{"xclip", "-selection", "clipboard"}, []string{"xsel", "--clipboard", "--input"})
	for _, c := range tries {
		if _, err := exec.LookPath(c[0]); err != nil {
			continue
		}
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Stdin = strings.NewReader(s)
		return cmd.Run()
	}
	return errors.New("no clipboard tool: install wl-clipboard (Wayland), xclip or xsel")
}
