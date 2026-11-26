package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/crmin/kuri/internal/daemon"
	"github.com/crmin/kuri/internal/launchd"
	"github.com/crmin/kuri/internal/settings"
)

type testSettingsServer struct {
	store       settings.Store
	state       daemon.State
	blockedPort int
	policyErr   error
}

func (s *testSettingsServer) State(context.Context) (daemon.State, error) { return s.state, nil }

func (s *testSettingsServer) ApplySettings(_ context.Context, _ daemon.State, hash string) (daemon.State, error) {
	cfg, err := s.store.Read()
	if err != nil {
		return daemon.State{}, err
	}
	if cfg.Fingerprint() != hash {
		return daemon.State{}, daemon.ErrSettingsChanged
	}
	if cfg.Server.Port == s.blockedPort {
		return daemon.State{}, daemon.ErrPortOccupied
	}
	s.state.Port, s.state.ProxyFingerprint = cfg.Server.Port, cfg.ProxyFingerprint()
	return s.state, nil
}

func (*testSettingsServer) Start(context.Context) (int, error) {
	return 0, errors.New("unexpected Start")
}
func (*testSettingsServer) Stop(context.Context) (int, error) {
	return 0, errors.New("unexpected Stop")
}
func (*testSettingsServer) ConfirmPortChange(context.Context, launchd.PortChange) error {
	return errors.New("unexpected launchd port change")
}
func (*testSettingsServer) Registration(context.Context) (launchd.Registration, error) {
	return launchd.Registration{}, nil
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
