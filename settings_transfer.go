package main

import (
	"context"
	"errors"
	"time"

	"github.com/crmin/kuri/internal/forward"
	"github.com/crmin/kuri/internal/settings"
)

type settingsTransferRequest struct {
	Operation             string                   `json:"transfer_operation"`
	Path                  string                   `json:"path,omitempty"`
	Fields                []settings.TransferField `json:"fields,omitempty"`
	Transfer              *settings.Transfer       `json:"transfer,omitempty"`
	KubeconfigPath        string                   `json:"kubeconfig_path,omitempty"`
	KubeconfigFingerprint string                   `json:"kubeconfig_fingerprint,omitempty"`
}

type settingsTransferReply struct {
	Error                 string                   `json:"error,omitempty"`
	Settings              *settingsView            `json:"settings,omitempty"`
	Transfer              *settings.Transfer       `json:"transfer,omitempty"`
	Fields                []settings.TransferField `json:"fields"`
	KubeconfigPath        string                   `json:"kubeconfig_path,omitempty"`
	KubeconfigFingerprint string                   `json:"kubeconfig_fingerprint,omitempty"`
	Refresh               bool                     `json:"refresh,omitempty"`
}

func handleSettingsTransfer(ctx context.Context, app settingsServer, home string, req settingsTransferRequest) settingsTransferReply {
	store := settings.NewStore(home)
	switch req.Operation {
	case "preview_import":
		cfg, err := store.Read()
		if err != nil {
			return settingsTransferReply{Error: err.Error()}
		}
		kube, _, _ := forward.LoadKubernetes(cfg.Kubernetes.ConfigPath)
		var names []string
		if kube != nil {
			names = kube.ContextNames()
		}
		transfer, err := settings.ReadTransfer(req.Path, names)
		if err != nil {
			return settingsTransferReply{Error: err.Error()}
		}
		preview := settingsTransferReply{Transfer: &transfer, Fields: transfer.Fields(), KubeconfigPath: cfg.Kubernetes.ConfigPath}
		if kube != nil {
			preview.KubeconfigFingerprint = kube.Fingerprint()
		}
		return preview
	case "export":
		if err := store.ExportTransfer(req.Path, req.Fields); err != nil {
			return settingsTransferReply{Error: err.Error()}
		}
		return settingsTransferReply{}
	case "import":
		return importSettings(ctx, app, home, store, req)
	default:
		return settingsTransferReply{Error: "Choose an import or export action."}
	}
}

func importSettings(ctx context.Context, app settingsServer, home string, store settings.Store, req settingsTransferRequest) settingsTransferReply {
	if req.Transfer == nil {
		return settingsTransferReply{Error: "Choose settings to import."}
	}
	selected := req.Transfer.Select(req.Fields)
	edit := settings.Edit{Transfer: &selected}
	if selected.Kubernetes != nil {
		cfg, err := store.Read()
		if err != nil {
			return settingsTransferReply{Error: err.Error()}
		}
		kube, _, err := forward.LoadKubernetes(cfg.Kubernetes.ConfigPath)
		if err != nil || cfg.Kubernetes.ConfigPath != req.KubeconfigPath || kube.Fingerprint() != req.KubeconfigFingerprint {
			return settingsTransferReply{Refresh: true}
		}
		aliases := make(map[string]string)
		for name, alias := range selected.Kubernetes.Aliases {
			if kube.HasContext(name) {
				aliases[name] = alias
			}
		}
		selected.Kubernetes = nil
		if len(aliases) > 0 {
			edit.ContextAliases = &settings.ContextAliasEdit{ConfigPath: req.KubeconfigPath,
				Fingerprint: req.KubeconfigFingerprint, Aliases: aliases}
		}
	}
	if len(selected.Fields()) == 0 && edit.ContextAliases == nil {
		return settingsTransferReply{Error: "Select at least one available setting to import."}
	}
	result := updateSettings(ctx, app, home, edit)
	if errors.Is(result.err, errKubeconfigChanged) || errors.Is(result.err, errKubeContextChanged) {
		return settingsTransferReply{Refresh: true}
	}
	if result.err != nil && !result.saved {
		return settingsTransferReply{Error: result.err.Error()}
	}
	observe, stop := context.WithTimeout(context.WithoutCancel(ctx), 6*time.Second)
	defer stop()
	view := loadSettingsView(observe, home, app)
	view.Saved, view.Applied, view.PortError = result.saved, result.applied, result.portError
	if result.err != nil {
		view.Error = result.err.Error()
	}
	return settingsTransferReply{Settings: &view}
}
