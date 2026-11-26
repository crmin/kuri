package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/crmin/kuri/internal/daemon"
	"github.com/crmin/kuri/internal/forward"
	"github.com/crmin/kuri/internal/route"
)

func TestRecentForwards(t *testing.T) {
	state := daemon.State{PID: 32, Mode: "background", Started: time.Now()}
	for i := range 7 {
		at := state.Started.Add(time.Duration(i) * time.Second)
		state.Rows = append(state.Rows, forward.Row{
			Target:     route.Target{Context: "dev", Namespace: "default", Service: fmt.Sprintf("svc%d", i), Port: 80},
			LastAccess: &at, MenuHost: fmt.Sprintf("svc%d.default.dev.kuri.localhost", i),
		})
	}
	got, err := makeMenuState(state)
	if err != nil || got.Connected != 7 || len(got.Rows) != 5 || got.Rows[0].Label != "svc6.default.dev" || got.Rows[4].Label != "svc2.default.dev" {
		t.Fatalf("unexpected snapshot: %+v %v", got, err)
	}
	later := state.Started.Add(time.Minute)
	state.Rows[1].LastAccess = &later
	got, err = makeMenuState(state)
	if err != nil || got.Rows[0].Label != "svc1.default.dev" || got.Rows[4].Label != "svc3.default.dev" {
		t.Fatalf("latest request did not move into the list: %+v %v", got, err)
	}
	state.Rows[0].LastAccess = nil
	got, err = makeMenuState(state)
	if err != nil || got.RecentAvailable || got.Connected != 7 || len(got.Rows) != 0 {
		t.Fatalf("unexpected legacy snapshot: %+v %v", got, err)
	}
}
