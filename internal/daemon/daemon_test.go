package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/crmin/kuri/internal/accesslog"
	"github.com/crmin/kuri/internal/launchd"
	"github.com/crmin/kuri/internal/proxy"
)

func testApp(t *testing.T) *App {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "kuri-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	app := New(home, filepath.Join(home, "activation"), io.Discard, io.Discard)
	if err := os.Mkdir(filepath.Join(home, ".kube"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".kube", "config"), []byte(`apiVersion: v1
kind: Config
clusters: []
contexts: []
users: []
`), 0600); err != nil {
		t.Fatal(err)
	}
	return app
}

func TestStopMissing(t *testing.T) {
	app := testApp(t)
	if _, err := app.Stop(t.Context()); !errors.Is(err, ErrNoBackground) {
		t.Fatalf("stop without process: %v", err)
	}
	state, err := app.State(t.Context())
	if err != nil || state.PID != 0 {
		t.Fatalf("state = %+v, err = %v", state, err)
	}
	if _, err := os.Stat(filepath.Join(app.home, ".kuri", "access.log")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("inactive state/stop created an access log")
	}
}

func TestForegroundLogsAndStopOnlyBackground(t *testing.T) {
	app := testApp(t)
	var console bytes.Buffer
	app.stdout = &console
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan State, 1)
	stopped := make(chan error, 1)
	go func() {
		stopped <- app.serve(ctx, foreground, []net.Listener{listener}, &console, func(state State) error {
			started <- state
			return nil
		})
	}()
	select {
	case <-started:
	case err := <-stopped:
		t.Fatal(err)
	case <-time.After(3 * time.Second):
		t.Fatal("foreground did not become ready")
	}
	if _, err := app.Stop(t.Context()); !errors.Is(err, ErrNoBackground) {
		t.Fatalf("stop foreground: %v", err)
	}
	request, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+listener.Addr().String(), nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	cancel()
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	file, err := os.ReadFile(filepath.Join(app.home, ".kuri", "access.log"))
	if err != nil || !bytes.Equal(file, console.Bytes()) || bytes.Count(file, []byte("\n")) != 3 {
		t.Fatalf("file log and console differ, error=%v file=%s, console=%s", err, file, console.Bytes())
	}
}

func TestActivationChoosesSingleOwner(t *testing.T) {
	app := testApp(t)
	httpListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	unixListener, err := net.ListenUnix("unix", &net.UnixAddr{Name: app.activation, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- app.RunActivated(ctx, launchd.Sockets{HTTP: []net.Listener{httpListener}, Control: unixListener}, false)
	}()

	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: app.activation, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	decoder := json.NewDecoder(conn)
	if err := json.NewEncoder(conn).Encode(request{Version: version, Action: foreground}); err != nil {
		t.Fatal(err)
	}
	var ready reply
	if err := decoder.Decode(&ready); err != nil || ready.Error != "" ||
		ready.State.PID != os.Getpid() || ready.State.Mode != foreground || ready.State.Token == "" {
		t.Fatalf("ready=%+v error=%v", ready, err)
	}
	var firstLog map[string]any
	if err := decoder.Decode(&firstLog); err != nil || firstLog["event"] != "startup" {
		t.Fatalf("first log=%v, error=%v", firstLog, err)
	}
	if _, err := app.Start(t.Context()); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("competing start was not rejected: %v", err)
	}
	_ = conn.CloseWrite()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("foreground owner disconnect did not stop server")
	}
}

func TestIPCVersionMismatch(t *testing.T) {
	var output bytes.Buffer
	if err := writeReply(&output, reply{Version: version + 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := decodeReply(json.NewDecoder(&output)); !errors.Is(err, ErrIncompatible) {
		t.Fatalf("unexpected mismatch result: %v", err)
	}
}

func TestStopLogsPendingRequestsBeforeExit(t *testing.T) {
	requests, cancel := context.WithCancelCause(t.Context())
	var tracker requestGroup
	started := make(chan struct{})
	var log []string
	var networkStopped = make(chan error, 1)
	handler := tracker.wrap(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		close(started)
		<-req.Context().Done()
		if errors.Is(context.Cause(req.Context()), proxy.ErrShutdown) {
			log = append(log, "request failed")
		}
	}))
	server := &http.Server{BaseContext: func(net.Listener) context.Context { return requests }, Handler: handler}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { networkStopped <- server.Serve(listener) }()
	go func() {
		resp, err := http.Get("http://" + listener.Addr().String())
		if err == nil {
			resp.Body.Close()
		}
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not start")
	}
	drain(server, &tracker, cancel, 15*time.Millisecond)
	log = append(log, "stop")
	if strings.Join(log, ",") != "request failed,stop" {
		t.Fatalf("stop event raced a pending request: %v", log)
	}
	<-networkStopped
}

type stalledOutput struct {
	entered chan struct{}
	release chan struct{}
}

func (out stalledOutput) Write(data []byte) (int, error) {
	select {
	case out.entered <- struct{}{}:
	default:
	}
	<-out.release
	return len(data), nil
}

func TestForegroundShutdownDoesNotWaitForStalledOutput(t *testing.T) {
	app := testApp(t)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	out := stalledOutput{entered: make(chan struct{}, 1), release: make(chan struct{})}
	defer close(out.release)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stopped := make(chan error, 1)
	go func() {
		stopped <- app.serve(ctx, foreground, []net.Listener{listener}, out, nil)
	}()
	select {
	case <-out.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("console did not receive the startup log")
	}

	cancel()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(consoleFlushTimeout + 2*time.Second):
		t.Fatal("foreground is waiting for the blocked console")
	}
	data, err := os.ReadFile(filepath.Join(app.home, ".kuri", "access.log"))
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	for _, want := range []string{"startup", "stop"} {
		var got accesslog.Event
		if err := decoder.Decode(&got); err != nil || got.Event != want {
			t.Fatalf("event=%+v error=%v, want %q", got, err, want)
		}
	}
}
