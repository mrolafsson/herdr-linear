package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

// Linux: tokens in the Secret Service (GNOME Keyring, KWallet, KeePassXC…),
// spoken to directly over D-Bus; the browser through `xdg-open`, the
// clipboard through whichever of wl-copy, xclip and xsel is there.
//
// Not through secret-tool: it can't tell a locked item from a missing one
// (both exit 1, silently), and a locked item mistaken for a missing one would
// be signing out without revoking. The Secret Service itself can: searching
// returns the unlocked and the locked matches apart.

const (
	ssName       = "org.freedesktop.secrets"
	ssPath       = dbus.ObjectPath("/org/freedesktop/secrets")
	ssService    = "org.freedesktop.Secret.Service"
	ssCollection = "org.freedesktop.Secret.Collection"
	ssItem       = "org.freedesktop.Secret.Item"
	ssPrompt     = "org.freedesktop.Secret.Prompt"
	ssSession    = "org.freedesktop.Secret.Session"
	noPrompt     = dbus.ObjectPath("/")
)

// keyringWait bounds each use of the keyring, an unlock prompt included, so
// nothing waits forever on it (while holding the token lock, say). A var for
// tests.
var keyringWait = 90 * time.Second

// errNoSecretService: there's no keyring to be had, as opposed to one that's
// locked or failing. With token_store "auto", that's when the sign-in goes
// in a file instead (tokenfile.go).
var errNoSecretService = errors.New("no Secret Service")

// loadTokens, saveTokens and deleteTokens go to the keyring, or the file, as
// token_store says. "auto" uses the keyring when there's a Secret Service,
// and the file when there's none. A keyring that's there but locked is still
// the keyring: it asks to be unlocked, it doesn't fall back.
//
// The file is also read when the keyring has no sign-in: one made over SSH,
// read later from the desktop. Saved to the keyring, the file's copy goes, so
// an older refresh token can't be read from it later.
func loadTokens(acct string) (*tokens, error) {
	switch tokenStore {
	case "file":
		return fileLoad(acct)
	case "keyring":
		return keyringLoad(acct)
	}
	t, err := keyringLoad(acct)
	if errors.Is(err, errSignedOut) || errors.Is(err, errNoSecretService) {
		return fileLoad(acct)
	}
	return t, err
}

func saveTokens(acct string, t *tokens) error {
	switch tokenStore {
	case "file":
		return fileSave(acct, t)
	case "keyring":
		return keyringSave(acct, t)
	}
	err := keyringSave(acct, t)
	if errors.Is(err, errNoSecretService) {
		return fileSave(acct, t)
	}
	if err == nil {
		_ = fileDelete(acct)
	}
	return err
}

func deleteTokens(acct string) error {
	switch tokenStore {
	case "file":
		return fileDelete(acct)
	case "keyring":
		return keyringDelete(acct)
	}
	err := keyringDelete(acct)
	if errors.Is(err, errNoSecretService) {
		err = nil
	}
	return errors.Join(err, fileDelete(acct))
}

// tokenPlace says where acct's sign-in is kept, for status.
func tokenPlace(acct string) string {
	if tokenStore == "file" || (tokenStore != "keyring" && fileExists(acct)) {
		return tokenFile(acct)
	}
	return "your keyring"
}

var errKeyringLocked = errors.New("your keyring is locked: unlock it, or accept its unlock prompt, then try again")

// ssSecret is the Secret Service's Secret struct, (oayays).
type ssSecret struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

// secretService is one use of the Secret Service: a private bus connection
// and a session to move secrets over, both gone when the use ends.
type secretService struct {
	ctx     context.Context
	conn    *dbus.Conn
	session dbus.ObjectPath
}

// sessionBusAddress is the session bus to use: DBUS_SESSION_BUS_ADDRESS, or
// the systemd user bus in XDG_RUNTIME_DIR. Never one started for the purpose
// (godbus's fallback runs dbus-launch): a new, empty bus has no keyring on it,
// and would outlive the popup.
func sessionBusAddress() (string, error) {
	if a := os.Getenv("DBUS_SESSION_BUS_ADDRESS"); a != "" {
		return a, nil
	}
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		if fi, err := os.Stat(dir + "/bus"); err == nil && fi.Mode()&os.ModeSocket != 0 {
			return "unix:path=" + dir + "/bus", nil
		}
	}
	return "", errors.New("no session bus: DBUS_SESSION_BUS_ADDRESS isn't set and there's no $XDG_RUNTIME_DIR/bus")
}

// connectSessionBus connects, authenticates and says hello, all within ctx:
// godbus bounds none of the three by itself.
func connectSessionBus(ctx context.Context) (*dbus.Conn, error) {
	addr, err := sessionBusAddress()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errNoSecretService, err)
	}
	type result struct {
		conn *dbus.Conn
		err  error
	}
	done := make(chan result, 1)
	go func() {
		conn, err := dbus.Dial(addr, dbus.WithContext(ctx))
		if err != nil && ctx.Err() == nil {
			// Nothing listening at the address: there's no bus, as above.
			err = fmt.Errorf("%w: %w", errNoSecretService, err)
		} else if err == nil {
			if err = conn.Auth(nil); err == nil {
				err = conn.Hello()
			}
			if err != nil {
				conn.Close()
			}
		}
		done <- result{conn, err}
	}()
	select {
	case r := <-done:
		return r.conn, r.err
	case <-ctx.Done():
		go func() { // it may still connect: don't leave that open
			if r := <-done; r.conn != nil {
				r.conn.Close()
			}
		}()
		return nil, fmt.Errorf("the session bus didn't answer within %v", keyringWait)
	}
}

func withSecretService(f func(*secretService) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), keyringWait)
	defer cancel()
	conn, err := connectSessionBus(ctx)
	if err != nil {
		return fmt.Errorf("no Secret Service: can't reach the D-Bus session bus (%w); herdr-linear keeps its sign-in in your keyring, such as GNOME Keyring or KWallet, which it reaches over your desktop session's bus", err)
	}
	defer conn.Close()
	s := &secretService{ctx: ctx, conn: conn}
	// "plain": the secret crosses the session bus unencrypted, as with most
	// clients; only processes running as you can see that bus.
	var out dbus.Variant
	if err := s.call(ssPath, ssService+".OpenSession", []any{&out, &s.session}, "plain", dbus.MakeVariant("")); err != nil {
		if notThere(err) {
			err = fmt.Errorf("%w: %w", errNoSecretService, err)
		}
		return fmt.Errorf("no Secret Service (%w); herdr-linear keeps its sign-in in your keyring, such as GNOME Keyring or KWallet", err)
	}
	defer func() { _ = s.call(s.session, ssSession+".Close", nil) }()
	err = f(s)
	if ctx.Err() != nil && err != nil {
		return fmt.Errorf("your keyring didn't answer within %v: %w", keyringWait, err)
	}
	return err
}

// notThere says the bus has no Secret Service on it, and none it can start:
// not a keyring that's slow, locked or failing, which must not be taken for
// no keyring at all (the sign-in would go to a file, and the keyring's copy
// be left behind).
func notThere(err error) bool {
	var e dbus.Error
	if !errors.As(err, &e) {
		return false
	}
	return e.Name == "org.freedesktop.DBus.Error.ServiceUnknown" ||
		e.Name == "org.freedesktop.DBus.Error.NameHasNoOwner" ||
		strings.HasPrefix(e.Name, "org.freedesktop.DBus.Error.Spawn.")
}

func (s *secretService) call(path dbus.ObjectPath, method string, out []any, args ...any) error {
	call := s.conn.Object(ssName, path).CallWithContext(s.ctx, method, 0, args...)
	if call.Err != nil {
		return call.Err
	}
	if len(out) == 0 {
		return nil
	}
	return call.Store(out...)
}

func attrs(acct string) map[string]string {
	return map[string]string{"service": keychainService, "account": acct}
}

// search returns the items for acct, unlocked and locked.
func (s *secretService) search(acct string) (unlocked, locked []dbus.ObjectPath, err error) {
	err = s.call(ssPath, ssService+".SearchItems", []any{&unlocked, &locked}, attrs(acct))
	return unlocked, locked, err
}

// unlock unlocks objects, through the keyring's own prompt if it asks for
// one. A prompt that's dismissed, or can't be shown, is errKeyringLocked.
func (s *secretService) unlock(objects []dbus.ObjectPath) error {
	if len(objects) == 0 {
		return nil
	}
	var done []dbus.ObjectPath
	var prompt dbus.ObjectPath
	if err := s.call(ssPath, ssService+".Unlock", []any{&done, &prompt}, objects); err != nil {
		return err
	}
	if prompt == noPrompt {
		if len(done) < len(objects) {
			return errKeyringLocked
		}
		return nil
	}
	dismissed, result, err := s.prompt(prompt)
	if err != nil {
		return err
	}
	if dismissed {
		return errKeyringLocked
	}
	// The prompt's result is what it unlocked: everything asked for, or it's
	// still locked.
	got, _ := result.Value().([]dbus.ObjectPath)
	unlocked := map[dbus.ObjectPath]bool{}
	for _, o := range append(done, got...) {
		unlocked[o] = true
	}
	for _, o := range objects {
		if !unlocked[o] {
			return errKeyringLocked
		}
	}
	return nil
}

// prompt shows a Secret Service prompt and waits for it to complete.
func (s *secretService) prompt(p dbus.ObjectPath) (dismissed bool, result dbus.Variant, err error) {
	opts := []dbus.MatchOption{dbus.WithMatchObjectPath(p), dbus.WithMatchInterface(ssPrompt), dbus.WithMatchMember("Completed")}
	if err := s.conn.AddMatchSignalContext(s.ctx, opts...); err != nil {
		return false, result, err
	}
	defer func() { _ = s.conn.RemoveMatchSignalContext(context.Background(), opts...) }()
	ch := make(chan *dbus.Signal, 4)
	s.conn.Signal(ch)
	defer s.conn.RemoveSignal(ch)
	if err := s.call(p, ssPrompt+".Prompt", nil, ""); err != nil {
		return false, result, err
	}
	for {
		select {
		case sig, ok := <-ch:
			if !ok {
				// godbus closes it when the connection goes: on timeout, or
				// if the bus itself went away mid-prompt.
				if s.ctx.Err() != nil {
					return true, result, errKeyringLocked
				}
				return true, result, errors.New("the session bus went away while the keyring was asking")
			}
			if sig == nil || sig.Path != p || sig.Name != ssPrompt+".Completed" || len(sig.Body) < 2 {
				continue
			}
			d, okD := sig.Body[0].(bool)
			r, okR := sig.Body[1].(dbus.Variant)
			if !okD || !okR {
				return true, result, errors.New("the keyring's prompt answered in a form it shouldn't")
			}
			return d, r, nil
		case <-s.ctx.Done():
			_ = s.conn.Object(ssName, p).Call(ssPrompt+".Dismiss", 0)
			return true, result, errKeyringLocked
		}
	}
}

// items is every item for acct, unlocked: unlocking the locked ones may
// prompt. None at all is errSignedOut, the only way to be signed out here.
func (s *secretService) items(acct string) ([]dbus.ObjectPath, error) {
	unlocked, locked, err := s.search(acct)
	if err != nil {
		return nil, err
	}
	if len(unlocked)+len(locked) == 0 {
		return nil, errSignedOut
	}
	if err := s.unlock(locked); err != nil {
		return nil, err
	}
	return append(unlocked, locked...), nil
}

func (s *secretService) modified(item dbus.ObjectPath) uint64 {
	v, err := s.conn.Object(ssName, item).GetProperty(ssItem + ".Modified")
	if err != nil {
		return 0
	}
	m, _ := v.Value().(uint64)
	return m
}

func keyringLoad(acct string) (*tokens, error) {
	var t tokens
	err := withSecretService(func(s *secretService) error {
		unlocked, locked, err := s.search(acct)
		if err != nil {
			return err
		}
		items := unlocked
		switch {
		case len(unlocked)+len(locked) == 0:
			return errSignedOut
		case len(unlocked) == 0:
			// Only locked copies: unlock them (which may prompt).
			if err := s.unlock(locked); err != nil {
				return err
			}
			items = locked
		}
		// An unlocked copy is used as is: keyringSave writes to the default
		// keyring, unlocking it first, so a locked copy elsewhere is an old one
		// it couldn't tidy away, and needn't block this read.
		//
		// Normally there's one. If there are more anyway, the last written is
		// the latest refresh; ties go by path, so it's the same one each time.
		newest, newestAt := items[0], s.modified(items[0])
		for _, it := range items[1:] {
			if at := s.modified(it); at > newestAt || (at == newestAt && it > newest) {
				newest, newestAt = it, at
			}
		}
		var sec ssSecret
		if err := s.call(newest, ssItem+".GetSecret", []any{&sec}, s.session); err != nil {
			return err
		}
		if err := json.Unmarshal(sec.Value, &t); err != nil || t.AccessToken == "" {
			return errSignedOut
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, errSignedOut) {
			return nil, err
		}
		return nil, fmt.Errorf("keyring read: %w", err)
	}
	return &t, nil
}

// keyringSave replaces acct's item in the default keyring, then removes any
// other copy (in another keyring, say), so a stale one can't be read later.
func keyringSave(acct string, t *tokens) error {
	data, err := json.Marshal(t)
	if err != nil {
		return err
	}
	err = withSecretService(func(s *secretService) error {
		var coll dbus.ObjectPath
		if err := s.call(ssPath, ssService+".ReadAlias", []any{&coll}, "default"); err != nil {
			return err
		}
		if coll == noPrompt {
			return errors.New("there's no default keyring to keep the sign-in in")
		}
		if err := s.unlock([]dbus.ObjectPath{coll}); err != nil {
			return err
		}
		props := map[string]dbus.Variant{
			ssItem + ".Label":      dbus.MakeVariant("herdr Linear"),
			ssItem + ".Attributes": dbus.MakeVariant(attrs(acct)),
		}
		secret := ssSecret{Session: s.session, Parameters: []byte{}, Value: data, ContentType: "text/plain"}
		var item, prompt dbus.ObjectPath
		if err := s.call(coll, ssCollection+".CreateItem", []any{&item, &prompt}, props, secret, true); err != nil {
			return err
		}
		if prompt != noPrompt {
			dismissed, result, err := s.prompt(prompt)
			if err != nil {
				return err
			}
			if dismissed {
				return errKeyringLocked
			}
			item, _ = result.Value().(dbus.ObjectPath)
		}
		if !item.IsValid() || item == noPrompt {
			// Stored, but the keyring didn't say as what: tidying copies
			// away without knowing which one is new could delete it.
			return nil
		}
		unlocked, locked, err := s.search(acct)
		if err != nil {
			return nil // stored; the copies are only tidying
		}
		for _, other := range append(unlocked, locked...) {
			if other != item {
				_ = s.remove(other)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("keyring write failed: %w", err)
	}
	return nil
}

func (s *secretService) remove(item dbus.ObjectPath) error {
	var prompt dbus.ObjectPath
	if err := s.call(item, ssItem+".Delete", []any{&prompt}); err != nil {
		return err
	}
	if prompt != noPrompt {
		if dismissed, _, err := s.prompt(prompt); err != nil {
			return err
		} else if dismissed {
			return errKeyringLocked
		}
	}
	return nil
}

// keyringDelete removes every item for acct, locked ones too (unlocking them
// may prompt), and checks none is left. None there to begin with is fine.
func keyringDelete(acct string) error {
	err := withSecretService(func(s *secretService) error {
		items, err := s.items(acct)
		if errors.Is(err, errSignedOut) {
			return nil
		} else if err != nil {
			return err
		}
		for _, it := range items {
			if err := s.remove(it); err != nil {
				return err
			}
		}
		unlocked, locked, err := s.search(acct)
		if err != nil {
			return err
		}
		if n := len(unlocked) + len(locked); n > 0 {
			return fmt.Errorf("%d item(s) are still there after deleting", n)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("keyring delete failed: %w", err)
	}
	return nil
}

// remoteSession: a browser can't be opened where you are, so sign-in shows
// the link instead: over SSH, or with no display to open one on (where
// xdg-open would fall back to a text browser, in the terminal the popup is
// using, or hang).
func remoteSession() bool {
	return overSSH() || (os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "")
}

// browserGrace is how long openBrowser waits for xdg-open to fail.
var browserGrace = 2 * time.Second

// openBrowser runs xdg-open without waiting for it to end: with some
// desktops it lasts as long as the browser does. A failure within
// browserGrace ("no browser", say) is reported, so sign-in can show the URL
// to open by hand; one still running then is taken as the browser opening.
func openBrowser(u string) error {
	cmd := exec.Command("xdg-open", u)
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("xdg-open: %w", err)
		}
		return nil
	case <-time.After(browserGrace):
		return nil
	}
}

// helperWait bounds a clipboard helper, which runs while the picker waits.
const helperWait = 5 * time.Second

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
		ctx, cancel := context.WithTimeout(context.Background(), helperWait)
		defer cancel()
		cmd := exec.CommandContext(ctx, c[0], c[1:]...)
		cmd.Stdin = strings.NewReader(s)
		return cmd.Run()
	}
	return errors.New("no clipboard tool: install wl-clipboard (Wayland), xclip or xsel")
}
