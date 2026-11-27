package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/crmin/kuri/internal/daemon"
	"github.com/crmin/kuri/internal/launchd"
	"github.com/crmin/kuri/internal/settings"
)

func startBackground(ctx context.Context, app *daemon.App, home string) (int, error) {
	return startBackgroundWithOrigin(ctx, app, home, false)
}

func startBackgroundWithOrigin(ctx context.Context, app *daemon.App, home string, automatic bool) (int, error) {
	lock, err := launchd.LockLifecycle(ctx, home)
	if err != nil {
		return 0, err
	}
	defer lock.Close()
	start := app.Start
	if automatic {
		allowed, err := settings.NewStore(home).AutoStart(ctx)
		if err != nil {
			return 0, err
		}
		if !allowed {
			return 0, daemon.ErrAutoStartDisabled
		}
		start = app.StartAutomatic
	}
	if err := prepareRegisteredStart(ctx, app, home); err != nil {
		return 0, err
	}
	pause, err := launchd.ReadMenuPause()
	if err != nil {
		return 0, err
	}
	if pause.Phase == "" {
		pid, err := start(ctx)
		if err != nil {
			return pid, err
		}
		return pid, settings.NewStore(home).SetProxyPaused(ctx, false)
	}
	operation := rand.Text()
	if pause.Phase == "resuming" && pause.Operation != "" {
		// Continue the recorded transaction rather than claiming another job.
		operation = pause.Operation
	}
	pid, err := runMenuResume(ctx, menuResumeSteps{
		begin: func(ctx context.Context) error {
			return launchd.ChangeMenuRegistration(ctx, "resume", home, operation, pause.Operation, os.Stderr)
		},
		start: func(ctx context.Context) (int, error) { return startResumedServer(ctx, app, start) },
		finish: func(ctx context.Context) error {
			return launchd.ChangeMenuRegistration(ctx, "finish-resume", home, operation, "", os.Stderr)
		},
		rollback: func(ctx context.Context) error {
			return launchd.ChangeMenuRegistration(ctx, "rollback", home, operation, "", os.Stderr)
		},
	})
	if err != nil {
		return pid, err
	}
	return pid, settings.NewStore(home).SetProxyPaused(ctx, false)
}

func startResumedServer(ctx context.Context, app *daemon.App, start func(context.Context) (int, error)) (int, error) {
	pid, err := start(ctx)
	if errors.Is(err, daemon.ErrAlreadyRunning) {
		state, stateErr := app.State(ctx)
		if stateErr == nil && state.PID > 0 && state.Mode == "background" {
			pid, err = state.PID, nil
		}
	}
	return pid, err
}

type menuResumeSteps struct {
	begin, finish, rollback func(context.Context) error
	start                   func(context.Context) (int, error)
}

func runMenuResume(ctx context.Context, steps menuResumeSteps) (int, error) {
	var pid int
	err := steps.begin(ctx)
	if err == nil {
		pid, err = steps.start(ctx)
	}
	if err == nil {
		err = steps.finish(ctx)
	}
	if err == nil {
		return pid, nil
	}
	recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 4*time.Minute)
	defer cancel()
	if rollbackErr := steps.rollback(recovery); rollbackErr != nil {
		return 0, errors.Join(err, fmt.Errorf("restore the paused kuri registration: %w", rollbackErr))
	}
	return 0, err
}

func stopBackground(ctx context.Context, app *daemon.App, home string) (int, error) {
	lock, err := launchd.LockLifecycle(ctx, home)
	if err != nil {
		return 0, err
	}
	defer lock.Close()
	if err := pauseBackground(ctx, app, home, false); err != nil {
		return 0, err
	}
	return app.Stop(ctx)
}

func pauseBackground(ctx context.Context, app *daemon.App, home string, quit bool) error {
	state, err := app.State(ctx)
	if err != nil {
		return err
	}
	if state.Mode == "foreground" {
		if quit {
			return errors.New("Stop the foreground kuri process in its terminal before quitting.")
		}
		return daemon.ErrNoBackground
	}
	if err := launchd.CheckProxyPauseSupport(ctx, home, state.PID, app.SupportsLiveProxyPause); err != nil {
		return err
	}
	return settings.NewStore(home).SetProxyPaused(ctx, true)
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
	if err := pauseBackground(ctx, app, home, true); err != nil {
		return err
	}
	if err := app.QuitBackground(ctx, pid, started); err != nil {
		return err
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
