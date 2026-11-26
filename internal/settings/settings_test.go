package settings

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSettingsCreatesDefaultsOnFirstLaunch(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)
	got, err := store.Ensure(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !got.General.AutoStart || got.General.LaunchAtLogin || got.Server.Port != 80 ||
		got.Kubernetes.ConfigPath != filepath.Join(home, ".kube", "config") ||
		got.PortForward.MinPort != 30000 || got.PortForward.MaxPort != 39999 {
		t.Fatalf("built-in defaults: %+v", got)
	}
	info, err := os.Stat(filepath.Join(home, ".kuri", "settings.yaml"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("settings file: %v, %v", info, err)
	}
}

func TestSettingsUpdatePreservesExplicitFalseAndEmptySelections(t *testing.T) {
	store := NewStore(t.TempDir())
	if _, err := store.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, change := range []func(*Config){
		func(c *Config) { c.General.AutoStart = false },
		func(c *Config) { c.Server.Styles = DomainStyles{} },
		func(c *Config) { c.Kubernetes.ExcludedContexts = []string{"dev"} },
	} {
		wg.Go(func() {
			if _, _, err := store.Update(context.Background(), func(c *Config) error { change(c); return nil }); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	got, err := store.Read()
	if err != nil || got.General.AutoStart || got.Server.Styles != (DomainStyles{}) ||
		len(got.Kubernetes.ExcludedContexts) != 1 || got.Server.Port != 80 {
		t.Fatalf("merged settings: %+v, %v", got, err)
	}
	start, err := store.AutoStart(t.Context())
	if err != nil || start {
		t.Fatalf("auto-start after committing off: %t %v", start, err)
	}
}

func TestServerSettingsValidateHostAndPort(t *testing.T) {
	cfg := Defaults(t.TempDir())
	cfg.Server.Domain = " Exa_mple.com. "
	cfg.Server.Port = 65535
	cfg.PortForward.Minutes = 0.5
	cfg.PortForward.MaxPort = cfg.PortForward.MinPort
	if err := cfg.Validate(); err != nil || cfg.Server.Domain != "exa_mple.com" ||
		cfg.PortForward.Duration() != 30*time.Second {
		t.Fatalf("validated %+v, %v", cfg, err)
	}
	for _, domain := range []string{" ", "x..com", "http://example.com", "example.com:8080", "user@example.com"} {
		cfg.Server.Domain = domain
		if err := cfg.Validate(); err == nil {
			t.Errorf("accepted invalid service domain %q", domain)
		}
	}
}

func TestAutoStartAdmissionUsesLatestCommittedPolicy(t *testing.T) {
	store := NewStore(t.TempDir())
	if _, err := store.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	locked, permit := make(chan struct{}), make(chan struct{})
	saved := make(chan error, 1)
	go func() {
		_, _, err := store.Update(t.Context(), func(cfg *Config) error {
			close(locked)
			<-permit
			cfg.General.AutoStart = false
			return nil
		})
		saved <- err
	}()
	<-locked
	admitted := make(chan bool, 1)
	go func() {
		ok, err := store.AutoStart(t.Context())
		if err != nil {
			t.Error(err)
		}
		admitted <- ok
	}()
	close(permit)
	if err := <-saved; err != nil {
		t.Fatal(err)
	}
	if <-admitted {
		t.Fatal("automatic start passed after the Off save acquired the lock")
	}
}

func TestKubeconfigChangeResetsContextSelection(t *testing.T) {
	cfg := Defaults(t.TempDir())
	cfg.Kubernetes.ExcludedContexts = []string{"disabled"}
	same := cfg.Kubernetes.ConfigPath
	if err := (Edit{ConfigPath: &same}).Apply(&cfg); err != nil || len(cfg.Kubernetes.ExcludedContexts) != 1 {
		t.Fatalf("same file changed context selection: %+v %v", cfg.Kubernetes, err)
	}
	next := filepath.Join(t.TempDir(), "other")
	if err := (Edit{ConfigPath: &next}).Apply(&cfg); err != nil || len(cfg.Kubernetes.ExcludedContexts) != 0 {
		t.Fatalf("new file did not enable all contexts: %+v %v", cfg.Kubernetes, err)
	}
}

func TestPortForwardDraftsPreserveDisabledValues(t *testing.T) {
	cfg := Defaults(t.TempDir())
	cfg.PortForward.Minutes = 1.5
	cfg.PortForward.Trigger = ForwardOpened
	start, minutes := false, "invalid"
	if err := (Edit{KeepAlive: &start, Minutes: &minutes}).Apply(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.PortForward.KeepAlive || cfg.PortForward.Minutes != 1.5 || cfg.PortForward.Trigger != ForwardOpened {
		t.Fatalf("disabled policy replaced stored settings: %+v", cfg.PortForward)
	}
}
