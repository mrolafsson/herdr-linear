package main

import (
	"encoding/base64"
	"io"
	"os"
	"strings"
	"testing"
)

// stdout captures what's written to os.Stdout while f runs.
func stdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	f()
	os.Stdout = old
	w.Close()
	out, _ := io.ReadAll(r)
	return string(out)
}

func TestCopyOverSSHAsksTheTerminal(t *testing.T) {
	t.Setenv("SSH_CONNECTION", "10.0.0.1 22 10.0.0.2 22")
	var term bool
	var err error
	out := stdout(t, func() { term, err = copyText("hello") })
	if err != nil || !term {
		t.Fatalf("term=%v err=%v", term, err)
	}
	if want := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("hello")) + "\x07"; out != want {
		t.Fatalf("wrote %q, want %q", out, want)
	}
	if !strings.Contains(copied("the link", term), "terminal") {
		t.Error("says it copied, when it only asked")
	}
}

// Over SSH, "open in Linear" puts the link on your clipboard: a browser
// here wouldn't be in front of you.
func TestOpenRemotelyCopiesTheLink(t *testing.T) {
	t.Setenv("SSH_CONNECTION", "10.0.0.1 22 10.0.0.2 22")
	oldNo, oldB := noBrowser, browse
	noBrowser = func() bool { return true }
	t.Cleanup(func() { noBrowser, browse = oldNo, oldB })
	m := listModel()
	var opening bool
	out := stdout(t, func() { opening = m.openURL("https://linear.app/acme/issue/ENG-1") != nil })
	if opening {
		t.Fatal("opens a browser")
	}
	if !strings.Contains(out, "\x1b]52;c;") || !strings.Contains(m.flash, "open it in your browser") {
		t.Fatalf("out %q, flash %q", out, m.flash)
	}
}
