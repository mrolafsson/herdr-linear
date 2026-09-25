package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// herdr shows one popup at a time, open for as long as its command runs, on
// whichever client counts as active. With several clients attached (another
// terminal, a mosh session) the picker can be left open on one you're not
// looking at, and every open after fails with ui_busy. So the picker notes
// its pid, and an open that meets ui_busy ends that picker and opens again,
// here. Another plugin's popup is theirs to close.

// openPicker opens the picker popup, replacing one of ours left open.
func openPicker(env map[string]string) error {
	err := openPopup("picker", "80%", "70%", env)
	if isHerdrCode(err, "ui_busy") {
		if !endOwnPopup() {
			notify("Linear", "Another popup is open: close it (esc) and try again.")
			return err
		}
		err = openPopup("picker", "80%", "70%", env)
	}
	return err
}

// popupFile holds the running picker's pid.
func popupFile() string { return filepath.Join(stateDir(), "popup.pid") }

// notePopup records this process as the picker, and returns a func that
// forgets it again, unless a newer picker has replaced it.
func notePopup() func() {
	pid := strconv.Itoa(os.Getpid())
	if os.MkdirAll(stateDir(), 0o700) != nil || os.WriteFile(popupFile(), []byte(pid), 0o600) != nil {
		return func() {}
	}
	return func() {
		if data, err := os.ReadFile(popupFile()); err == nil && strings.TrimSpace(string(data)) == pid {
			os.Remove(popupFile())
		}
	}
}

// endOwnPopup ends our picker if one is running, and waits for it to go:
// whether it was ours to end.
func endOwnPopup() bool {
	data, err := os.ReadFile(popupFile())
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 1 || !isOwnPopup(pid) {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil || p.Signal(syscall.SIGTERM) != nil {
		return false
	}
	for range 20 {
		if p.Signal(syscall.Signal(0)) != nil {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return p.Signal(syscall.Signal(0)) != nil
}

// isOwnPopup checks the process is this program, not a stranger given the
// pid after ours exited.
func isOwnPopup(pid int) bool {
	self, err := os.Executable()
	if err != nil {
		return false
	}
	exe, err := processExe(pid)
	if err != nil {
		return false
	}
	fa, errA := os.Stat(exe)
	fb, errB := os.Stat(self)
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}

// processExe is the program a process runs: /proc on Linux, else ps (the
// path it was started by, the absolute bin/ path herdr gave the picker).
func processExe(pid int) (string, error) {
	if exe, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe"); err == nil {
		return exe, nil
	}
	out, err := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(pid)).Output()
	return strings.TrimSpace(string(out)), err
}
