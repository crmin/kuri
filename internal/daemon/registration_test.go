package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/crmin/kuri/internal/launchd"
	"golang.org/x/sys/unix"
)

func TestStartRegistersOnceAndReusesActivation(t *testing.T) {
	for _, mode := range []string{background, foreground} {
		t.Run(mode, func(t *testing.T) {
			app := testApp(t)
			out := &bytes.Buffer{}
			app.stdout = out
			registerCalls, directCalls := 0, 0
			app.direct = func(context.Context, string) (int, error) {
				directCalls++
				return 0, bindError(unix.EACCES)
			}
			result := make(chan error, 1)
			app.register = func(ctx context.Context) error {
				registerCalls++
				listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: app.activation, Net: "unix"})
				if err != nil {
					return err
				}
				t.Cleanup(func() { listener.Close() })
				if err := os.Chmod(app.activation, 0600); err != nil {
					return err
				}
				go func() {
					for i := range 2 {
						conn, err := listener.AcceptUnix()
						if err != nil {
							result <- err
							return
						}
						_ = conn.SetDeadline(time.Now().Add(time.Second))
						req, err := readRequest(conn)
						switch {
						case err != nil:
						case req.Action != mode:
							err = errors.New("wrong activation mode")
						default:
							err = writeReply(conn, reply{State: State{PID: 40 + i, Mode: mode, Token: "token"}})
							if err == nil && mode == foreground {
								_, err = io.WriteString(conn, "{\"event\":\"startup\"}\n")
							}
						}
						conn.Close()
						if err != nil {
							result <- err
							return
						}
					}
					result <- nil
				}()
				return nil
			}
			for i := range 2 {
				if mode == foreground {
					if err := app.RunForeground(t.Context()); err != nil {
						t.Fatal(err)
					}
				} else if pid, err := app.Start(t.Context()); err != nil || pid != 40+i {
					t.Fatalf("start pid=%d err=%v", pid, err)
				}
			}
			if err := <-result; err != nil {
				t.Fatal(err)
			}
			if registerCalls != 1 || directCalls != 1 {
				t.Fatalf("used direct binding %d times and registration %d times", directCalls, registerCalls)
			}
			if mode == foreground && out.String() != "{\"event\":\"startup\"}\n{\"event\":\"startup\"}\n" {
				t.Fatalf("unexpected foreground output %q", out)
			}
		})
	}
}

func TestStartRegistersAfterStaleActivationSocket(t *testing.T) {
	app := testApp(t)
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: app.activation, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	stale.SetUnlinkOnClose(false)
	if err := stale.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(app.activation, 0600); err != nil {
		t.Fatal(err)
	}
	registrationCanceled := errors.New("registration canceled")
	app.direct = func(context.Context, string) (int, error) {
		return 0, bindError(unix.EPERM)
	}
	app.register = func(context.Context) error { return registrationCanceled }
	if _, err := app.Start(t.Context()); !errors.Is(err, registrationCanceled) {
		t.Fatalf("stale socket prevented registration: %v", err)
	}
}

func TestStartupBindPermissionSurvivesDetachedIPC(t *testing.T) {
	var message bytes.Buffer
	if err := writeReply(&message, failure(bindError(unix.EACCES))); err != nil {
		t.Fatal(err)
	}
	if _, err := decodeReply(json.NewDecoder(&message)); !errors.Is(err, errBindPermission) {
		t.Fatalf("startup client lost the detached bind error: %v", err)
	}
}

func TestAlreadyUsableActivationDoesNotPromptOnStartupError(t *testing.T) {
	app := testApp(t)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: app.activation, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := os.Chmod(app.activation, 0600); err != nil {
		t.Fatal(err)
	}
	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = readRequest(conn)
		_ = writeReply(conn, failure(ErrIncompatible))
	}()
	app.register = func(context.Context) error {
		t.Fatal("tried to install after an established activation connection")
		return nil
	}
	if _, err := app.Start(t.Context()); !errors.Is(err, ErrIncompatible) {
		t.Fatalf("expected existing daemon error, got %v", err)
	}
}

func TestEagerRegistrationUsesQueuedForegroundRequest(t *testing.T) {
	app := testApp(t)
	ctrl, err := net.ListenUnix("unix", &net.UnixAddr{Name: app.activation, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer ctrl.Close()
	conn, err := net.DialUnix("unix", nil, ctrl.Addr().(*net.UnixAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if err := sendRequest(conn, foreground); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		finished <- app.RunActivated(ctx, launchd.Sockets{Control: ctrl}, true)
	}()
	state, err := decodeReply(json.NewDecoder(conn))
	if err != nil || state.Mode != foreground {
		t.Fatalf("queued foreground request returned %+v, %v", state, err)
	}
	_ = conn.CloseWrite()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("foreground activation did not exit")
	}
}
