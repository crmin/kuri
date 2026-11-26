package daemon

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/crmin/kuri/internal/settings"
)

func TestLiveSettingsApplyWithoutRestart(t *testing.T) {
	app := testApp(t)
	store := settings.NewStore(app.home)
	cfg, err := store.Ensure(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	const kubeconfig = `apiVersion: v1
kind: Config
current-context: dev
clusters:
- name: test
  cluster:
    server: http://127.0.0.1:1
contexts:
- name: dev
  context:
    cluster: test
    user: test
users:
- name: test
  user: {}
`
	if err := os.WriteFile(cfg.Kubernetes.ConfigPath, []byte(kubeconfig), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ready := make(chan State, 1)
	done := make(chan error, 1)
	go func() {
		done <- app.serve(ctx, foreground, []net.Listener{listener}, nil, func(state State) error { ready <- state; return nil })
	}()
	var initial State
	select {
	case initial = <-ready:
	case err := <-done:
		t.Fatal(err)
	}
	checkHost := func(host string, want int) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+listener.Addr().String(), nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("host %s: status %d, expected %d", host, resp.StatusCode, want)
		}
	}
	checkHost("api.default.kuri.localhost", http.StatusBadGateway)
	_, updated, err := store.Update(ctx, func(c *settings.Config) error {
		c.Server.Domain = "example.com"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.ApplySettings(ctx, initial, cfg.Fingerprint()); !errors.Is(err, ErrSettingsChanged) {
		t.Fatalf("stale apply: %v", err)
	}
	got, err := app.ApplySettings(ctx, initial, updated.Fingerprint())
	if err != nil || got.PID != initial.PID || !got.Started.Equal(initial.Started) || got.ProxyFingerprint != updated.ProxyFingerprint() {
		t.Fatalf("apply: %+v, %v", got, err)
	}
	checkHost("api.default.kuri.localhost", http.StatusBadRequest)
	checkHost("api.default.example.com", http.StatusBadGateway)
	_, updated, err = store.Update(ctx, func(c *settings.Config) error {
		c.Kubernetes.ExcludedContexts = []string{"dev"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.ApplySettings(ctx, initial, updated.Fingerprint()); err != nil {
		t.Fatal(err)
	}
	checkHost("api.default.example.com", http.StatusBadRequest)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(app.home, ".kuri", "access.log")); err != nil {
		t.Fatal(err)
	}
}

func TestSavePortRestartsRunningProxy(t *testing.T) {
	app := testApp(t)
	freePort := func() int {
		t.Helper()
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		return listener.Addr().(*net.TCPAddr).Port
	}
	previous := freePort()
	listeners, err := bindHTTP(previous)
	if err != nil {
		t.Fatal(err)
	}
	store := settings.NewStore(app.home)
	if _, _, err := store.Update(t.Context(), func(cfg *settings.Config) error { cfg.Server.Port = previous; return nil }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ready, done := make(chan State, 1), make(chan error, 1)
	go func() {
		done <- app.serve(ctx, foreground, listeners, nil, func(state State) error { ready <- state; return nil })
	}()
	var initial State
	select {
	case initial = <-ready:
	case err := <-done:
		t.Fatal(err)
	}
	newPort := freePort()
	_, cfg, err := store.Update(ctx, func(cfg *settings.Config) error { cfg.Server.Port = newPort; return nil })
	if err != nil {
		t.Fatal(err)
	}
	state, err := app.ApplySettings(ctx, initial, cfg.Fingerprint())
	if err != nil || state.Port != newPort {
		t.Fatalf("port switch: %+v, %v", state, err)
	}
	for _, address := range []string{"127.0.0.1", "::1"} {
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(address, strconv.Itoa(newPort)))
		if err != nil {
			t.Fatal(err)
		}
		conn.Close()
	}
	old, err := (&net.Dialer{}).DialContext(ctx, "tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(previous)))
	if err == nil {
		old.Close()
		t.Fatal("previous listener is still open")
	}

	occupied, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	_, cfg, err = store.Update(ctx, func(cfg *settings.Config) error {
		cfg.Server.Port = occupied.Addr().(*net.TCPAddr).Port
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.ApplySettings(ctx, initial, cfg.Fingerprint()); !errors.Is(err, ErrPortOccupied) {
		t.Fatalf("occupied port: %v", err)
	}
	status, err := app.State(ctx)
	if err != nil || status.Port != newPort || status.Mode != foreground {
		t.Fatalf("failed switch changed serving state: %+v %v", status, err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestActivateRejectsMismatchedPort(t *testing.T) {
	app := testApp(t)
	first, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := first.Addr().(*net.TCPAddr).Port
	second, err := net.Listen("tcp6", net.JoinHostPort("::1", strconv.Itoa(port)))
	if err != nil {
		first.Close()
		t.Fatal(err)
	}
	save := port + 1
	if save > 65535 {
		save = port - 1
	}
	if _, _, err := settings.NewStore(app.home).Update(t.Context(), func(cfg *settings.Config) error {
		cfg.Server.Port = save
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	err = app.serve(t.Context(), background, []net.Listener{first, second}, nil, func(State) error {
		t.Error("a mismatched listener reached readiness")
		return errors.New("unexpected readiness")
	})
	if err == nil || !strings.Contains(err.Error(), "Apply the saved port") {
		t.Fatalf("unexpected activation result %v", err)
	}
}
