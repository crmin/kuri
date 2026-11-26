package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/crmin/kuri/internal/daemon"
	"github.com/crmin/kuri/internal/launchd"
	"github.com/crmin/kuri/internal/settings"
)

var errKubeContextChanged = errors.New("The selected context is no longer in this kubeconfig.")

type settingsServer interface {
	State(context.Context) (daemon.State, error)
	ApplySettings(context.Context, daemon.State, string, bool) (daemon.State, error)
	Start(context.Context) (int, error)
	Stop(context.Context) (int, error)
	ConfirmPortChange(context.Context, launchd.PortChange) error
	Registration(context.Context) (launchd.Registration, error)
	OwnsRegisteredPort(context.Context, int) (bool, error)
	UninstallStartupForReset(context.Context, func(context.Context) error) error
	PreparePortRestart(context.Context, daemon.State, string) error
	RegisterForPort(context.Context, string) error
	HandoffDirectForeground(context.Context, daemon.State, string) error
	EnsurePolicySupport(context.Context) error
}

type settingsResult struct {
	saved, applied bool
	portError      string
	err            error
}

func updateSettings(ctx context.Context, app settingsServer, home string, edit settings.Edit) settingsResult {
	lock, err := launchd.LockLifecycle(ctx, home)
	if err != nil {
		return settingsResult{err: err}
	}
	defer lock.Close()

	store := settings.NewStore(home)
	before, readErr := store.Read()
	if readErr != nil {
		if !edit.Reset {
			return settingsResult{err: readErr}
		}
		before = settings.Defaults(home)
	}
	desired := before
	if edit.Reset {
		desired = settings.Defaults(home)
	}
	if err := edit.Apply(&desired); err != nil {
		return settingsResult{err: err}
	}
	if err := validateKubernetesEdit(desired, edit); err != nil {
		return settingsResult{err: err}
	}
	state, err := app.State(ctx)
	if err != nil {
		return settingsResult{err: err}
	}

	if edit.AutoStart != nil && !desired.General.AutoStart {
		if err := app.EnsurePolicySupport(ctx); err != nil {
			return settingsResult{err: err}
		}
	}
	if edit.Register != nil && !edit.Reset {
		if err := changeStartupSetting(ctx, app, home, state, desired.General.LaunchAtLogin); err != nil {
			return settingsResult{err: err}
		}
		state, err = app.State(ctx)
		if err != nil {
			return settingsResult{err: err}
		}
	}

	previousPort := before.Server.Port
	if state.Port != 0 {
		previousPort = state.Port
	}
	wasRunning := state.PID != 0
	var saved settings.Config
	var committed bool
	save := func(ctx context.Context) error {
		var err error
		if edit.Reset && readErr != nil {
			_, saved, err = store.Reset(ctx)
		} else {
			_, saved, err = store.Update(ctx, func(current *settings.Config) error {
				if current.Fingerprint() != before.Fingerprint() {
					return daemon.ErrSettingsChanged
				}
				*current = desired
				return nil
			})
		}
		committed = err == nil
		return err
	}
	resetRegistration := false
	if edit.Reset {
		reg, err := app.Registration(ctx)
		if err != nil {
			return settingsResult{err: err}
		}
		if reg.AtBoot {
			if state.Mode == "foreground" && state.Activated {
				return settingsResult{err: errors.New("Stop the foreground proxy from its terminal before changing the startup registration.")}
			}
			if err := app.UninstallStartupForReset(ctx, save); err != nil {
				return settingsResult{saved: committed, err: err}
			}
			resetRegistration = true
		}
	}
	if !committed {
		if err := save(ctx); err != nil {
			return settingsResult{err: err}
		}
	}
	explicitPort := edit.Port != nil || edit.Reset || edit == (settings.Edit{})
	generalOnly := !explicitPort && (edit.AutoStart != nil || edit.Register != nil) &&
		before.ProxyFingerprint() == saved.ProxyFingerprint()
	if generalOnly {
		return settingsResult{saved: true, applied: true}
	}
	// A proxy can publish its control socket between the first state check
	// and the settings write. Observe again before treating it as stopped.
	if state.PID == 0 || resetRegistration {
		state, err = app.State(ctx)
		if err != nil {
			return settingsResult{saved: true, err: err}
		}
	}
	if explicitPort {
		portChanged := saved.Server.Port != previousPort || (state.Port != 0 && saved.Server.Port != state.Port)
		if !portChanged && state.PID == 0 {
			reg, err := app.Registration(ctx)
			if err != nil {
				return settingsResult{saved: true, err: err}
			}
			portChanged = reg.Present() && reg.Port != saved.Server.Port
		}
		if portChanged {
			result := switchSavedPort(ctx, app, home, state, saved, previousPort)
			if !resetRegistration || !wasRunning || state.PID != 0 {
				return result
			}
			return resumeResetProxy(ctx, app, store, result)
		}
	}
	if resetRegistration && wasRunning && state.PID == 0 {
		return resumeResetProxy(ctx, app, store, settingsResult{saved: true, applied: true})
	}
	if state.PID != 0 && state.ProxyFingerprint == "" && state.Mode == "background" {
		err = upgradeRunningProxy(ctx, app, saved)
		if err != nil {
			return settingsResult{saved: true, err: err}
		}
		return settingsResult{saved: true, applied: true}
	}
	if err := applySettings(ctx, app, state, saved, !explicitPort); err != nil {
		return settingsResult{saved: true, err: err}
	}
	return settingsResult{saved: true, applied: true}
}

func resumeResetProxy(ctx context.Context, app settingsServer, store settings.Store, result settingsResult) settingsResult {
	cfg, err := store.Read()
	if err == nil {
		_, err = app.Start(ctx)
		if err == nil || errors.Is(err, daemon.ErrAlreadyRunning) {
			err = confirmServingSettings(ctx, app, cfg)
		}
	}
	if err != nil {
		result.err = errors.Join(result.err, err)
		result.applied = false
	}
	return result
}

func upgradeRunningProxy(ctx context.Context, app settingsServer, cfg settings.Config) error {
	if err := app.EnsurePolicySupport(ctx); err != nil {
		return err
	}
	if _, err := app.Stop(ctx); err != nil {
		return err
	}
	if _, err := app.Start(ctx); err != nil && !errors.Is(err, daemon.ErrAlreadyRunning) {
		return err
	}
	return confirmServingSettings(ctx, app, cfg)
}

func applySavedSettings(ctx context.Context, app settingsServer, state daemon.State, cfg settings.Config) error {
	return applySettings(ctx, app, state, cfg, false)
}

func applySettings(ctx context.Context, app settingsServer, state daemon.State, cfg settings.Config, retainPort bool) error {
	if state.PID == 0 {
		return nil
	}
	fingerprint := cfg.Fingerprint()
	retainPort = retainPort && state.Port != 0 && state.Port != cfg.Server.Port
	if retainPort {
		cfg.Server.Port = state.Port
	}
	if state.ProxyFingerprint == cfg.ProxyFingerprint() {
		return nil
	}
	applied, err := app.ApplySettings(ctx, state, fingerprint, retainPort)
	if err != nil {
		return err
	}
	if applied.ProxyFingerprint != cfg.ProxyFingerprint() || applied.Port != cfg.Server.Port {
		return errors.New("The reverse proxy did not confirm the saved settings.")
	}
	return nil
}

func switchSavedPort(ctx context.Context, app settingsServer, home string, state daemon.State, saved settings.Config, previousPort int) settingsResult {
	change, err := applyPort(ctx, app, home, state, saved)
	if err == nil {
		return settingsResult{saved: true, applied: true}
	}
	recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 4*time.Minute)
	defer cancel()
	store := settings.NewStore(home)
	_, restored, writeErr := store.Update(recovery, func(current *settings.Config) error {
		if current.Server.Port != saved.Server.Port {
			return daemon.ErrSettingsChanged
		}
		current.Server.Port = previousPort
		return nil
	})
	if writeErr != nil {
		return settingsResult{saved: true, err: errors.Join(err, writeErr),
			portError: fmt.Sprintf("Could not restore the saved port to %d.", previousPort)}
	}
	recoverErr := restoreServingPort(recovery, app, home, state, restored, change)
	result := settingsResult{saved: true, err: errors.Join(err, recoverErr)}
	var servingPreviousPort bool
	if recoverErr != nil && state.PID != 0 {
		if current, statusErr := app.State(recovery); statusErr == nil {
			servingPreviousPort = current.PID != 0 && current.Port == previousPort
		}
	}
	switch {
	case recoverErr != nil && state.PID == 0:
		result.portError = fmt.Sprintf("The saved port was restored to %d. The macOS registration could not be restored.", previousPort)
	case recoverErr != nil && !servingPreviousPort:
		result.portError = fmt.Sprintf("The saved port was restored to %d. The reverse proxy could not restart.", previousPort)
	case errors.Is(err, daemon.ErrPortOccupied):
		result.portError = fmt.Sprintf("Port %d is already in use by another process. The saved port was restored to %d.", saved.Server.Port, previousPort)
	default:
		result.portError = fmt.Sprintf("Could not switch to port %d. The saved port was restored to %d.", saved.Server.Port, previousPort)
	}
	return result
}
