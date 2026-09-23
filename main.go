// herdr-linear: your Linear issues and projects in a herdr popup, one key away
// from a worktree for any of them.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"
)

const usage = `herdr-linear — Linear issues and projects in herdr

  action open      what the herdr action runs: opens the picker popup
  action logout    what the herdr action runs: signs out, with a toast
  action demo      what the herdr action runs: the picker on a fictional workspace
  picker [--demo]  the popup itself; --demo (or HERDR_LINEAR_DEMO=1) shows a
                   fictional workspace: no account, no network, safe to screenshot
  login            sign in from a terminal (the picker also offers this)
  logout           revoke access at Linear, then forget the tokens
  logout --local   only forget the tokens here, without revoking
  status           show whether you're signed in
  kickoff W P      (internal) wait for pane P's agent in workspace W, then
                   send it the prompt read from stdin
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "herdr-linear:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return nil
	}
	cfg, cfgErr := loadConfig()
	// Signing out and checking status must work even with a broken config
	// (they fall back to the defaults); everything else needs a sound one.
	switch {
	case cfgErr == nil:
	case args[0] == "logout" || args[0] == "status" || (len(args) > 1 && args[0] == "action" && args[1] == "logout"):
		fmt.Fprintln(os.Stderr, "herdr-linear: ignoring", cfgErr)
	default:
		if args[0] == "action" {
			notify("Linear", cfgErr.Error())
		}
		return cfgErr
	}

	switch args[0] {
	case "action":
		if len(args) < 2 {
			return errors.New("action needs a name")
		}
		return runAction(ctx, cfg, args[1])
	case "picker":
		demo := os.Getenv("HERDR_LINEAR_DEMO") == "1" || (len(args) > 1 && args[1] == "--demo")
		return runPicker(ctx, cfg, demo)
	case "login":
		if err := login(ctx, cfg, func(s string) { fmt.Println(s) }); err != nil {
			return err
		}
		fmt.Println("Signed in to Linear.")
		return nil
	case "logout":
		local := len(args) > 1 && args[1] == "--local"
		switch err := logout(ctx, cfg, local); {
		case errors.Is(err, errNotSignedIn):
			fmt.Println("Not signed in.")
		case err != nil:
			return err
		case local:
			fmt.Println("Forgot the tokens on this Mac. Access wasn't revoked at Linear; do that in Linear's settings if you need to.")
		default:
			fmt.Println("Signed out: access revoked at Linear and forgotten here.")
		}
		return nil
	case "status":
		t, err := loadTokens()
		if errors.Is(err, errSignedOut) {
			fmt.Println("Not signed in.")
			return nil
		} else if err != nil {
			return err
		}
		fmt.Printf("Signed in. Access token valid until %s (refreshes automatically).\n", t.ExpiresAt.Local().Format(time.RFC1123))
		return nil
	case "kickoff":
		if len(args) != 3 {
			return errors.New("kickoff needs: workspace pane (and the prompt on stdin)")
		}
		text, err := io.ReadAll(io.LimitReader(os.Stdin, 64<<10))
		if err != nil || len(text) == 0 {
			return errors.New("kickoff: no prompt on stdin")
		}
		return kickoff(cfg, args[1], args[2], string(text))
	default:
		fmt.Print(usage)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runAction(ctx context.Context, cfg config, name string) error {
	switch name {
	case "open":
		inv := invocationContext()
		cwd := inv.WorkspaceCwd
		if cwd == "" {
			cwd = inv.FocusedPaneCwd
		}
		if err := openPopup("picker", "80%", "70%", map[string]string{"HERDR_LINEAR_CWD": cwd}); err != nil {
			notify("Linear", "Couldn't open the picker: "+err.Error())
			return err
		}
		return nil
	case "logout":
		switch err := logout(ctx, cfg, false); {
		case errors.Is(err, errNotSignedIn):
			notify("Linear", "Not signed in.")
		case err != nil:
			notify("Linear", "Not signed out: "+err.Error())
			return err
		default:
			notify("Linear", "Signed out: access revoked at Linear.")
		}
		return nil
	case "demo":
		if err := openPopup("picker", "80%", "70%", map[string]string{"HERDR_LINEAR_DEMO": "1"}); err != nil {
			notify("Linear", "Couldn't open the demo: "+err.Error())
			return err
		}
		return nil
	}
	return fmt.Errorf("unknown action %q", name)
}
