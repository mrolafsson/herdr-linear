package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The sign-in in a file, for Linux without a keyring (over SSH, on a server):
// one per workspace, in the state directory, readable only by you. Like
// ~/.ssh or gh's hosts.yml, it's as safe as your account and the disk: not
// encrypted, and in your backups. See token_store in the README.

func tokenFile(acct string) string {
	name := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return '_'
	}, acct)
	return filepath.Join(stateDir(), "tokens", name+".json")
}

func fileLoad(acct string) (*tokens, error) {
	p := tokenFile(acct)
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errSignedOut
	} else if err != nil {
		return nil, fmt.Errorf("token file: %w", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("token file: %w", err)
	}
	// As ssh does with a key: one others can read isn't used, so it's noticed.
	if fi.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s can be read by other users: `chmod 600` it, or sign out and in again", p)
	}
	var t tokens
	if err := json.NewDecoder(f).Decode(&t); err != nil || t.AccessToken == "" {
		return nil, errSignedOut
	}
	return &t, nil
}

// fileSave writes the whole file anew and renames it into place, so a crash
// leaves the old tokens or the new, never half of either.
func fileSave(acct string, t *tokens) error {
	p := tokenFile(acct)
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("token file: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("token file: %w", err)
	}
	data, err := json.Marshal(t)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tokens-*")
	if err != nil {
		return fmt.Errorf("token file: %w", err)
	}
	defer os.Remove(tmp.Name()) // gone after the rename; left behind otherwise
	// CreateTemp makes it 0600 already; said again in case that ever changes.
	err = tmp.Chmod(0o600)
	if err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), p)
	}
	if err != nil {
		return fmt.Errorf("token file: %w", err)
	}
	return nil
}

func fileDelete(acct string) error {
	if err := os.Remove(tokenFile(acct)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("token file: %w", err)
	}
	return nil
}

func fileExists(acct string) bool {
	_, err := os.Stat(tokenFile(acct))
	return err == nil
}
