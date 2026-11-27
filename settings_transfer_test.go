package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/crmin/kuri/internal/daemon"
	"github.com/crmin/kuri/internal/forward"
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

func TestImportPortWithContextAliases(t *testing.T) {
	for _, scenario := range []struct {
		name          string
		portOccupied  bool
		changeAtApply int
		wantAlias     string
	}{
		{"verified port switch", false, 0, "imported"},
		{"port occupied with an unchanged kubeconfig", true, 0, "imported"},
		{"kubeconfig changes during application", false, 1, "previous"},
		{"kubeconfig changes during port recovery", true, 2, "previous"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			home := t.TempDir()
			store := settings.NewStore(home)
			kubePath := filepath.Join(home, "active-config")
			writeKube := func(port int) error {
				return os.WriteFile(kubePath, []byte(fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: cluster
  cluster: {server: 'http://127.0.0.1:%d'}
contexts:
- name: dev
  context: {cluster: cluster}
`, port)), 0600)
			}
			if err := writeKube(1); err != nil {
				t.Fatal(err)
			}
			_, initial, err := store.Update(t.Context(), func(c *settings.Config) error {
				c.Kubernetes.ConfigPath = kubePath
				c.Kubernetes.MergeAliases(map[string]string{"dev": "previous"})
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			kube, _, err := forward.LoadKubernetes(kubePath)
			if err != nil {
				t.Fatal(err)
			}
			approved := kube.Fingerprint()
			const newPort = 16080
			app := &testSettingsServer{store: store, state: daemon.State{
				PID: 123, Port: initial.Server.Port, ProxyFingerprint: initial.ProxyFingerprint(),
				SettingsSchema: settings.SettingsSchemaVersion, KubeconfigFingerprint: approved,
			}}
			if scenario.portOccupied {
				app.blockedPort = newPort
			}
			attempts := 0
			app.beforeApply = func(c settings.Config, expected string) error {
				attempts++
				if c.Kubernetes.Aliases()["dev"] != "imported" {
					return nil
				}
				if index := attempts; index == scenario.changeAtApply {
					if err := writeKube(2); err != nil {
						t.Fatal(err)
					}
				}
				if expected != approved || c.PendingKubeconfigFingerprint() != approved {
					t.Fatalf("applying the import without its approved identity: %q, %q", expected, c.PendingKubeconfigFingerprint())
				}
				loaded, _, err := forward.LoadKubernetes(kubePath)
				if err != nil {
					t.Fatal(err)
				}
				if loaded.Fingerprint() != expected {
					return daemon.ErrKubeconfigChanged
				}
				return nil
			}
			req := settingsTransferRequest{Operation: "import", KubeconfigPath: kubePath, KubeconfigFingerprint: approved,
				Fields: []settings.TransferField{settings.TransferPort, settings.TransferDomain, settings.TransferAliases},
				Transfer: &settings.Transfer{Server: &settings.TransferServer{Port: new(newPort), Domain: new("example.test")},
					Kubernetes: &settings.TransferKubernetes{Aliases: map[string]string{"dev": "imported"}}}}
			result := handleSettingsTransfer(t.Context(), app, home, req)
			wantPort := newPort
			if scenario.portOccupied || scenario.changeAtApply != 0 {
				wantPort = initial.Server.Port
			}
			if result.Settings == nil || !result.Settings.Saved || result.Settings.Applied != (wantPort == newPort) {
				t.Fatalf("response did not distinguish saved values and listener result: %+v", result)
			}
			got := result.Settings.Config
			if got.Server.Port != wantPort || got.Server.Domain != "example.test" || got.Kubernetes.Aliases()["dev"] != scenario.wantAlias ||
				got.PendingKubeconfigFingerprint() != "" || app.state.ProxyFingerprint != got.ProxyFingerprint() {
				t.Fatalf("saved/active settings: %+v, %+v", got, app.state)
			}
		})
	}
}
