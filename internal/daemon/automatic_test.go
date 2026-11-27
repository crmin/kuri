package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/crmin/kuri/internal/accesslog"
	"github.com/crmin/kuri/internal/launchd"
	"github.com/crmin/kuri/internal/settings"
)

func TestAutomaticPublicationHonorsLastStop(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS boot session")
	}
	app := testApp(t)
	store := settings.NewStore(app.home)
	app.automaticStart, app.passiveStart = true, true
	// An activation that already accepted a connection may reach publication
	// after Stop commits its pause. That instance must not become visible.
	if err := store.SetProxyPaused(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	logger, err := accesslog.Open(filepath.Join(store.Directory(), "access.log"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.prepareRuntime(t.Context(), nil, logger); !errors.Is(err, ErrAutoStartDisabled) {
		t.Fatalf("automatic publication: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.Directory(), "ctl")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("automatic instance published its control socket: %v", err)
	}
}

func TestAutoStartPolicyForBootHTTPAndManual(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		eager, paused, enabled bool
	}{
		{name: "http"}, {name: "boot", eager: true},
		{name: "socket-only", enabled: true},
		{name: "stopped-http", paused: true}, {name: "stopped-boot", eager: true, paused: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := testApp(t)
			store := settings.NewStore(app.home)
			if tc.paused {
				if runtime.GOOS != "darwin" {
					t.Skip("macOS boot session")
				}
				if err := store.SetProxyPaused(t.Context(), true); err != nil {
					t.Fatal(err)
				}
			} else if !tc.enabled {
				if _, _, err := store.Update(t.Context(), func(c *settings.Config) error {
					c.General.AutoStart = false
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			httpListener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			control, err := net.ListenUnix("unix", &net.UnixAddr{Name: app.activation, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer control.Close()
			defer httpListener.Close()
			sockets := launchd.Sockets{HTTP: []net.Listener{httpListener}, Control: control}
			ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
			defer cancel()
			incoming, err := (&net.Dialer{}).DialContext(ctx, "tcp4", httpListener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer incoming.Close()
			_ = incoming.SetDeadline(time.Now().Add(3 * time.Second))
			_, _ = incoming.Write([]byte("GET / HTTP/1.1\r\nHost: api.default.kuri.localhost\r\n\r\n"))
			done := make(chan error, 1)
			go func() { done <- app.RunActivated(ctx, sockets, tc.eager) }()
			if n, err := incoming.Read(make([]byte, 1)); n != 0 || err == nil {
				t.Fatalf("passive connection received proxy data: %d bytes, %v", n, err)
			} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				t.Fatalf("passive connection was not closed: %v", err)
			}
			if state, err := app.State(ctx); state.PID != 0 || err != nil {
				t.Fatalf("automatic proxy became ready: %+v, %v", state, err)
			}
			if !app.SupportsLiveProxyPause(ctx) {
				t.Fatal("idle activation did not advertise pause support")
			}
			conn, err := (&net.Dialer{}).DialContext(ctx, "unix", app.activation)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
			request := request{Version: version, Action: foreground, Automatic: tc.paused}
			if err := json.NewEncoder(conn).Encode(request); err != nil {
				t.Fatal(err)
			}
			state, err := decodeReply(json.NewDecoder(conn))
			if state.PID == 0 || state.Mode != foreground || err != nil {
				t.Fatalf("explicit start: %+v, %v", state, err)
			}
			cancel()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}
