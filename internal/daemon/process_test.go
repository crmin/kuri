package daemon

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestStartWaitsForReady(t *testing.T) {
	app := testApp(t)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: app.activation, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := os.Chmod(app.activation, 0600); err != nil {
		t.Fatal(err)
	}
	received := make(chan error, 1)
	ready := make(chan struct{})
	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			received <- err
			return
		}
		defer conn.Close()
		req, err := readRequest(conn)
		if err == nil && req.Action != background {
			err = fmt.Errorf("unexpected startup mode %q", req.Action)
		}
		received <- err
		<-ready
		_ = writeReply(conn, reply{State: State{PID: 42, Mode: background, Token: "fresh"}})
	}()

	type result struct {
		pid int
		err error
	}
	done := make(chan result, 1)
	go func() {
		pid, err := app.Start(t.Context())
		done <- result{pid, err}
	}()
	select {
	case err := <-received:
		if err != nil {
			t.Fatal(err)
		}
	case got := <-done:
		t.Fatalf("start returned before activation: %+v", got)
	case <-time.After(3 * time.Second):
		t.Fatal("no activation request")
	}
	select {
	case got := <-done:
		t.Fatalf("start returned before readiness: %+v", got)
	default:
	}
	close(ready)
	select {
	case got := <-done:
		if got.pid != 42 || got.err != nil {
			t.Fatalf("invalid startup response: %+v", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("start did not return after readiness")
	}
}

func TestQuitRequiresExitOfKnownServerWithNoSocket(t *testing.T) {
	app := testApp(t)
	if err := app.QuitBackground(t.Context(), os.Getpid(), time.Now()); err == nil {
		t.Fatal("absence of the socket did not prove the known process exited")
	}
	if err := app.QuitBackground(t.Context(), 0, time.Time{}); err != nil {
		t.Fatal(err)
	}
}

func TestStopWaitsForProcessExit(t *testing.T) {
	app := testApp(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestDaemonExitHelper$")
	cmd.Env = append(os.Environ(), "KURI_EXIT_TEST_HOME="+app.home, "GORACE=atexit_sleep_ms=0")
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	reaped := make(chan error, 1)
	go func() { reaped <- cmd.Wait() }()
	defer func() { _ = cmd.Process.Kill() }()
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
	defer cancel()
	for {
		state, err := app.State(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if state.PID == cmd.Process.Pid {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("subprocess did not become ready")
		case err := <-reaped:
			t.Fatalf("subprocess exited before readiness: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
	}
	started := time.Now()
	pid, err := app.Stop(ctx)
	if err != nil || pid != cmd.Process.Pid {
		t.Fatalf("stop: pid=%d err=%v", pid, err)
	}
	if time.Since(started) < 150*time.Millisecond {
		t.Fatal("stop returned before the acknowledged child had exited")
	}
	if err := <-reaped; err != nil {
		t.Fatal(err)
	}
}

func TestDaemonExitHelper(t *testing.T) {
	home := os.Getenv("KURI_EXIT_TEST_HOME")
	if home == "" {
		return
	}
	app := New(home, "", io.Discard, os.Stderr)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.serve(t.Context(), background, []net.Listener{listener}, nil, nil); err != nil {
		t.Fatal(err)
	}
	// Ensure the IPC acknowledgement and process exit are observable separately.
	time.Sleep(200 * time.Millisecond)
}

func TestForegroundActivationMirrorsOnlyLogLines(t *testing.T) {
	app := testApp(t)
	var stdout bytes.Buffer
	app.stdout = &stdout
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: app.activation, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	const logLine = "{\"event\":\"startup\"}\n"
	serve := make(chan error, 1)
	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			serve <- err
			return
		}
		defer conn.Close()
		if _, err := readRequest(conn); err != nil {
			serve <- err
			return
		}
		var frames bytes.Buffer
		if err := writeReply(&frames, reply{State: State{PID: 42, Mode: foreground, Token: "fresh"}}); err != nil {
			serve <- err
			return
		}
		frames.WriteString(logLine)
		_, err = conn.Write(frames.Bytes())
		serve <- err
	}()
	if _, err := app.activate(t.Context(), foreground, false); err != nil {
		t.Fatal(err)
	}
	if err := <-serve; err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != logLine {
		t.Fatalf("console received control data: %q", got)
	}
}
