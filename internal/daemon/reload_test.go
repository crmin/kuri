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
	"time"

	"github.com/crmin/kuri/internal/settings"
	"golang.org/x/sys/unix"
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
	if _, err := app.ApplySettings(ctx, initial, cfg.Fingerprint(), false); !errors.Is(err, ErrSettingsChanged) {
		t.Fatalf("stale apply: %v", err)
	}
	got, err := app.ApplySettings(ctx, initial, updated.Fingerprint(), false)
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
	if _, err := app.ApplySettings(ctx, initial, updated.Fingerprint(), false); err != nil {
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
	state, err := app.ApplySettings(ctx, initial, cfg.Fingerprint(), false)
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
	if _, err := app.ApplySettings(ctx, initial, cfg.Fingerprint(), false); !errors.Is(err, ErrPortOccupied) {
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

func TestStartupPublishesLatestSavedSettings(t *testing.T) {
	app := testApp(t)
	delayedConfig := newKubeconfigFIFO(t, app.home)
	store := settings.NewStore(app.home)
	original, _, err := store.Update(t.Context(), func(cfg *settings.Config) error {
		cfg.Kubernetes.ConfigPath = delayedConfig
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ready, done := make(chan State, 1), make(chan error, 1)
	go func() {
		done <- app.serve(ctx, foreground, []net.Listener{listener}, nil, func(state State) error { ready <- state; return nil })
	}()
	writer := waitForKubeconfigReader(t, delayedConfig)
	state, err := app.State(ctx)
	if err != nil || state.PID != 0 {
		t.Fatalf("runtime published before initialization: %+v, %v", state, err)
	}
	_, latest, err := store.Update(ctx, func(cfg *settings.Config) error {
		cfg.Kubernetes.ConfigPath = original.Kubernetes.ConfigPath
		cfg.Server.Domain = "new.example.com"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	writeKubeconfigAndClose(t, writer, original.Kubernetes.ConfigPath)
	select {
	case state := <-ready:
		if state.ProxyFingerprint != latest.ProxyFingerprint() || state.AppliedSettings.Server.Domain != latest.Server.Domain {
			t.Fatalf("startup published stale settings: %+v", state)
		}
	case err := <-done:
		t.Fatal(err)
	case <-time.After(4 * time.Second):
		t.Fatal("runtime never became ready")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestKubeconfigPreparationLeavesCurrentRequestsAvailable(t *testing.T) {
	app := testApp(t)
	store := settings.NewStore(app.home)
	initial, err := store.Ensure(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ready, done := make(chan State, 1), make(chan error, 1)
	go func() {
		done <- app.serve(ctx, foreground, []net.Listener{listener}, nil, func(state State) error { ready <- state; return nil })
	}()
	var state State
	select {
	case state = <-ready:
	case err := <-done:
		t.Fatal(err)
	case <-time.After(4 * time.Second):
		t.Fatal("runtime never became ready")
	}
	delayedConfig := newKubeconfigFIFO(t, app.home)
	_, update, err := store.Update(ctx, func(cfg *settings.Config) error {
		cfg.Kubernetes.ConfigPath = delayedConfig
		cfg.Server.Domain = "stale.example.com"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	applied := make(chan error, 1)
	go func() {
		_, err := app.ApplySettings(ctx, state, update.Fingerprint(), false)
		applied <- err
	}()
	writer := waitForKubeconfigReader(t, delayedConfig)
	requestCtx, stopRequest := context.WithTimeout(ctx, 2*time.Second)
	defer stopRequest()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, "http://"+listener.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal("backend preparation blocked the current HTTP handler:", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("current route status = %d", resp.StatusCode)
	}
	if _, _, err := store.Update(ctx, func(cfg *settings.Config) error {
		cfg.Server.Domain = "latest.example.com"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	writeKubeconfigAndClose(t, writer, initial.Kubernetes.ConfigPath)
	if err := <-applied; !errors.Is(err, ErrSettingsChanged) {
		t.Fatalf("stale prepared backend applied: %v", err)
	}
	current, err := app.State(ctx)
	if err != nil || current.ProxyFingerprint != initial.ProxyFingerprint() {
		t.Fatalf("current runtime changed after rejected application: %+v, %v", current, err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func newKubeconfigFIFO(t *testing.T, home string) string {
	t.Helper()
	path := filepath.Join(home, "delayed-kubeconfig")
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func waitForKubeconfigReader(t *testing.T, path string) *os.File {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		fd, err := unix.Open(path, unix.O_WRONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
		if err == nil {
			writer := os.NewFile(uintptr(fd), path)
			t.Cleanup(func() { _ = writer.Close() })
			return writer
		}
		if !errors.Is(err, unix.ENXIO) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("kubeconfig preparation never reached the test barrier")
	return nil
}

func writeKubeconfigAndClose(t *testing.T, writer *os.File, validPath string) {
	t.Helper()
	data, err := os.ReadFile(validPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
}
