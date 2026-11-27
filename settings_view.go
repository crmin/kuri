package main

import (
	"context"
	"slices"

	"github.com/crmin/kuri/internal/forward"
	"github.com/crmin/kuri/internal/launchd"
	"github.com/crmin/kuri/internal/settings"
)

type settingsView struct {
	Config            settings.Config `json:"config"`
	Contexts          []string        `json:"contexts"`
	FileError         string          `json:"file_error,omitempty"`
	KubeconfigError   string          `json:"kubeconfig_error,omitempty"`
	ProxyUpdateNeeded bool            `json:"proxy_update_needed,omitempty"`
	RegistrationError string          `json:"registration_error,omitempty"`
	RuntimeWarning    string          `json:"runtime_warning,omitempty"`
	ApplyPending      bool            `json:"apply_pending,omitempty"`
	Error             string          `json:"error,omitempty"`
	PortError         string          `json:"port_error,omitempty"`
	Saved             bool            `json:"saved"`
	Applied           bool            `json:"applied"`
}

func loadSettingsView(ctx context.Context, home string, app settingsServer) settingsView {
	store := settings.NewStore(home)
	cfg, err := store.Ensure(ctx)
	view := settingsView{Config: cfg, Contexts: []string{}}
	if err != nil {
		view.Config = settings.Defaults(home)
		view.FileError = err.Error()
	}
	registration, err := launchd.Registered(ctx, home)
	if err != nil {
		view.RegistrationError = err.Error()
	} else if registration.AppStartup || !registration.SupportsProxyPause(ctx) ||
		!registration.SupportsSettingsSchema(ctx, settings.SettingsSchemaVersion) {
		if registration.CanUpdateFromApp() {
			view.ProxyUpdateNeeded = true
		} else {
			view.RegistrationError = registration.ProxyUpdateError().Error()
		}
	}
	kube, _, err := forward.LoadKubernetes(view.Config.Kubernetes.ConfigPath)
	if err != nil {
		view.KubeconfigError = err.Error()
	} else {
		view.Contexts = kube.ContextNames()
	}
	if view.FileError == "" {
		if state, err := app.State(ctx); err == nil && state.PID != 0 && state.ProxyFingerprint != view.Config.ProxyFingerprint() {
			view.ApplyPending = true
			view.RuntimeWarning = "The reverse proxy has settings waiting to be applied."
			if state.ProxyFingerprint == "" {
				view.RuntimeWarning = "Update the installed kuri binary to apply the saved settings."
			}
		}
	}
	return view
}

func validateKubernetesEdit(cfg settings.Config, edit settings.Edit) error {
	if edit.ConfigPath == nil && edit.Context == nil {
		return nil
	}
	kube, _, err := forward.LoadKubernetes(cfg.Kubernetes.ConfigPath)
	if err != nil {
		return err
	}
	if edit.Context != nil && !slices.Contains(kube.ContextNames(), edit.Context.Name) {
		return errKubeContextChanged
	}
	return nil
}
