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

func withSecretService(f func(*secretService) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), keyringWait)
	defer cancel()
	conn, err := dbus.ConnectSessionBus(dbus.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("no Secret Service: can't reach the D-Bus session bus (%w); herdr-linear keeps its sign-in in your keyring, such as GNOME Keyring or KWallet", err)
	}
	defer conn.Close()
	s := &secretService{ctx: ctx, conn: conn}
	// "plain": the secret crosses the session bus unencrypted, as with most
	// clients; only processes running as you can see that bus.
	var out dbus.Variant
	if err := s.call(ssPath, ssService+".OpenSession", []any{&out, &s.session}, "plain", dbus.MakeVariant("")); err != nil {
		return fmt.Errorf("no Secret Service (%w); herdr-linear keeps its sign-in in your keyring, such as GNOME Keyring or KWallet", err)
	}
	defer func() { _ = s.call(s.session, ssSession+".Close", nil) }()
	err = f(s)
	if ctx.Err() != nil && err != nil {
		return fmt.Errorf("your keyring didn't answer within %v: %w", keyringWait, err)
	}
	return err
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
	dismissed, _, err := s.prompt(prompt)
	if err != nil {
		return err
	}
	if dismissed {
		return errKeyringLocked
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
		case sig := <-ch:
			if sig.Path != p || sig.Name != ssPrompt+".Completed" || len(sig.Body) < 2 {
				continue
			}
			d, _ := sig.Body[0].(bool)
			r, _ := sig.Body[1].(dbus.Variant)
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

func loadTokens(acct string) (*tokens, error) {
	var t tokens
	err := withSecretService(func(s *secretService) error {
		items, err := s.items(acct)
		if err != nil {
			return err
		}
		// Normally one; saveTokens removes any other. If there are more
		// anyway, the last written is the latest refresh.
		newest := items[0]
		for _, it := range items[1:] {
			if s.modified(it) > s.modified(newest) {
				newest = it
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

// saveTokens replaces acct's item in the default keyring, then removes any
// other copy (in another keyring, say), so a stale one can't be read later.
func saveTokens(acct string, t *tokens) error {
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

// deleteTokens removes every item for acct, locked ones too (unlocking them
// may prompt), and checks none is left. None there to begin with is fine.
func deleteTokens(acct string) error {
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

// openBrowser starts xdg-open without waiting for it: with some desktops it
// lasts as long as the browser does.
func openBrowser(u string) error {
	cmd := exec.Command("xdg-open", u)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
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
