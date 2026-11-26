package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/crmin/kuri/internal/daemon"
	"github.com/crmin/kuri/internal/launchd"
	"github.com/crmin/kuri/internal/settings"
)

// The port change record identifies the privileged swap if a later step
// fails after the original listener has already been handed off.
type savedPortChange struct {
	registration launchd.Registration
	attempted    bool
	operation    launchd.PortChange
	created      bool
}

func applyPort(ctx context.Context, app settingsServer, home string, state daemon.State, cfg settings.Config) (savedPortChange, error) {
	registered, err := app.Registration(ctx)
	change := savedPortChange{registration: registered}
	if err != nil {
		return change, err
	}
	if !registered.Present() || registered.Port == cfg.Server.Port {
		if !state.Activated {
			if state.PID == 0 {
				if err := daemon.ProbePort(cfg.Server.Port); err != nil && !daemon.RequiresRegistration(err) {
					return change, err
				}
			}
			err := applySavedSettings(ctx, app, state, cfg)
			if !daemon.RequiresRegistration(err) || registered.Present() {
				return change, err
			}
			if state.Mode == "foreground" {
				change.operation = launchd.PortChange{ID: rand.Text(), Port: cfg.Server.Port}
			}
			if err := app.RegisterForPort(ctx, change.operation.ID); err != nil {
				return change, err
			}
			change.created = true
			if state.Mode == "foreground" {
				if err := app.ConfirmPortChange(ctx, change.operation); err != nil {
					return change, err
				}
				if err := app.HandoffDirectForeground(ctx, state, change.operation.ID); err != nil {
					return change, err
				}
				if err := waitForForegroundRestart(ctx, app, state, cfg); err != nil {
					return change, err
				}
				return change, change.operation.Finish(home)
			}
			if _, err := app.Stop(ctx); err != nil {
				return change, err
			}
			if _, err := app.Start(ctx); err != nil && !errors.Is(err, daemon.ErrAlreadyRunning) {
				return change, err
			}
			return change, confirmServingSettings(ctx, app, cfg)
		}
		if registered.Port == cfg.Server.Port {
			return change, applySavedSettings(ctx, app, state, cfg)
		}
		return change, errors.New("Could not locate the active kuri registration.")
	}

	if err := app.EnsurePolicySupport(ctx); err != nil {
		return change, err
	}
	if err := daemon.ProbePort(cfg.Server.Port); err != nil && !daemon.RequiresRegistration(err) {
		return change, err
	}
	operation := launchd.PortChange{ID: rand.Text(), Port: cfg.Server.Port}
	change.operation = operation
	if state.Mode == "foreground" {
		if !state.Activated {
			return change, errors.New("The foreground reverse proxy cannot hand off its listener to the current registration.")
		}
		if err := app.PreparePortRestart(ctx, state, operation.ID); err != nil {
			return change, err
		}
	}
	change.attempted = true
	loaded, err := launchd.ChangePort(ctx, home, operation, os.Stderr)
	if err != nil {
		return change, err
	}
	if loaded {
		if err := app.ConfirmPortChange(ctx, operation); err != nil {
			return change, err
		}
	}
	if state.PID != 0 {
		if state.Activated || (registered.Present() && state.ProxyFingerprint == "") {
			if err := waitForPreviousServer(ctx, app, state); err != nil {
				return change, err
			}
		} else {
			if _, err := app.Stop(ctx); err != nil {
				return change, err
			}
		}
		if state.Mode == "foreground" {
			if err := waitForForegroundRestart(ctx, app, state, cfg); err != nil {
				return change, err
			}
		} else {
			if _, err := app.Start(ctx); err != nil {
				return change, err
			}
			if err := confirmServingSettings(ctx, app, cfg); err != nil {
				return change, err
			}
		}
	}
	if loaded {
		return change, operation.Finish(home)
	}
	return change, nil
}

func restoreServingPort(ctx context.Context, app settingsServer, home string, before daemon.State, cfg settings.Config, change savedPortChange) error {
	if change.created {
		if before.Mode == "foreground" {
			state, err := app.State(ctx)
			if err != nil {
				return err
			}
			if state.PID != 0 && state.Activated {
				if err := app.PreparePortRestart(ctx, state, change.operation.ID); err != nil {
					return err
				}
			}
		}
		if err := launchd.RemoveSocketRegistration(ctx, os.Stderr); err != nil {
			return err
		}
	}
	var operation launchd.PortChange
	bootConfig := cfg
	if change.attempted {
		if before.AppliedSettings != nil && before.AppliedSettings.Server.Port == cfg.Server.Port {
			bootConfig = *before.AppliedSettings
			drop, err := settings.NewStore(home).RetainRecovery(ctx, change.operation.ID, bootConfig)
			if err != nil {
				return err
			}
			defer drop()
		}
		current, err := app.Registration(ctx)
		if err != nil {
			return err
		}
		if current.Port != change.registration.Port {
			operation = launchd.PortChange{ID: change.operation.ID, Port: change.registration.Port}
			if before.Mode == "foreground" {
				state, err := app.State(ctx)
				if err != nil {
					return err
				}
				if state.PID != 0 {
					if err := app.PreparePortRestart(ctx, state, operation.ID); err != nil {
						return err
					}
				}
			}
			loaded, err := launchd.ChangePort(ctx, home, operation, os.Stderr)
			if err != nil {
				return err
			}
			if !loaded {
				operation = launchd.PortChange{}
			}
		} else {
			operation, err = launchd.ReadPortChange()
			if err != nil {
				return err
			}
		}
		pending, err := operation.Pending(home)
		if err != nil {
			return err
		}
		if pending {
			if err := app.ConfirmPortChange(ctx, operation); err != nil {
				return err
			}
		} else {
			operation = launchd.PortChange{}
		}
	}
	if before.PID != 0 {
		current, err := app.State(ctx)
		if err != nil {
			return err
		}
		if current.PID == 0 {
			if before.Mode == "foreground" {
				if err := waitForForegroundRestart(ctx, app, before, bootConfig); err != nil && !errors.Is(err, errForegroundClosed) {
					return err
				}
			} else {
				if _, err := app.Start(ctx); err != nil {
					return err
				}
				if err := confirmServingSettings(ctx, app, bootConfig); err != nil {
					return err
				}
			}
		}
	}
	if operation.ID != "" {
		if err := operation.Finish(home); err != nil {
			return err
		}
	}
	if before.PID != 0 {
		state, err := app.State(ctx)
		if err != nil {
			return err
		}
		return applySavedSettings(ctx, app, state, cfg)
	}
	return nil
}

var errForegroundClosed = errors.New("The foreground terminal exited before the reverse proxy restarted.")

func prepareRegisteredStart(ctx context.Context, app settingsServer, home string) error {
	state, err := app.State(ctx)
	if err != nil || state.PID != 0 {
		return err
	}
	registered, err := app.Registration(ctx)
	if err != nil || !registered.Present() {
		return err
	}
	cfg, err := settings.NewStore(home).Read()
	if err != nil || cfg.Server.Port == registered.Port {
		return err
	}
	result := switchSavedPort(ctx, app, home, state, cfg, registered.Port)
	return result.err
}

func waitForForegroundRestart(ctx context.Context, app settingsServer, previous daemon.State, cfg settings.Config) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ticker := time.NewTicker(75 * time.Millisecond)
	defer ticker.Stop()
	for {
		state, err := app.State(ctx)
		if err == nil && state.Mode == "foreground" && state.Port == cfg.Server.Port &&
			state.ProxyFingerprint == cfg.ProxyFingerprint() && state.ForegroundOwner == previous.ForegroundOwner {
			return nil
		}
		if previous.ForegroundOwner > 0 && errors.Is(syscall.Kill(previous.ForegroundOwner, 0), syscall.ESRCH) {
			return errForegroundClosed
		}
		select {
		case <-ctx.Done():
			return errors.New("The foreground reverse proxy could not reconnect.")
		case <-ticker.C:
		}
	}
}

func confirmServingSettings(ctx context.Context, app settingsServer, cfg settings.Config) error {
	state, err := app.State(ctx)
	if err != nil {
		return err
	}
	if state.PID == 0 || state.Port != cfg.Server.Port || state.ProxyFingerprint != cfg.ProxyFingerprint() {
		return fmt.Errorf("The reverse proxy did not become ready on port %d.", cfg.Server.Port)
	}
	return nil
}

func waitForPreviousServer(ctx context.Context, app settingsServer, previous daemon.State) error {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	timer := time.NewTicker(75 * time.Millisecond)
	defer timer.Stop()
	for {
		current, err := app.State(ctx)
		if err == nil && (current.PID != previous.PID || current.Token != previous.Token) {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("The previous reverse proxy did not stop.")
		case <-timer.C:
		}
	}
}
