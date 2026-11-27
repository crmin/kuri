package main

import (
	"context"
	"errors"
	"os"

	"github.com/crmin/kuri/internal/daemon"
	"github.com/crmin/kuri/internal/launchd"
)

func prepareProxyRegistration(ctx context.Context, app settingsServer, home string) error {
	reg, err := app.Registration(ctx)
	if err != nil || !reg.Present() {
		return err
	}
	if !reg.AppStartup {
		return launchd.EnsureProxyPauseSupport(ctx, home, os.Stderr)
	}

	state, err := app.State(ctx)
	if err != nil {
		return err
	}
	if state.Mode == "foreground" {
		return errors.New("Stop the foreground proxy from its terminal before updating the registration.")
	}
	source, err := os.Executable()
	if err != nil {
		return err
	}
	var handoff func(context.Context) error
	stopped := false
	if state.PID != 0 {
		handoff = func(ctx context.Context) error {
			_, err := app.Stop(ctx)
			stopped = err == nil
			return err
		}
	}
	err = launchd.ConvertAppStartup(ctx, home, source, os.Stderr, handoff)
	if !stopped {
		return err
	}
	_, startErr := app.Start(context.WithoutCancel(ctx))
	if errors.Is(startErr, daemon.ErrAlreadyRunning) {
		startErr = nil
	}
	return errors.Join(err, startErr)
}
