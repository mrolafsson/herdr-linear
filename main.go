// herdr-linear: your Linear issues and projects in a herdr popup, one key away
// from a worktree for any of them.
package main

import (
	"context"
	"errors"
	"fmt"
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
  logout           revoke the grant and forget it
  status           show whether you're signed in
  kickoff W P TXT  (internal) wait for the agent in workspace W, then prompt it
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
	cfg, err := loadConfig()
	if err != nil {
		if len(args) > 1 && args[0] == "action" {
			notify("Linear", err.Error())
		}
		return err
	}

	switch args[0] {
	case "action":
		if len(args) < 2 {
			return errors.New("action needs a name")
		}
		return runAction(ctx, args[1])
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
		if err := logout(ctx); err != nil {
			return err
		}
		fmt.Println("Signed out of Linear.")
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
		if len(args) != 4 {
			return errors.New("kickoff needs: workspace pane text")
		}
		return kickoff(cfg, args[1], args[2], args[3])
	default:
		fmt.Print(usage)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runAction(ctx context.Context, name string) error {
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
		if err := logout(ctx); err != nil {
			notify("Linear", "Sign-out failed: "+err.Error())
			return err
		}
		notify("Linear", "Signed out.")
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
