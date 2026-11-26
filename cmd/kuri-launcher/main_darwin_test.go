//go:build cgo

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/crmin/kuri/internal/menuview"
)

func TestLauncherRunsBundledStart(t *testing.T) {
	dir := t.TempDir()
	const fixture = `#!/bin/sh
printf '%s\n' "$#" "$1" "${KURI_LAUNCHD_START-unset}" "$PATH"
exit 23
`
	t.Setenv("KURI_LAUNCHD_START", "1")
	t.Setenv("PATH", dir+":/usr/local/bin")
	if err := os.WriteFile(filepath.Join(dir, "kuri"), []byte("#!/bin/sh\nexit 99\n"), 0755); err != nil {
		t.Fatal(err)
	}
	runner := testCommandRunner(t, dir, fixture)
	result := runner.run(t.Context(), time.Second, time.Second, "start")
	var exit *exec.ExitError
	if !errors.As(result.err, &exit) || exit.ExitCode() != 23 {
		t.Fatalf("launcher result: %+v", result)
	}
	want := strings.Join([]string{"1", "start", "unset", dir + ":/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin", ""}, "\n")
	if string(result.stdout) != want {
		t.Fatalf("invocation = %q, want %q", result.stdout, want)
	}
}

func TestFinderStartKeepsBoundedFailureLog(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	const fixture = `#!/bin/sh
if [ "$1" = __menu_state ]; then
    printf '%s\n' '{"mode":"stopped","pid":0,"connected":0,"rows":[]}'
    exit 0
fi
if [ -e "$HOME/success" ]; then exit 0; fi
printf 'Invalid kubeconfig.\n' >&2
/usr/bin/yes 'diagnostic' | /usr/bin/head -c 70000 >&2
exit 23
`
	runner := testCommandRunner(t, home, fixture)
	views := make(chan menuPresentation, 8)
	controller := &menuController{runner: runner, render: func(view menuPresentation) { views <- view },
		logFailure: func(data []byte) {
			if err := writeStartupLog(data); err != nil {
				t.Error(err)
			}
		}}
	controller.act(startAction, true)
	waitForMenu(t, views, func(view menuPresentation) bool { return !view.Busy && view.Known })
	path := filepath.Join(home, "Library", "Logs", "kuri", "startup.log")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != startupLogLimit || info.Mode().Perm() != 0600 {
		t.Fatalf("log size = %d, mode = %o", info.Size(), info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.HasPrefix(data, []byte("Invalid kubeconfig.\n")) {
		t.Fatalf("failed startup diagnosis is missing: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "success"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	controller.act(startAction, false)
	waitForMenu(t, views, func(view menuPresentation) bool { return !view.Busy && view.Known })
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("successful start changed the latest failure log: %v", err)
	}
}

func TestQuitWaitsForPendingStart(t *testing.T) {
	runner := &menuFixture{calls: make(chan []string, 4), resume: make(chan struct{}),
		stateValue: menuview.State{Mode: "background", PID: 42, Started: new(time.Now())}}
	terminated := make(chan struct{})
	controller := &menuController{runner: runner, render: func(menuPresentation) {}, terminate: func() { close(terminated) }}
	controller.act(startAction, true)
	if args := <-runner.calls; len(args) != 1 || args[0] != "start" {
		t.Fatal(args)
	}
	controller.act(quitAction, false)
	controller.act(quitAction, false)
	select {
	case <-terminated:
		t.Fatal("app closed before start settled")
	default:
	}
	close(runner.resume)
	select {
	case args := <-runner.calls:
		if len(args) != 3 || args[0] != "__menu_quit" || args[1] != "42" {
			t.Fatal(args)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("quit not issued")
	}
	select {
	case <-terminated:
	case <-time.After(3 * time.Second):
		t.Fatal("app did not terminate")
	}
	if len(runner.calls) != 0 {
		t.Fatal("duplicate quit")
	}
}

type menuFixture struct {
	calls      chan []string
	resume     chan struct{}
	stateValue menuview.State
}

func (f *menuFixture) run(ctx context.Context, _, _ time.Duration, args ...string) commandResult {
	f.calls <- args
	if args[0] == "start" {
		<-f.resume
	}
	return commandResult{}
}

func (f *menuFixture) state(context.Context) (menuview.State, error) { return f.stateValue, nil }

func waitForMenu(t *testing.T, views <-chan menuPresentation, ready func(menuPresentation) bool) {
	t.Helper()
	timeout := time.After(3 * time.Second)
	for {
		select {
		case view := <-views:
			if ready(view) {
				return
			}
		case <-timeout:
			t.Fatal("the menu did not settle")
		}
	}
}

func testCommandRunner(t *testing.T, dir, fixture string) commandRunner {
	t.Helper()
	bundle := filepath.Join(dir, "kuri test.app", "Contents", "MacOS")
	if err := os.MkdirAll(bundle, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "kuri"), []byte(fixture), 0755); err != nil {
		t.Fatal(err)
	}
	return newCommandRunner(filepath.Join(bundle, "launcher"))
}
