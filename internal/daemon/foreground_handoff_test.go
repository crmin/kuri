package daemon

import (
	"bytes"
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/crmin/kuri/internal/launchd"
	"github.com/crmin/kuri/internal/settings"
)

func TestForegroundLogStreamSurvivesPortRestart(t *testing.T) {
	app := testApp(t)
	var output bytes.Buffer
	app.stdout = &output
	var transition atomic.Pointer[launchd.PortChange]
	app.portChange = func() (launchd.PortChange, error) {
		if change := transition.Load(); change != nil {
			return *change, nil
		}
		return launchd.PortChange{}, nil
	}
	ctx, stop := context.WithTimeout(t.Context(), 12*time.Second)
	defer stop()
	store := settings.NewStore(app.home)
	startSockets := func() (launchd.Sockets, int) {
		t.Helper()
		portProbe, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := portProbe.Addr().(*net.TCPAddr).Port
		portProbe.Close()
		http, err := bindHTTP(port)
		if err != nil {
			t.Fatal(err)
		}
		control, err := net.ListenUnix("unix", &net.UnixAddr{Name: app.activation, Net: "unix"})
		if err != nil {
			closeListeners(http)
			t.Fatal(err)
		}
		return launchd.Sockets{HTTP: http, Control: control}, port
	}
	savePort := func(port int) {
		t.Helper()
		if _, _, err := store.Update(ctx, func(cfg *settings.Config) error { cfg.Server.Port = port; return nil }); err != nil {
			t.Fatal(err)
		}
	}
	waitForPort := func(port int) State {
		t.Helper()
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			state, err := app.State(ctx)
			if err == nil && state.Port == port && state.ForegroundOwner > 0 && state.Mode == foreground {
				return state
			}
			select {
			case <-ctx.Done():
				t.Fatalf("foreground port %d: state %+v, error %v", port, state, err)
			case <-ticker.C:
			}
		}
	}
	run := func(sockets launchd.Sockets, eager bool) (context.CancelFunc, <-chan error) {
		t.Helper()
		runCtx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- app.RunActivated(runCtx, sockets, eager) }()
		return cancel, done
	}
	oldSockets, oldPort := startSockets()
	savePort(oldPort)
	cancelOld, oldDone := run(oldSockets, false)
	defer cancelOld()
	terminal, endTerminal := context.WithCancel(ctx)
	defer endTerminal()
	exit := make(chan error, 1)
	go func() {
		_, err := app.activate(terminal, foreground, false)
		exit <- err
	}()
	before := waitForPort(oldPort)
	const operation = "testForegroundTransfer"
	if err := app.PreparePortRestart(ctx, before, operation); err != nil {
		t.Fatal(err)
	}
	cancelOld()
	if err := <-oldDone; err != nil {
		t.Fatal(err)
	}
	newSockets, newPort := startSockets()
	savePort(newPort)
	change := launchd.PortChange{ID: operation, Port: newPort}
	transition.Store(&change)
	cancelNew, newDone := run(newSockets, true)
	defer cancelNew()
	if err := app.ConfirmPortChange(ctx, change); err != nil {
		t.Fatal(err)
	}
	after := waitForPort(newPort)
	if after.Token == before.Token || after.ForegroundOwner != before.ForegroundOwner {
		t.Fatalf("foreground handoff changed owner or kept old instance: before %+v after %+v", before, after)
	}
	endTerminal()
	if err := <-exit; err != nil {
		t.Fatal(err)
	}
	if err := <-newDone; err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(output.Bytes(), []byte(handoffEvent)) || bytes.Count(output.Bytes(), []byte(`"event":"startup"`)) != 2 {
		t.Fatalf("foreground log did not follow the restarted proxy: %s", output.Bytes())
	}
}
