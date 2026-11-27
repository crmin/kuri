package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/crmin/kuri/internal/daemon"
	"github.com/crmin/kuri/internal/settings"
)

func TestImportContextAliasesMatchesCurrentKubeconfig(t *testing.T) {
	home := t.TempDir()
	store := settings.NewStore(home)
	current := filepath.Join(home, "current.config")
	other := filepath.Join(home, "other.config")
	const kubeconfig = "apiVersion: v1\nkind: Config\ncontexts:\n- name: dev\n  context: {}\n- name: keep\n  context: {}\n"
	if err := os.WriteFile(current, []byte(kubeconfig), 0600); err != nil {
		t.Fatal(err)
	}
	_, cfg, err := store.Update(t.Context(), func(c *settings.Config) error {
		c.Kubernetes.ConfigPath = other
		c.Kubernetes.MergeAliases(map[string]string{"dev": "other"})
		c.Kubernetes.ConfigPath = current
		c.Kubernetes.MergeAliases(map[string]string{"keep": "preserved"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(home, "aliases.yaml")
	if err := os.WriteFile(file, []byte("kubernetes:\n  context_aliases:\n    dev: demo\n    absent: not/a-label\n"), 0600); err != nil {
		t.Fatal(err)
	}
	app := &testSettingsServer{store: store, state: daemon.State{PID: 123, Port: cfg.Server.Port,
		ProxyFingerprint: cfg.ProxyFingerprint(), SettingsSchema: settings.SettingsSchemaVersion}}
	preview := handleSettingsTransfer(t.Context(), app, home, settingsTransferRequest{Operation: "preview_import", Path: file})
	if preview.Error != "" || len(preview.Fields) != 1 || preview.Fields[0] != settings.TransferAliases {
		t.Fatalf("preview: %+v", preview)
	}
	req := settingsTransferRequest{Operation: "import", Transfer: preview.Transfer, Fields: preview.Fields,
		KubeconfigPath: preview.KubeconfigPath, KubeconfigFingerprint: "old-view"}
	if got := handleSettingsTransfer(t.Context(), app, home, req); !got.Refresh {
		t.Fatalf("stale file accepted: %+v", got)
	}
	req.KubeconfigFingerprint = preview.KubeconfigFingerprint
	result := handleSettingsTransfer(t.Context(), app, home, req)
	if result.Error != "" || result.Settings == nil || !result.Settings.Applied {
		t.Fatalf("import: %+v", result)
	}
	got := result.Settings.Config.Kubernetes
	if got.Aliases()["dev"] != "demo" || got.Aliases()["keep"] != "preserved" || len(got.Aliases()) != 2 ||
		got.ContextAliases[other]["dev"] != "other" {
		t.Fatalf("settings used the wrong mappings: %+v", got)
	}
}

func TestImportSelectedPortLifecycle(t *testing.T) {
	home := t.TempDir()
	store := settings.NewStore(home)
	cfg, err := store.Ensure(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	app := &testSettingsServer{store: store, blockedPort: 16080, state: daemon.State{PID: 123,
		Port: cfg.Server.Port, ProxyFingerprint: cfg.ProxyFingerprint(), SettingsSchema: settings.SettingsSchemaVersion}}
	req := settingsTransferRequest{Operation: "import", Fields: []settings.TransferField{settings.TransferPort, settings.TransferDomain},
		Transfer: &settings.Transfer{Server: &settings.TransferServer{Domain: new("example.test"), Port: new(16080)}}}
	result := handleSettingsTransfer(t.Context(), app, home, req)
	if result.Settings == nil || result.Settings.PortError == "" || result.Settings.Config.Server.Port != cfg.Server.Port ||
		result.Settings.Config.Server.Domain != "example.test" {
		t.Fatalf("failed port change did not restore only the port: %+v", result.Settings)
	}
}
