package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/crmin/kuri/internal/forward"
	"github.com/crmin/kuri/internal/menuview"
	"github.com/crmin/kuri/internal/route"
)

func (app *App) MenuState(ctx context.Context) (menuview.State, error) {
	return app.readMenuState(ctx, false)
}

func (app *App) MenuHistory(ctx context.Context) (menuview.State, error) {
	return app.readMenuState(ctx, true)
}

func (app *App) readMenuState(ctx context.Context, history bool) (menuview.State, error) {
	ctx, cancel := context.WithTimeout(ctx, controlTimeout)
	defer cancel()
	conn, pid, err := app.connectRuntime(ctx)
	if errors.Is(err, errNoServer) {
		return makeMenuState(State{}, 0, nil, true)
	}
	if err != nil {
		return menuview.State{}, err
	}
	defer conn.Close()
	answer, err := readOnlyState(ctx, conn, true, history)
	if err != nil {
		return menuview.State{}, err
	}
	if answer.Menu != nil {
		if answer.Menu.PID != pid {
			return menuview.State{}, errors.New("control response PID does not match the running process")
		}
		return *answer.Menu, nil
	}
	// An older v1 daemon ignores the menu hint and returns its normal snapshot.
	if answer.State.PID != pid {
		return menuview.State{}, errors.New("control response PID does not match the running process")
	}
	limit := menuview.LegacyRecentLimit
	if history {
		limit = menuview.RecentLimit
	}
	return legacyMenuState(answer.State, limit)
}

func legacyMenuState(server State, limit int) (menuview.State, error) {
	recent, available := forward.RecentRows(server.Rows, limit)
	return makeMenuState(server, len(server.Rows), recent, available)
}

func makeMenuState(server State, connected int, recent []forward.Row, available bool) (menuview.State, error) {
	view := menuview.State{Mode: "stopped", RecentAvailable: available, Rows: []menuview.Forward{}}
	if server.PID == 0 {
		return view, nil
	}
	if server.PID < 0 || server.Started.IsZero() || (server.Mode != "foreground" && server.Mode != "background") {
		return view, errors.New("Invalid kuri server state.")
	}
	started := server.Started.UTC()
	view.Mode, view.PID, view.Started = server.Mode, server.PID, &started
	view.Connected = connected
	view.Port, view.ProxyFingerprint = server.Port, server.ProxyFingerprint
	for _, row := range recent {
		id, err := json.Marshal(row.Target)
		if err != nil {
			return view, err
		}
		item := menuview.Forward{ID: string(id), Label: route.ForwardLabel(row.Target, row.DefaultPort)}
		if row.MenuHost != "" {
			item.Label = strings.TrimSuffix(row.MenuHost, ".kuri.localhost")
			item.URL = "http://" + row.MenuHost
		}
		if row.MenuLabel != "" {
			item.Label = row.MenuLabel
		}
		view.Rows = append(view.Rows, item)
	}
	return view, nil
}
