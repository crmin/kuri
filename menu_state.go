package main

import (
	"cmp"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/crmin/kuri/internal/daemon"
	"github.com/crmin/kuri/internal/forward"
	"github.com/crmin/kuri/internal/menuview"
	"github.com/crmin/kuri/internal/route"
)

func makeMenuState(server daemon.State) (menuview.State, error) {
	view := menuview.State{Mode: "stopped", RecentAvailable: true, Rows: []menuview.Forward{}}
	if server.PID == 0 {
		return view, nil
	}
	if server.PID < 0 || server.Started.IsZero() || (server.Mode != "foreground" && server.Mode != "background") {
		return view, errors.New("Invalid kuri server state.")
	}
	started := server.Started.UTC()
	view.Mode, view.PID, view.Started = server.Mode, server.PID, &started
	view.Connected = len(server.Rows)
	var recent []forward.Row
	for _, row := range server.Rows {
		if row.LastAccess == nil {
			view.RecentAvailable = false
			return view, nil
		}
		if !row.LastAccess.IsZero() {
			recent = append(recent, row)
		}
	}
	slices.SortFunc(recent, func(a, b forward.Row) int {
		return cmp.Or(b.LastAccess.Compare(*a.LastAccess), cmp.Compare(a.Context, b.Context),
			cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Service, b.Service), cmp.Compare(a.Port, b.Port))
	})
	for _, row := range recent[:min(5, len(recent))] {
		id, err := json.Marshal(row.Target)
		if err != nil {
			return view, err
		}
		item := menuview.Forward{ID: string(id), Label: route.ForwardLabel(row.Target, row.DefaultPort)}
		if row.MenuHost != "" {
			item.Label = strings.TrimSuffix(row.MenuHost, ".kuri.localhost")
			item.URL = "http://" + row.MenuHost
		}
		view.Rows = append(view.Rows, item)
	}
	return view, nil
}
