package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
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
	schemaErr    error
	registration launchd.Registration
	ownedPort    int
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
func (*testSettingsServer) HoldProxyForRegistration(context.Context, func(daemon.State) error) (io.Closer, bool, error) {
	return nil, false, errors.New("unexpected registration handoff")
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
func (s *testSettingsServer) CheckSettingsSchema(context.Context, daemon.State, int) error {
	return s.schemaErr
}

func TestLegacyDaemonSettingsCompatibility(t *testing.T) {
	home := t.TempDir()
	store := settings.NewStore(home)
	defaults, err := store.Ensure(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	app := &testSettingsServer{store: store, state: daemon.State{
		PID: 123, Port: defaults.Server.Port, ProxyFingerprint: defaults.ProxyFingerprint(),
	}}
	if result := updateSettings(t.Context(), app, home, settings.Edit{AutoHealing: new(false)}); result.saved || result.err == nil {
		t.Fatalf("old live process accepted new YAML: %+v", result)
	}
	app.state = daemon.State{}
	app.schemaErr = errors.New("old activation")
	if result := updateSettings(t.Context(), app, home, settings.Edit{CheckSeconds: new("3")}); result.saved || result.err == nil {
		t.Fatalf("old activation process accepted new YAML: %+v", result)
	}
	if result := updateSettings(t.Context(), app, home, settings.Edit{Reset: true}); !result.saved || result.err != nil {
		t.Fatalf("reset should retain old YAML compatibility: %+v", result)
	}
	raw, err := os.ReadFile(store.Path())
	if err != nil || strings.Contains(string(raw), "auto_healing_enabled") || strings.Contains(string(raw), "connection_check_interval_seconds") {
		t.Fatalf("default settings used new keys: %s %v", raw, err)
	}
}

func TestProxyRegistrationUpdateRefusesPendingPort(t *testing.T) {
	home := t.TempDir()
	store := settings.NewStore(home)
	if _, _, err := store.Update(t.Context(), func(cfg *settings.Config) error {
		cfg.Server.Port = 8081
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	app := &testSettingsServer{store: store,
		registration: launchd.Registration{Program: "/usr/local/bin/kuri", Port: 8080, AtBoot: true, AppStartup: true},
		state:        daemon.State{PID: 123, Mode: "background", Port: 8080}}
	result := updateSettings(t.Context(), app, home, settings.Edit{PrepareProxy: true})
	if result.applied || result.err == nil || !strings.Contains(result.err.Error(), "Apply the port setting") || app.state.PID != 123 {
		t.Fatalf("port mismatch: %+v, state %+v", result, app.state)
	}
}

func TestIndependentCLIUpdateUsesCLIInstruction(t *testing.T) {
	home := t.TempDir()
	app := &testSettingsServer{registration: launchd.Registration{Program: "/usr/local/bin/kuri", Port: 80, AtBoot: true}}
	result := updateSettings(t.Context(), app, home, settings.Edit{PrepareProxy: true})
	if app.registration.CanUpdateFromApp() || result.applied || result.err == nil || !strings.Contains(result.err.Error(), "./kuri install") {
		t.Fatalf("independent CLI update: %+v", result)
	}
}

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

func TestResetPreservesLoginItemAndCLIStartup(t *testing.T) {
	home := t.TempDir()
	store := settings.NewStore(home)
	_, before, err := store.Update(t.Context(), func(cfg *settings.Config) error {
		cfg.General.LaunchAtLogin = true // Legacy YAML from the prior app.
		cfg.General.AutoStart = false
		cfg.Server.Domain = "saved.example.com"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	registration := launchd.Registration{Program: "/fixture/kuri", Port: 80, AtBoot: true}
	app := &testSettingsServer{store: store, registration: registration,
		state: daemon.State{PID: 123, Mode: "background", Port: 80, Activated: true, ProxyFingerprint: before.ProxyFingerprint()}}
	result := updateSettings(t.Context(), app, home, settings.Edit{Reset: true})
	after, err := store.Read()
	if !result.applied || err != nil || after.Fingerprint() != settings.Defaults(home).Fingerprint() ||
		app.registration != registration || app.state.PID != 123 {
		t.Fatalf("reset %+v, config %+v (%v), registration %+v, state %+v", result, after, err, app.registration, app.state)
	}
}

func TestLegacyRegistrationEditIsRejected(t *testing.T) {
	home := t.TempDir()
	store := settings.NewStore(home)
	app := &testSettingsServer{store: store}
	result := updateSettings(t.Context(), app, home, settings.Edit{Register: new(true)})
	if result.saved || result.applied || result.err == nil {
		t.Fatalf("legacy registration edit: %+v", result)
	}
	if _, err := os.Stat(store.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy registration edit unexpectedly wrote settings: %v", err)
	}
}

func TestDisableAutoStartKeepsRunningProxy(t *testing.T) {
	home := t.TempDir()
	store := settings.NewStore(home)
	cfg, err := store.Ensure(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	app := &testSettingsServer{store: store, state: daemon.State{
		PID: 123, Mode: "background", Port: 80, ProxyFingerprint: cfg.ProxyFingerprint(),
	}}
	result := updateSettings(t.Context(), app, home, settings.Edit{AutoStart: new(false)})
	after, err := store.Read()
	if !result.applied || err != nil || after.General.AutoStart || app.state.PID != 123 {
		t.Fatalf("auto-start change %+v, config %+v (%v), state %+v", result, after, err, app.state)
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
