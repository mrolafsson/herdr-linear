package main

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
)

func TestThePickerNotesItself(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	forget := notePopup()
	data, _ := os.ReadFile(popupFile())
	if string(data) != strconv.Itoa(os.Getpid()) {
		t.Fatalf("noted %q", data)
	}
	// A newer picker took over: this one leaving mustn't forget it.
	os.WriteFile(popupFile(), []byte("999999"), 0o600)
	forget()
	if data, _ := os.ReadFile(popupFile()); string(data) != "999999" {
		t.Errorf("forgot the newer picker: %q", data)
	}
}

func TestOnlyOurOwnPickerIsEnded(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	if endOwnPopup() {
		t.Error("no picker noted, but one was ended")
	}
	sleep := exec.Command("sleep", "30")
	if err := sleep.Start(); err != nil {
		t.Skip("no sleep")
	}
	t.Cleanup(func() { sleep.Process.Kill(); sleep.Wait() })
	os.MkdirAll(stateDir(), 0o700)
	os.WriteFile(popupFile(), []byte(strconv.Itoa(sleep.Process.Pid)), 0o600)
	if endOwnPopup() {
		t.Error("ended a process that isn't the picker")
	}
	if sleep.Process.Signal(syscall.Signal(0)) != nil {
		t.Error("the stranger was ended")
	}
}
