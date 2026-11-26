package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/crmin/kuri/internal/forward"
	"github.com/crmin/kuri/internal/menuview"
	"github.com/crmin/kuri/internal/route"
)

func TestRecentForwards(t *testing.T) {
	state := State{PID: 32, Mode: "background", Started: time.Now()}
	for i := range 7 {
		at := state.Started.Add(time.Duration(i) * time.Second)
		state.Rows = append(state.Rows, forward.Row{
			Target:     route.Target{Context: "dev", Namespace: "default", Service: fmt.Sprintf("svc%d", i), Port: 80},
			LastAccess: &at, MenuHost: fmt.Sprintf("svc%d.default.dev.kuri.localhost", i),
		})
	}
	got, err := legacyMenuState(state, menuview.LegacyRecentLimit)
	if err != nil || got.Connected != 7 || len(got.Rows) != 5 || got.Rows[0].Label != "svc6.default.dev" || got.Rows[4].Label != "svc2.default.dev" {
		t.Fatalf("unexpected snapshot: %+v %v", got, err)
	}
	later := state.Started.Add(time.Minute)
	state.Rows[1].LastAccess = &later
	got, err = legacyMenuState(state, menuview.LegacyRecentLimit)
	if err != nil || got.Rows[0].Label != "svc1.default.dev" || got.Rows[4].Label != "svc3.default.dev" {
		t.Fatalf("latest request did not move into the list: %+v %v", got, err)
	}
	state.Rows[0].LastAccess = nil
	got, err = legacyMenuState(state, menuview.LegacyRecentLimit)
	if err != nil || got.RecentAvailable || got.Connected != 7 || len(got.Rows) != 0 {
		t.Fatalf("unexpected legacy snapshot: %+v %v", got, err)
	}
}

func TestMenuStateRequestsBoundedSnapshot(t *testing.T) {
	app := testApp(t)
	if err := os.Mkdir(filepath.Join(app.home, ".kuri"), 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := openRuntimeControl(filepath.Join(app.home, ".kuri", "ctl"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var wholeCalls, menuCalls atomic.Int32
	started := time.Now()
	state := State{Mode: background, PID: os.Getpid(), Started: started, Token: "only-runtime"}
	go serveRuntimeControl(listener, controlCommands{
		state: func() State { return state },
		snapshot: func() []forward.Row {
			wholeCalls.Add(1)
			return make([]forward.Row, 7)
		},
		menu: func(history bool) (menuview.State, error) {
			menuCalls.Add(1)
			return makeMenuState(state, 7, []forward.Row{
				{Target: route.Target{Context: "prod", Service: "api", Namespace: "default", Port: 80}, MenuHost: "api.default.prod.kuri.localhost"},
			}, true)
		},
	})
	view, err := app.MenuState(t.Context())
	if err != nil || view.PID != os.Getpid() || view.Started == nil || !view.Started.Equal(started) || view.Connected != 7 || len(view.Rows) != 1 ||
		view.Rows[0].URL != "http://api.default.prod.kuri.localhost" || wholeCalls.Load() != 0 || menuCalls.Load() != 1 {
		t.Fatalf("menu response: %+v %v, whole calls = %d", view, err, wholeCalls.Load())
	}
	got, err := app.State(t.Context())
	if err != nil || len(got.Rows) != 7 || got.Token != state.Token || wholeCalls.Load() != 1 || menuCalls.Load() != 1 {
		t.Fatalf("ordinary state response: %+v %v", got, err)
	}
}

func TestRecentHistoryMenuCompatibility(t *testing.T) {
	app := testApp(t)
	if err := os.Mkdir(filepath.Join(app.home, ".kuri"), 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := openRuntimeControl(filepath.Join(app.home, ".kuri", "ctl"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	state := State{Mode: background, PID: os.Getpid(), Started: time.Now(), Token: "test-history"}
	go serveRuntimeControl(listener, controlCommands{menu: func(history bool) (menuview.State, error) {
		if history {
			return makeMenuState(state, 0, make([]forward.Row, menuview.RecentLimit), true)
		}
		return makeMenuState(state, 1, make([]forward.Row, 1), true)
	}})
	oldView, err := app.MenuState(t.Context())
	if err != nil || oldView.Connected != 1 || len(oldView.Rows) != 1 {
		t.Fatalf("legacy active-only state: %+v, %v", oldView, err)
	}
	currentView, err := app.MenuHistory(t.Context())
	if err != nil || currentView.Connected != 0 || len(currentView.Rows) != 10 {
		t.Fatalf("recent history state: %+v, %v", currentView, err)
	}
}

func TestCancelReadOnlyStateClosesSocket(t *testing.T) {
	for _, menu := range []bool{false, true} {
		t.Run(fmt.Sprint(menu), func(t *testing.T) {
			app := testApp(t)
			if err := os.Mkdir(filepath.Join(app.home, ".kuri"), 0700); err != nil {
				t.Fatal(err)
			}
			listener, err := openRuntimeControl(filepath.Join(app.home, ".kuri", "ctl"))
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			requestReceived := make(chan error, 1)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result := make(chan error, 1)
			go func() {
				if menu {
					_, err := app.MenuState(ctx)
					result <- err
				} else {
					_, err := app.State(ctx)
					result <- err
				}
			}()
			if err := listener.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
				t.Fatal(err)
			}
			conn, err := listener.AcceptUnix()
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			go func() { _, err := readRequest(conn); requestReceived <- err }()
			select {
			case err := <-requestReceived:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("server did not receive the state request")
			}
			cancel()
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled query: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("state query waited for the full socket deadline")
			}
		})
	}
}
