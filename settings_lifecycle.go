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

	if edit.Register != nil {
		return settingsResult{err: errors.New("Open Login Items in macOS System Settings to change whether kuri opens at login.")}
	}
	if edit.PrepareProxy {
		if edit != (settings.Edit{PrepareProxy: true}) {
			return settingsResult{err: errors.New("Update the proxy registration separately from other settings.")}
		}
		err := prepareProxyRegistration(ctx, app, home)
		return settingsResult{applied: err == nil, err: err}
	}

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

	previousPort := before.Server.Port
	if state.Port != 0 {
		previousPort = state.Port
	}
	var saved settings.Config
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
	if err != nil {
		return settingsResult{err: err}
	}
	explicitPort := edit.Port != nil || edit.Reset || edit == (settings.Edit{})
	generalOnly := !explicitPort && edit.AutoStart != nil &&
		before.ProxyFingerprint() == saved.ProxyFingerprint()
	if generalOnly {
		return settingsResult{saved: true, applied: true}
	}
	// A proxy can publish its control socket between the first state check
	// and the settings write. Observe again before treating it as stopped.
	if state.PID == 0 {
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
			return switchSavedPort(ctx, app, home, state, saved, previousPort)
		}
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
