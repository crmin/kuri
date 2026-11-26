package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/crmin/kuri/internal/daemon"
	"github.com/crmin/kuri/internal/launchd"
	"github.com/crmin/kuri/internal/settings"
)

type testSettingsServer struct {
	store        settings.Store
	state        daemon.State
	blockedPort  int
	policyErr    error
	registration launchd.Registration
	ownedPort    int
	uninstall    func(context.Context, func(context.Context) error) error
	start        func(context.Context) (int, error)
	states       []daemon.State
}

func (s *testSettingsServer) State(context.Context) (daemon.State, error) {
	if len(s.states) != 0 {
		s.state, s.states = s.states[0], s.states[1:]
	}
	return s.state, nil
}

func (s *testSettingsServer) ApplySettings(_ context.Context, state daemon.State, hash string, retainPort bool) (daemon.State, error) {
	cfg, err := s.store.Read()
	if err != nil {
		return daemon.State{}, err
	}
	if cfg.Fingerprint() != hash {
		return daemon.State{}, daemon.ErrSettingsChanged
	}
	if retainPort {
		cfg.Server.Port = state.Port
	}
	if cfg.Server.Port == s.blockedPort {
		return daemon.State{}, daemon.ErrPortOccupied
	}
	s.state.Port, s.state.ProxyFingerprint = cfg.Server.Port, cfg.ProxyFingerprint()
	return s.state, nil
}

func (s *testSettingsServer) Start(ctx context.Context) (int, error) {
	if s.start != nil {
		return s.start(ctx)
	}
	return 0, errors.New("unexpected Start")
}
func (*testSettingsServer) Stop(context.Context) (int, error) {
	return 0, errors.New("unexpected Stop")
}
func (*testSettingsServer) ConfirmPortChange(context.Context, launchd.PortChange) error {
	return errors.New("unexpected launchd port change")
}
func (s *testSettingsServer) Registration(context.Context) (launchd.Registration, error) {
	return s.registration, nil
}
func (s *testSettingsServer) OwnsRegisteredPort(_ context.Context, port int) (bool, error) {
	return s.ownedPort == port, nil
}
func (s *testSettingsServer) UninstallStartupForReset(ctx context.Context, save func(context.Context) error) error {
	if s.uninstall == nil {
		return errors.New("unexpected startup uninstall")
	}
	return s.uninstall(ctx, save)
}
func (*testSettingsServer) PreparePortRestart(context.Context, daemon.State, string) error {
	return errors.New("unexpected foreground port change")
}
func (*testSettingsServer) RegisterForPort(context.Context, string) error {
	return errors.New("unexpected socket registration")
}
func (*testSettingsServer) HandoffDirectForeground(context.Context, daemon.State, string) error {
	return errors.New("unexpected direct foreground handoff")
}
func (s *testSettingsServer) EnsurePolicySupport(context.Context) error { return s.policyErr }

func TestFailedPortChangeRestoresSavedPortAndKeepsOtherFields(t *testing.T) {
	home := t.TempDir()
	store := settings.NewStore(home)
	cfg, err := store.Ensure(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	app := &testSettingsServer{store: store, blockedPort: 16080,
		state: daemon.State{PID: 123, Mode: "foreground", Port: 80, ProxyFingerprint: cfg.ProxyFingerprint()}}
	port, domain := "16080", "example.com"
	result := updateSettings(t.Context(), app, home, settings.Edit{Port: &port, Domain: &domain})
	if !result.saved || result.applied || result.portError != "Port 16080 is already in use by another process. The saved port was restored to 80." {
		t.Fatalf("result %+v", result)
	}
	got, err := store.Read()
	if err != nil || got.Server.Port != 80 || got.Server.Domain != domain || app.state.ProxyFingerprint != got.ProxyFingerprint() {
		t.Fatalf("restored settings %+v, %v, server %+v", got, err, app.state)
	}
}

func TestPortChangeKeepsStoppedProxyStopped(t *testing.T) {
	home := t.TempDir()
	store := settings.NewStore(home)
	app := &testSettingsServer{store: store}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	portText := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	port := fmt.Sprint(portText)
	result := updateSettings(t.Context(), app, home, settings.Edit{Port: &port})
	cfg, err := store.Read()
	if err != nil || !result.applied || cfg.Server.Port != portText || app.state.PID != 0 {
		t.Fatalf("result %+v, settings %+v, %v, state %+v", result, cfg, err, app.state)
	}
}

func TestSaveAllValidatesBeforeWriting(t *testing.T) {
	home := t.TempDir()
	store := settings.NewStore(home)
	original, err := store.Ensure(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	domain, port, min, max := "example.com", "34567", "30001", "30000"
	edit := settings.Edit{Domain: &domain, Port: &port, MinPort: &min, MaxPort: &max}
	result := updateSettings(t.Context(), &testSettingsServer{store: store}, home, edit)
	after, err := store.Read()
	if err != nil || result.saved || result.err == nil || after.Fingerprint() != original.Fingerprint() {
		t.Fatalf("invalid draft committed a partial update: %+v %+v %v", result, after, err)
	}
}

func TestDisableAutoStartRequiresCompatibleRegistration(t *testing.T) {
	home := t.TempDir()
	store := settings.NewStore(home)
	if _, err := store.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	app := &testSettingsServer{store: store, policyErr: daemon.ErrIncompatible}
	off := false
	result := updateSettings(t.Context(), app, home, settings.Edit{AutoStart: &off})
	got, err := store.Read()
	if err != nil || !got.General.AutoStart || result.saved || !errors.Is(result.err, daemon.ErrIncompatible) {
		t.Fatalf("unsupported registration accepted Off: %+v %+v %v", result, got, err)
	}
}

func TestResetSettingsWritesDefaults(t *testing.T) {
	home := t.TempDir()
	store := settings.NewStore(home)
	_, original, err := store.Update(t.Context(), func(c *settings.Config) error {
		c.General.AutoStart = false
		c.Server.Port, c.Server.Domain = 18080, "example.com"
		c.Kubernetes.ExcludedContexts = []string{"dev"}
		c.PortForward.KeepAlive = false
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	app := &testSettingsServer{store: store,
		state: daemon.State{PID: 123, Mode: "foreground", Port: original.Server.Port,
			ProxyFingerprint: original.ProxyFingerprint()}}
	result := updateSettings(t.Context(), app, home, settings.Edit{Reset: true})
	after, err := store.Read()
	if err != nil || !result.applied || after.Fingerprint() != settings.Defaults(home).Fingerprint() ||
		app.state.Port != after.Server.Port || app.state.ProxyFingerprint != after.ProxyFingerprint() {
		t.Fatalf("reset did not restore defaults: %+v %+v %v", result, after, err)
	}
}

func TestLauncherReportsSettingsPendingForOldDaemon(t *testing.T) {
	home := t.TempDir()
	store := settings.NewStore(home)
	app := &testSettingsServer{store: store, state: daemon.State{PID: 123, Mode: "background"}}
	view := loadSettingsView(t.Context(), home, app)
	if !view.ApplyPending || view.RuntimeWarning != "Update the installed kuri binary to apply the saved settings." {
		t.Fatalf("old daemon reported updated: %+v", view)
	}
}

func TestLaunchAtStartupUsesBundledBinary(t *testing.T) {
	home := t.TempDir()
	applications := filepath.Join(t.TempDir(), "Applications")
	bundle := filepath.Join(applications, "kuri.app", "Contents", "MacOS")
	if err := os.MkdirAll(bundle, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(applications, 0775); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(bundle, "kuri")
	const content = "the packaged kuri CLI"
	if err := os.WriteFile(binary, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
	staged, err := stageStartupBinary(home, binary)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(filepath.Dir(staged)) != filepath.Join(home, ".kuri", "installed") {
		t.Fatalf("binary staged outside the user's installer directory: %s", staged)
	}
	got, err := os.ReadFile(staged)
	if err != nil || string(got) != content {
		t.Fatalf("staged binary %q, %v", got, err)
	}
	info, err := os.Stat(staged)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("staged executable permissions: %v, %v", info, err)
	}
}

func TestResetSavesBeforeRemovingRegistration(t *testing.T) {
	home := t.TempDir()
	store := settings.NewStore(home)
	if _, err := store.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Path(), []byte("server: ["), 0600); err != nil {
		t.Fatal(err)
	}
	app := &testSettingsServer{store: store,
		registration: launchd.Registration{Program: "/fixture/kuri", Port: 80, AtBoot: true},
		state:        daemon.State{PID: 123, Mode: "background", Port: 80, Activated: true}}
	starts := 0
	app.uninstall = func(ctx context.Context, save func(context.Context) error) error {
		if err := save(ctx); err != nil {
			return err
		}
		cfg, err := store.Read()
		if err != nil || cfg.Fingerprint() != settings.Defaults(home).Fingerprint() {
			t.Fatalf("defaults at uninstall: %+v, %v", cfg, err)
		}
		app.registration = launchd.Registration{}
		app.state = daemon.State{}
		return nil
	}
	app.start = func(context.Context) (int, error) {
		starts++
		cfg, err := store.Read()
		if err != nil {
			return 0, err
		}
		app.state = daemon.State{PID: 456, Mode: "background", Port: cfg.Server.Port, ProxyFingerprint: cfg.ProxyFingerprint()}
		return app.state.PID, nil
	}
	result := updateSettings(t.Context(), app, home, settings.Edit{Reset: true})
	if !result.applied || starts != 1 || app.registration.Present() || app.state.Port != 80 {
		t.Fatalf("reset %+v, starts %d, registration %+v, state %+v", result, starts, app.registration, app.state)
	}
}

func TestResetSaveFailurePreservesRegistrationAndProxy(t *testing.T) {
	home := t.TempDir()
	store := settings.NewStore(home)
	if _, err := store.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	app := &testSettingsServer{store: store,
		registration: launchd.Registration{Program: "/fixture/kuri", Port: 80, AtBoot: true},
		state:        daemon.State{PID: 123, Mode: "background", Port: 80, Activated: true}}
	app.uninstall = func(ctx context.Context, save func(context.Context) error) error {
		if _, _, err := store.Update(ctx, func(c *settings.Config) error {
			c.Server.Domain = "newer.example.com"
			return nil
		}); err != nil {
			return err
		}
		if err := save(ctx); err != nil {
			return err
		}
		t.Fatal("registration changed despite losing the settings comparison")
		return nil
	}
	result := updateSettings(t.Context(), app, home, settings.Edit{Reset: true})
	if result.saved || !errors.Is(result.err, daemon.ErrSettingsChanged) || !app.registration.Present() || app.state.PID != 123 {
		t.Fatalf("failed reset %+v, registration %+v, state %+v", result, app.registration, app.state)
	}
}

func TestUnrelatedSaveLeavesPendingPort(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit settings.Edit
	}{
		{"general", settings.Edit{AutoStart: new(false)}},
		{"forwarding", settings.Edit{KeepAlive: new(false)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			store := settings.NewStore(home)
			original, saved, err := store.Update(t.Context(), func(c *settings.Config) error {
				c.Server.Port = 16080
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			app := &testSettingsServer{store: store, blockedPort: saved.Server.Port,
				state: daemon.State{PID: 123, Mode: "background", Port: original.Server.Port, ProxyFingerprint: original.ProxyFingerprint()}}
			result := updateSettings(t.Context(), app, home, tc.edit)
			cfg, err := store.Read()
			if err != nil || !result.applied || cfg.Server.Port != saved.Server.Port || app.state.Port != original.Server.Port {
				t.Fatalf("unrelated save applied or overwrote pending port: %+v, state %+v, file %+v, %v", result, app.state, cfg.Server, err)
			}
			if tc.edit.KeepAlive != nil {
				cfg.Server.Port = original.Server.Port
				if cfg.PortForward.KeepAlive || app.state.ProxyFingerprint != cfg.ProxyFingerprint() {
					t.Fatalf("keep-alive edit was not applied: %+v, %+v", cfg, app.state)
				}
			}
		})
	}
}

func TestSaveProbesRuntimeAgainAfterWriting(t *testing.T) {
	home := t.TempDir()
	store := settings.NewStore(home)
	original, err := store.Ensure(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	app := &testSettingsServer{store: store, states: []daemon.State{
		{}, {PID: 123, Mode: "background", Port: 80, ProxyFingerprint: original.ProxyFingerprint()},
	}}
	domain := "new.example.com"
	result := updateSettings(t.Context(), app, home, settings.Edit{Domain: &domain})
	cfg, err := store.Read()
	if err != nil || !result.applied || app.state.ProxyFingerprint != cfg.ProxyFingerprint() {
		t.Fatalf("startup missed the saved domain: %+v, %+v, %v", result, app.state, err)
	}
}

func TestStoppedProxyReusesOnlyVerifiedRegisteredPort(t *testing.T) {
	target, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	port := target.Addr().(*net.TCPAddr).Port
	portText := fmt.Sprint(port)
	for _, verified := range []bool{false, true} {
		t.Run(fmt.Sprintf("verified=%t", verified), func(t *testing.T) {
			home := t.TempDir()
			store := settings.NewStore(home)
			app := &testSettingsServer{store: store, registration: launchd.Registration{Program: "/fixture/kuri", Port: port}}
			if verified {
				app.ownedPort = port
			}
			result := updateSettings(t.Context(), app, home, settings.Edit{Port: &portText})
			cfg, err := store.Read()
			if err != nil || result.applied != verified || (cfg.Server.Port == port) != verified || app.state.PID != 0 {
				t.Fatalf("result %+v, settings %+v, state %+v, %v", result, cfg.Server, app.state, err)
			}
		})
	}
}
