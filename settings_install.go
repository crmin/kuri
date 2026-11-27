package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/crmin/kuri/internal/daemon"
	"github.com/crmin/kuri/internal/launchd"
	"github.com/crmin/kuri/internal/settings"
)

func prepareProxyRegistration(ctx context.Context, app settingsServer, home string) error {
	reg, err := app.Registration(ctx)
	if err != nil || !reg.Present() {
		return err
	}
	if !reg.CanUpdateFromApp() {
		return reg.ProxyUpdateError()
	}
	source, err := os.Executable()
	if err != nil {
		return err
	}

	store := settings.NewStore(home)
	if err := checkRegistrationPort(store, reg.Port, 0); err != nil {
		return err
	}
	var hold io.Closer
	var stopped, pauseAdded bool
	err = launchd.UpdateProxyRegistration(ctx, home, source, reg, os.Stderr, func(ctx context.Context) error {
		current, err := app.Registration(ctx)
		if err != nil {
			return err
		}
		if current != reg {
			return errors.New("The proxy registration changed while waiting for approval. Try again.")
		}
		hold, stopped, err = app.HoldProxyForRegistration(ctx, func(state daemon.State) error {
			return checkRegistrationPort(store, reg.Port, state.Port)
		})
		if err == nil && reg.AtBoot && !reg.AppStartup {
			pauseAdded, err = store.PauseProxyForRefresh(ctx)
		}
		return err
	})
	if hold != nil {
		err = errors.Join(err, hold.Close())
	}
	if !stopped && !pauseAdded {
		return err
	}

	recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	transitionFailed := err != nil
	restarted := false
	if stopped {
		_, startErr := app.Start(recovery)
		restarted = startErr == nil || errors.Is(startErr, daemon.ErrAlreadyRunning)
		if !restarted {
			err = errors.Join(err, startErr)
		}
	}
	if pauseAdded && (restarted || transitionFailed) {
		err = errors.Join(err, store.SetProxyPaused(recovery, false))
	}
	return err
}

func checkRegistrationPort(store settings.Store, registered, running int) error {
	cfg, err := store.Read()
	if err != nil {
		return err
	}
	if cfg.Server.Port != registered || (running != 0 && running != registered) {
		return fmt.Errorf("The saved or running port differs from the registered port %d. Apply the port setting before updating the proxy registration.", registered)
	}
	return nil
}
