package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTokenFileRoundTrip(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	acct := account("0b6e0c8e-1c1a-4f5e-9b1e-3f1f0a2b3c4d")
	if _, err := fileLoad(acct); !errors.Is(err, errSignedOut) {
		t.Fatalf("before: %v", err)
	}
	want := &tokens{AccessToken: "a", RefreshToken: "r", ExpiresAt: time.Now().Add(time.Hour).Round(time.Second)}
	if err := fileSave(acct, want); err != nil {
		t.Fatal(err)
	}
	got, err := fileLoad(acct)
	if err != nil || got.AccessToken != "a" || got.RefreshToken != "r" || !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Fatalf("%+v %v", got, err)
	}
	p := tokenFile(acct)
	if strings.ContainsAny(filepath.Base(p), ":/") {
		t.Errorf("file name %q", filepath.Base(p))
	}
	for path, perm := range map[string]os.FileMode{p: 0o600, filepath.Dir(p): 0o700} {
		fi, err := os.Stat(path)
		if err != nil || fi.Mode().Perm() != perm {
			t.Errorf("%s: %v %v, want %v", path, fi.Mode().Perm(), err, perm)
		}
	}
	// Rewritten, not appended to; no temp files left behind.
	if err := fileSave(acct, &tokens{AccessToken: "a2", RefreshToken: "r2"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := fileLoad(acct); got.RefreshToken != "r2" {
		t.Errorf("after rewrite: %+v", got)
	}
	if es, _ := os.ReadDir(filepath.Dir(p)); len(es) != 1 {
		t.Errorf("%d files in the directory", len(es))
	}
	if err := fileDelete(acct); err != nil {
		t.Fatal(err)
	}
	if err := fileDelete(acct); err != nil {
		t.Errorf("deleting twice: %v", err)
	}
	if _, err := fileLoad(acct); !errors.Is(err, errSignedOut) {
		t.Fatalf("after delete: %v", err)
	}
}

func TestTokenFileOthersCanReadIsRefused(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	if err := fileSave("oauth:x", &tokens{AccessToken: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(tokenFile("oauth:x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := fileLoad("oauth:x"); err == nil || errors.Is(err, errSignedOut) || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("got %v", err)
	}
}

func TestConfigTokenStore(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", dir)
	write := func(s string) {
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{}`)
	if cfg, err := loadConfig(); err != nil || cfg.TokenStore != "auto" {
		t.Errorf("default: %q %v", cfg.TokenStore, err)
	}
	write(`{"token_store": "file"}`)
	if cfg, err := loadConfig(); err != nil || cfg.TokenStore != "file" {
		t.Errorf("file: %q %v", cfg.TokenStore, err)
	}
	write(`{"token_store": "plaintext"}`)
	if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "token_store") {
		t.Errorf("bad value: %v", err)
	}
}
