package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/crmin/kuri/internal/daemon"
	"github.com/crmin/kuri/internal/forward"
	"github.com/crmin/kuri/internal/route"
)

func TestStateSnapshot(t *testing.T) {
	now := time.Date(2026, 11, 26, 1, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	if err := WriteState(&out, daemon.State{}, now); err != nil || out.String() != "Status: stopped\n" {
		t.Fatalf("unexpected stopped state: %q, %v", out.String(), err)
	}
	out.Reset()
	if err := WriteState(&out, daemon.State{PID: 42, Started: now}, now); err != nil || !strings.Contains(out.String(), "Connected services: 0\n\n") || !strings.HasSuffix(out.String(), "connected\n") {
		t.Fatalf("unexpected running state without connections: %q, %v", out.String(), err)
	}
	out.Reset()
	state := daemon.State{PID: 42, Started: now.Add(-time.Hour), Rows: []forward.Row{
		{Target: route.Target{Context: "dev", Namespace: "default", Service: "api", Port: 8080},
			PortName: "http", LocalPort: 33333, Connected: now.Add(-185 * time.Second)},
		{Target: route.Target{Context: "prod", Namespace: "ops", Service: "other", Port: 8081},
			LocalPort: 34444, Connected: now.Add(-5 * time.Hour)},
	}}
	if err := WriteState(&out, state, now); err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"Status: running", "PID: 42", "Started:", "Connected services: 2",
		"ctx", "namespace", "service", "remote port", "local port", "connected",
		"dev", "default", "api", "8080 (http)", "33333", "3m ago", "prod", "ops", "other", "8081", "34444", "5h ago"} {
		if !strings.Contains(out.String(), part) {
			t.Errorf("state output is missing %q:\n%s", part, out.String())
		}
	}
	if strings.Contains(out.String(), "\x1b") {
		t.Fatalf("redirected state contains terminal controls: %q", out.String())
	}
}

func TestStateTerminalKeepsLastRow(t *testing.T) {
	now := time.Now()
	rows := []forward.Row{
		{Target: route.Target{Context: "dev", Namespace: "default", Service: "first", Port: 8080}, LocalPort: 33333, Connected: now.Add(-time.Minute)},
		{Target: route.Target{Context: "dev", Namespace: "default", Service: "last", Port: 8081}, LocalPort: 34444, Connected: now.Add(-3 * time.Minute)},
	}
	for _, count := range []int{1, 2} {
		state := daemon.State{Rows: rows[:count]}
		view := stateView{content: serviceTable(state, now)}
		var out bytes.Buffer
		_, err := tea.NewProgram(view, tea.WithOutput(&out), tea.WithInput(nil), tea.WithoutSignalHandler(),
			tea.WithWindowSize(120, 24), tea.WithEnvironment([]string{"TERM=xterm-256color"})).Run()
		if err != nil {
			t.Fatal(err)
		}
		output := out.String()
		lastRow := strings.LastIndex(output, rows[count-1].Service)
		clear := strings.LastIndex(output, "\x1b[J")
		if lastRow < 0 || clear <= lastRow || !strings.Contains(output[lastRow:clear], "\n") {
			t.Errorf("with %d rows, the renderer should clear below the last row: %q", count, output)
		}
	}
}

func TestConnectedRounding(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{-time.Second, "0s ago"},
		{0, "0s ago"},
		{119*time.Second + 750*time.Millisecond, "120s ago"},
		{120 * time.Second, "2m ago"},
		{185 * time.Second, "3m ago"},
		{210 * time.Second, "4m ago"},
		{59*time.Minute + 45*time.Second, "60m ago"},
		{60 * time.Minute, "1h ago"},
		{23*time.Hour + 45*time.Minute, "24h ago"},
		{24 * time.Hour, "1d ago"},
		{36 * time.Hour, "2d ago"},
	} {
		if got := connected(tc.d); got != tc.want {
			t.Errorf("connected(%v)=%q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestHelpCommands(t *testing.T) {
	var out bytes.Buffer
	if err := WriteHelp(&out); err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"kuri", "foreground", "help", "start", "background", "stop", "kuri status", "kuri install", "kuri uninstall", "SHA-256",
		"portName.service.namespace.ctx.kuri.localhost"} {
		if !strings.Contains(out.String(), part) {
			t.Errorf("help is missing %q", part)
		}
	}
	if strings.Contains(out.String(), "kuri state") {
		t.Fatal("help lists the old command")
	}
	if strings.Contains(out.String(), "port.service.namespace.ctx.kuri.localhost") {
		t.Fatal("help lists the old numeric domain style")
	}
}
