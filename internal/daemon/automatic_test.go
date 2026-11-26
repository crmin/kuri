package daemon

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/crmin/kuri/internal/launchd"
	"github.com/crmin/kuri/internal/settings"
)

func TestAutoStartPolicyForBootHTTPAndManual(t *testing.T) {
	for _, eager := range []bool{false, true} {
		t.Run(map[bool]string{true: "boot", false: "http"}[eager], func(t *testing.T) {
			app := testApp(t)
			if _, _, err := settings.NewStore(app.home).Update(t.Context(), func(c *settings.Config) error {
				c.General.AutoStart = false
				return nil
			}); err != nil {
				t.Fatal(err)
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
			go func() { done <- app.RunActivated(ctx, sockets, eager) }()
			if n, err := incoming.Read(make([]byte, 1)); n != 0 || err == nil {
				t.Fatalf("passive connection received proxy data: %d bytes, %v", n, err)
			} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				t.Fatalf("passive connection was not closed: %v", err)
			}
			if state, err := app.State(ctx); state.PID != 0 || err != nil {
				t.Fatalf("automatic proxy became ready: %+v, %v", state, err)
			}
			conn, err := (&net.Dialer{}).DialContext(ctx, "unix", app.activation)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
			if err := sendRequest(conn.(*net.UnixConn), foreground); err != nil {
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
