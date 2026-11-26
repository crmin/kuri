package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/crmin/kuri/internal/cli"
	"github.com/crmin/kuri/internal/daemon"
	"github.com/crmin/kuri/internal/launchd"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 1 && args[0] == "help" {
		if err := cli.WriteHelp(os.Stdout); err != nil {
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
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	switch {
	case len(args) == 0:
		err = app.RunForeground(ctx)
	case len(args) == 1 && args[0] == "start":
		var pid int
		pid, err = app.Start(ctx)
		if err == nil {
			fmt.Printf("Started kuri (PID %d).\n", pid)
		}
	case len(args) == 1 && args[0] == "stop":
		var pid int
		pid, err = app.Stop(ctx)
		if errors.Is(err, daemon.ErrNoBackground) {
			fmt.Fprintln(os.Stdout, err)
			return 1
		}
		if err == nil {
			fmt.Printf("Stopped kuri (PID %d).\n", pid)
		}
	case len(args) == 1 && args[0] == "state":
		var state daemon.State
		state, err = app.State(ctx)
		if err == nil {
			err = cli.WriteState(os.Stdout, state, time.Now())
		}
	case len(args) == 1 && args[0] == "__background":
		err = app.RunBackground(ctx, os.NewFile(3, "readiness"))
	case len(args) >= 1 && args[0] == "__launchd" && (len(args) == 1 || (len(args) == 2 && args[1] == "--eager")):
		var sockets launchd.Sockets
		sockets, err = launchd.Acquire()
		if err == nil {
			err = app.RunActivated(ctx, sockets, len(args) == 2)
		}
	default:
		err = errors.New("Unknown kuri command. Run kuri help to see the available commands.")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
