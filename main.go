package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/crmin/kuri/internal/accesslog"
	"github.com/crmin/kuri/internal/cli"
	"github.com/crmin/kuri/internal/credential"
	"github.com/crmin/kuri/internal/daemon"
	"github.com/crmin/kuri/internal/launchd"
	"github.com/crmin/kuri/internal/menuview"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) > 0 && args[0] == credential.Argument {
		if err := credential.Execute(args[1:]); err != nil {
			// A stopped plugin must exit even if stderr shares a blocked console.
			diagnostic := accesslog.NewConsole(os.Stderr)
			diagnostic.Start()
			diagnostic.Enqueue([]byte(err.Error() + "\n"))
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			_ = diagnostic.Close(ctx)
			return 1
		}
		return 0
	}
	if len(args) == 1 && args[0] == "help" {
		if err := cli.WriteHelp(os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if len(args) == 1 && (args[0] == "install" || args[0] == "uninstall") {
		operation := launchd.InstallStartup
		if args[0] == "uninstall" {
			operation = launchd.UninstallStartup
		}
		if err := operation(ctx, os.Stdout, os.Stderr); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Could not determine the user home directory:", err)
		return 1
	}
	app := daemon.New(home, launchd.ControlPath(os.Geteuid()), os.Stdout, os.Stderr)

	switch {
	case len(args) == 0:
		paused, pauseErr := launchd.MenuPaused()
		err = pauseErr
		if err == nil && paused {
			err = errors.New("Run kuri start to resume the paused registration.")
		}
		if err == nil {
			err = app.RunForeground(ctx)
		}
	case len(args) == 1 && args[0] == "start" && os.Getenv("KURI_LAUNCHD_START") == "1":
		err = runActivated(ctx, app, true)
	case len(args) == 1 && args[0] == "start":
		var pid int
		pid, err = startBackground(ctx, app, home)
		if err == nil {
			fmt.Printf("Started kuri (PID %d).\n", pid)
		}
	case len(args) == 1 && args[0] == "__auto_start":
		_, err = startBackgroundWithOrigin(ctx, app, home, true)
		if errors.Is(err, daemon.ErrAutoStartDisabled) {
			err = nil
		}
	case len(args) == 1 && args[0] == "__settings_version":
		fmt.Fprintln(os.Stdout, "1")
	case len(args) == 1 && args[0] == "stop":
		var pid int
		pid, err = stopBackground(ctx, app, home)
		if errors.Is(err, daemon.ErrNoBackground) {
			fmt.Fprintln(os.Stdout, err)
			return 1
		}
		if err == nil {
			fmt.Printf("Stopped kuri (PID %d).\n", pid)
		}
	case len(args) == 1 && args[0] == "status":
		var state daemon.State
		state, err = app.State(ctx)
		if err == nil {
			err = cli.WriteState(os.Stdout, state, time.Now())
		}
	case len(args) == 1 && args[0] == "__menu_state":
		var view menuview.State
		view, err = app.MenuState(ctx)
		if err == nil {
			err = json.NewEncoder(os.Stdout).Encode(view)
		}
	case len(args) >= 1 && args[0] == "__menu_quit":
		err = quitMenu(ctx, app, home, args[1:])
	case len(args) == 1 && args[0] == "__background":
		err = app.RunBackground(ctx, os.NewFile(3, "readiness"))
	case len(args) >= 1 && args[0] == "__launchd" && (len(args) == 1 || (len(args) == 2 && args[1] == "--eager")):
		err = runActivated(ctx, app, len(args) == 2)
	default:
		err = errors.New("Unknown kuri command. Run kuri help to see the available commands.")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func runActivated(ctx context.Context, app *daemon.App, eager bool) error {
	sockets, err := launchd.Acquire()
	if err != nil {
		return err
	}
	return app.RunActivated(ctx, sockets, eager)
}
