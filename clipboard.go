package main

import (
	"os"

	"github.com/charmbracelet/x/ansi"
)

// copyText puts s on your clipboard. On this machine that's pbcopy, wl-copy,
// xclip or xsel (copyLocal). Over SSH those reach the remote machine's
// clipboard, not yours, and a server may have none: then your terminal is
// asked to set it (OSC 52), through herdr. Whether it did can't be told, so
// viaTerminal says it was only asked.
func copyText(s string) (viaTerminal bool, err error) {
	if !overSSH() && copyLocal(s) == nil {
		return false, nil
	}
	// One write, so it can't land in the middle of a frame: writes to a file
	// don't interleave.
	_, err = os.Stdout.WriteString(ansi.SetSystemClipboard(s))
	return true, err
}

// copied is the confirmation for copying what.
func copied(what string, viaTerminal bool) string {
	if viaTerminal {
		return "Sent " + what + " to your terminal's clipboard"
	}
	return "Copied " + what
}
