package cli

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/crmin/kuri/internal/daemon"
	"golang.org/x/term"
)

const day = 24 * time.Hour

func WriteState(out io.Writer, state daemon.State, now time.Time) error {
	view := stateView{content: "Status: stopped"}
	if state.PID != 0 {
		view.content = fmt.Sprintf("Status: running\nPID: %d\nStarted: %s\nConnected services: %d\n\n%s",
			state.PID, state.Started.Local().Format(time.RFC3339), len(state.Rows), serviceTable(state, now))
	}
	if file, ok := out.(*os.File); ok && state.PID != 0 && term.IsTerminal(int(file.Fd())) {
		width, height, err := term.GetSize(int(file.Fd()))
		if err == nil && width >= lipgloss.Width(view.content) && height > lipgloss.Height(view.content) {
			_, err := tea.NewProgram(view, tea.WithOutput(out), tea.WithInput(nil), tea.WithoutSignalHandler()).Run()
			return err
		}
	}
	_, err := fmt.Fprintln(out, view.View().Content)
	return err
}

type stateView struct{ content string }

func (stateView) Init() tea.Cmd { return tea.Quit }

func (m stateView) Update(tea.Msg) (tea.Model, tea.Cmd) { return m, nil }

func (m stateView) View() tea.View { return tea.NewView(m.content) }

func serviceTable(state daemon.State, now time.Time) string {
	titles := []string{"ctx", "namespace", "service", "remote port", "local port", "connected"}
	columns := make([]table.Column, len(titles))
	for i, title := range titles {
		columns[i] = table.Column{Title: title, Width: len(title)}
	}

	rows := make([]table.Row, 0, len(state.Rows))
	for _, service := range state.Rows {
		remote := strconv.Itoa(int(service.Port))
		if service.PortName != "" {
			remote += " (" + service.PortName + ")"
		}
		row := table.Row{
			service.Context, service.Namespace, service.Service, remote,
			strconv.Itoa(service.LocalPort), connected(now.Sub(service.Connected)),
		}
		for i, value := range row {
			columns[i].Width = max(columns[i].Width, lipgloss.Width(value))
		}
		rows = append(rows, row)
	}

	width := 0
	for _, column := range columns {
		width += column.Width + 2
	}
	bubble := table.New(table.WithColumns(columns), table.WithRows(rows),
		table.WithWidth(width), table.WithHeight(len(rows)+1))
	bubble.SetStyles(table.Styles{
		Header: lipgloss.NewStyle().Padding(0, 1),
		Cell:   lipgloss.NewStyle().Padding(0, 1),
	})
	return strings.TrimRight(bubble.View(), " \n")
}

func connected(elapsed time.Duration) string {
	elapsed = max(elapsed, 0)
	unit, suffix := time.Second, "s"
	switch {
	case elapsed >= day:
		unit, suffix = day, "d"
	case elapsed >= time.Hour:
		unit, suffix = time.Hour, "h"
	case elapsed >= 2*time.Minute:
		unit, suffix = time.Minute, "m"
	}
	return fmt.Sprintf("%d%s ago", elapsed.Round(unit)/unit, suffix)
}
