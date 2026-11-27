package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"time"

	"github.com/crmin/kuri/internal/daemon"
	"github.com/crmin/kuri/internal/forward"
	"github.com/crmin/kuri/internal/launchd"
	"github.com/crmin/kuri/internal/settings"
)

var errKubeContextChanged = errors.New("The selected context is no longer in this kubeconfig.")
var errKubeconfigChanged = daemon.ErrKubeconfigChanged

type settingsServer interface {
	State(context.Context) (daemon.State, error)
	ApplySettings(context.Context, daemon.State, string, bool, ...string) (daemon.State, error)
	Start(context.Context) (int, error)
	Stop(context.Context) (int, error)
	HoldProxyForRegistration(context.Context, func(daemon.State) error) (io.Closer, bool, error)
	ConfirmPortChange(context.Context, launchd.PortChange) error
	Registration(context.Context) (launchd.Registration, error)
	OwnsRegisteredPort(context.Context, int) (bool, error)
	PreparePortRestart(context.Context, daemon.State, string) error
	RegisterForPort(context.Context, string) error
	HandoffDirectForeground(context.Context, daemon.State, string) error
	EnsurePolicySupport(context.Context) error
	CheckSettingsSchema(context.Context, daemon.State, int) error
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
	if edit.ContextAliases != nil && edit.ContextAliases.ConfigPath != desired.Kubernetes.ConfigPath {
		return settingsResult{err: errKubeconfigChanged}
	}
	if err := edit.Apply(&desired); err != nil {
		return settingsResult{err: err}
	}
	if err := validateKubernetesEdit(desired, edit); err != nil {
		return settingsResult{err: err}
	}
	explicitPort := edit.Port != nil || (edit.Transfer != nil && edit.Transfer.Server != nil && edit.Transfer.Server.Port != nil) ||
		edit.Reset || edit == (settings.Edit{})
	var previousAliases *settings.Config
	var kubeconfigFingerprint string
	if edit.ContextAliases != nil {
		kubeconfigFingerprint = edit.ContextAliases.Fingerprint
		if explicitPort || desired.PendingKubeconfigFingerprint() != "" {
			desired.SetPendingKubeconfigFingerprint(kubeconfigFingerprint)
			previousAliases = &before
		}
	}
	state, err := app.State(ctx)
	if err != nil {
		return settingsResult{err: err}
	}
	if required := desired.RequiredSchemaVersion(); required > 0 {
		if err := app.CheckSettingsSchema(ctx, state, required); err != nil {
			return settingsResult{err: err}
		}
		if state.PID != 0 && state.SettingsSchema < required {
			return settingsResult{err: errors.New("Restart the reverse proxy with the updated kuri binary before saving these settings.")}
		}
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
			return switchSavedPort(ctx, app, home, state, saved, previousPort, previousAliases)
		}
	}
	if state.PID != 0 && state.ProxyFingerprint == "" && state.Mode == "background" {
		err = upgradeRunningProxy(ctx, app, saved)
		if err != nil {
			return settingsResult{saved: true, err: err}
		}
		return settingsResult{saved: true, applied: true}
	}
	if edit == (settings.Edit{}) && state.KubeconfigFingerprint != "" && saved.PendingKubeconfigFingerprint() == "" {
		if len(saved.Kubernetes.Aliases()) > 0 {
			kube, _, err := forward.LoadKubernetes(saved.Kubernetes.ConfigPath)
			if err != nil {
				return settingsResult{saved: true, err: err}
			}
			kubeconfigFingerprint = kube.Fingerprint()
		}
	}
	if err := applySettings(ctx, app, state, saved, !explicitPort, kubeconfigFingerprint); err != nil {
		if errors.Is(err, errKubeconfigChanged) && previousAliases != nil {
			_, _, restoreErr := store.Update(ctx, func(current *settings.Config) error {
				return restoreAliasProfile(current, saved, *previousAliases)
			})
			err = errors.Join(err, restoreErr)
		}
		return settingsResult{saved: true, err: err}
	}
	return settingsResult{saved: true, applied: true, err: finishSettingsApply(ctx, store, saved)}
}

func finishSettingsApply(ctx context.Context, store settings.Store, saved settings.Config) error {
	if saved.PendingKubeconfigFingerprint() == "" {
		return nil
	}
	_, _, err := store.Update(ctx, func(current *settings.Config) error {
		if current.Fingerprint() != saved.Fingerprint() {
			return daemon.ErrSettingsChanged
		}
		current.SetPendingKubeconfigFingerprint("")
		return nil
	})
	return err
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
	return applySettings(ctx, app, state, cfg, false, "")
}

func applySettings(ctx context.Context, app settingsServer, state daemon.State, cfg settings.Config, retainPort bool, expectedKubeconfig string) error {
	if state.PID == 0 {
		return verifySavedKubeconfig(cfg)
	}
	fingerprint := cfg.Fingerprint()
	retainPort = retainPort && state.Port != 0 && state.Port != cfg.Server.Port
	if retainPort {
		cfg.Server.Port = state.Port
	}
	if pending := cfg.PendingKubeconfigFingerprint(); pending != "" {
		expectedKubeconfig = pending
	}
	if state.ProxyFingerprint == cfg.ProxyFingerprint() && (expectedKubeconfig == "" || expectedKubeconfig == state.KubeconfigFingerprint) {
		return nil
	}
	applied, err := app.ApplySettings(ctx, state, fingerprint, retainPort, expectedKubeconfig)
	if err != nil {
		return err
	}
	if applied.ProxyFingerprint != cfg.ProxyFingerprint() || applied.Port != cfg.Server.Port ||
		(expectedKubeconfig != "" && applied.KubeconfigFingerprint != expectedKubeconfig) {
		return errors.New("The reverse proxy did not confirm the saved settings.")
	}
	return nil
}

func verifySavedKubeconfig(cfg settings.Config) error {
	expected := cfg.PendingKubeconfigFingerprint()
	if expected == "" {
		return nil
	}
	kube, _, err := forward.LoadKubernetes(cfg.Kubernetes.ConfigPath)
	if err != nil {
		return errors.Join(errKubeconfigChanged, err)
	}
	if kube.Fingerprint() != expected {
		return errKubeconfigChanged
	}
	return nil
}

func restoreAliasProfile(current *settings.Config, saved, before settings.Config) error {
	if current.Kubernetes.ConfigPath != saved.Kubernetes.ConfigPath ||
		current.PendingKubeconfigFingerprint() != saved.PendingKubeconfigFingerprint() ||
		!maps.Equal(current.Kubernetes.Aliases(), saved.Kubernetes.Aliases()) {
		return daemon.ErrSettingsChanged
	}
	current.Kubernetes.ReplaceAliases(before.Kubernetes.Aliases())
	current.SetPendingKubeconfigFingerprint(before.PendingKubeconfigFingerprint())
	return nil
}

func switchSavedPort(ctx context.Context, app settingsServer, home string, state daemon.State, saved settings.Config, previousPort int, previousAliases *settings.Config) settingsResult {
	store := settings.NewStore(home)
	change, err := applyPort(ctx, app, home, state, saved)
	if err == nil && state.PID == 0 {
		err = verifySavedKubeconfig(saved)
	}
	if err == nil {
		return settingsResult{saved: true, applied: true, err: finishSettingsApply(ctx, store, saved)}
	}
	recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 4*time.Minute)
	defer cancel()
	// Keep the other imported values on a port failure. The alias edit can
	// only be kept if it still refers to the approved kubeconfig.
	if !errors.Is(err, errKubeconfigChanged) {
		err = errors.Join(err, verifySavedKubeconfig(saved))
	}
	undoAliases := previousAliases != nil && errors.Is(err, errKubeconfigChanged)
	_, restored, writeErr := store.Update(recovery, func(current *settings.Config) error {
		if current.Server.Port != saved.Server.Port {
			return daemon.ErrSettingsChanged
		}
		current.Server.Port = previousPort
		if undoAliases {
			return restoreAliasProfile(current, saved, *previousAliases)
		}
		return nil
	})
	if writeErr != nil {
		return settingsResult{saved: true, err: errors.Join(err, writeErr),
			portError: fmt.Sprintf("Could not restore the saved port to %d.", previousPort)}
	}
	recoverErr := restoreServingPort(recovery, app, home, state, restored, change)
	if !undoAliases && previousAliases != nil && errors.Is(recoverErr, errKubeconfigChanged) {
		// The file can change while the previous listener is being restored.
		// At this point any newly created registration has been removed.
		err = errors.Join(err, recoverErr)
		_, reverted, revertErr := store.Update(recovery, func(current *settings.Config) error {
			if current.Server.Port != previousPort {
				return daemon.ErrSettingsChanged
			}
			return restoreAliasProfile(current, saved, *previousAliases)
		})
		if revertErr != nil {
			recoverErr = revertErr
		} else {
			undoAliases = true
			restored = reverted
			change.created = false
			recoverErr = restoreServingPort(recovery, app, home, state, restored, change)
		}
	}
	var finishErr error
	if recoverErr == nil {
		finishErr = finishSettingsApply(recovery, store, restored)
	}
	result := settingsResult{saved: true, err: errors.Join(err, recoverErr, finishErr)}
	var servingPreviousPort, remainingApplied bool
	if state.PID != 0 && (recoverErr != nil || previousAliases != nil) {
		if current, statusErr := app.State(recovery); statusErr == nil {
			servingPreviousPort = current.PID != 0 && current.Port == previousPort
			remainingApplied = servingPreviousPort && current.ProxyFingerprint == restored.ProxyFingerprint() &&
				verifyServingKubeconfig(current, restored) == nil
		}
	}
	switch {
	case errors.Is(recoverErr, errKubeconfigChanged):
		result.portError = fmt.Sprintf("The saved port was restored to %d. Review the current kubeconfig before applying the saved settings.", previousPort)
	case recoverErr != nil && state.PID == 0:
		result.portError = fmt.Sprintf("The saved port was restored to %d. The macOS registration could not be restored.", previousPort)
	case recoverErr != nil && !servingPreviousPort:
		result.portError = fmt.Sprintf("The saved port was restored to %d. The reverse proxy could not restart.", previousPort)
	case recoverErr != nil:
		result.portError = fmt.Sprintf("The saved port was restored to %d. Some saved settings are still waiting to be applied.", previousPort)
	case undoAliases:
		result.portError = fmt.Sprintf("The kubeconfig changed. The saved port was restored to %d and the previous aliases were kept. Review the kubeconfig before importing aliases again.", previousPort)
	case errors.Is(err, daemon.ErrPortOccupied):
		result.portError = fmt.Sprintf("Port %d is already in use by another process. The saved port was restored to %d.", saved.Server.Port, previousPort)
	default:
		result.portError = fmt.Sprintf("Could not switch to port %d. The saved port was restored to %d.", saved.Server.Port, previousPort)
	}
	if previousAliases != nil && recoverErr == nil {
		if remainingApplied {
			result.portError += " Other imported settings were applied."
		} else {
			result.portError += " Other imported settings were saved."
		}
	}
	return result
}
