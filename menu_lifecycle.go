package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/crmin/kuri/internal/daemon"
	"github.com/crmin/kuri/internal/launchd"
)

func startBackground(ctx context.Context, app *daemon.App, home string) (int, error) {
	lock, err := launchd.LockLifecycle(ctx, home)
	if err != nil {
		return 0, err
	}
	defer lock.Close()
	paused, err := launchd.MenuPaused()
	if err != nil {
		return 0, err
	}
	if !paused {
		return app.Start(ctx)
	}
	if err := launchd.ResumeMenu(ctx, os.Stderr); err != nil {
		return 0, err
	}
	pid, err := app.Start(ctx)
	if errors.Is(err, daemon.ErrAlreadyRunning) {
		state, stateErr := app.State(ctx)
		if stateErr == nil && state.PID > 0 && state.Mode == "background" {
			pid, err = state.PID, nil
		}
	}
	if err != nil {
		rollback, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Minute)
		defer cancel()
		latePID, pauseErr := launchd.PauseMenu(rollback, os.Stderr)
		if pauseErr == nil {
			pauseErr = daemon.WaitForLaunchdExit(rollback, latePID)
		}
		return 0, errors.Join(err, pauseErr)
	}
	return pid, launchd.FinishMenuResume(ctx, os.Stderr)
}

func stopBackground(ctx context.Context, app *daemon.App, home string) (int, error) {
	lock, err := launchd.LockLifecycle(ctx, home)
	if err != nil {
		return 0, err
	}
	defer lock.Close()
	return app.Stop(ctx)
}

func quitMenu(ctx context.Context, app *daemon.App, home string, args []string) error {
	var pid int
	var started time.Time
	if len(args) != 0 {
		if len(args) != 2 {
			return errors.New("Invalid kuri quit request.")
		}
		var err error
		pid, err = strconv.Atoi(args[0])
		if err != nil || pid <= 0 {
			return errors.New("Invalid kuri quit process ID.")
		}
		started, err = time.Parse(time.RFC3339Nano, args[1])
		if err != nil {
			return errors.New("Invalid kuri server start time.")
		}
	}
	lock, err := launchd.LockLifecycle(ctx, home)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := app.QuitBackground(ctx, pid, started); err != nil {
		return err
	}
	latePID, err := launchd.PauseMenu(ctx, os.Stderr)
	if err != nil {
		return err
	}
	if err := daemon.WaitForLaunchdExit(ctx, latePID); err != nil {
		return fmt.Errorf("wait for the kuri LaunchDaemon to exit: %w", err)
	}
	state, err := app.State(ctx)
	if err != nil {
		return err
	}
	if state.PID != 0 || len(state.Rows) != 0 {
		return errors.New("The kuri server is still running. Try Quit again.")
	}
	return nil
}
