//go:build cgo

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/crmin/kuri/internal/menuview"
)

type commandRunner struct {
	binary string
	env    []string
}

func newCommandRunner(launcher string) commandRunner {
	// Finder may omit the directories used by kubeconfig exec plugins.
	path := strings.Split(os.Getenv("PATH"), string(os.PathListSeparator))
	path = slices.DeleteFunc(path, func(dir string) bool { return dir == "" })
	for _, dir := range []string{"/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"} {
		if !slices.Contains(path, dir) {
			path = append(path, dir)
		}
	}
	env := slices.DeleteFunc(os.Environ(), func(entry string) bool {
		return strings.HasPrefix(entry, "PATH=") || strings.HasPrefix(entry, "KURI_LAUNCHD_START=")
	})
	env = append(env, "PATH="+strings.Join(path, string(os.PathListSeparator)))
	return commandRunner{filepath.Join(filepath.Dir(launcher), "kuri"), env}
}

type commandResult struct {
	stdout     []byte
	diagnostic string
	err        error
	timedOut   bool
}

func (r commandRunner) run(ctx context.Context, timeout, grace time.Duration, args ...string) commandResult {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var stdout bytes.Buffer
	var stderr startupOutput
	cmd := exec.CommandContext(ctx, r.binary, args...)
	cmd.Env, cmd.Stdout, cmd.Stderr = r.env, &stdout, &stderr
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = grace
	err := cmd.Run()
	diagnostic := strings.TrimSpace(string(stderr.data))
	timedOut := ctx.Err() != nil
	if timedOut {
		diagnostic = fmt.Sprintf("kuri %s did not complete before its deadline.", args[0])
	} else if diagnostic == "" && err != nil {
		diagnostic = strings.TrimSpace(stdout.String())
		if diagnostic == "" {
			diagnostic = err.Error()
		}
	}
	return commandResult{stdout.Bytes(), diagnostic, err, timedOut}
}

func (r commandRunner) state(ctx context.Context) (menuview.State, error) {
	result := r.run(ctx, 5*time.Second, 20*time.Second, "__menu_state")
	var state menuview.State
	if result.err != nil {
		return state, fmt.Errorf("%s", result.diagnostic)
	}
	if err := json.Unmarshal(result.stdout, &state); err != nil {
		return state, fmt.Errorf("Read kuri server state: %w", err)
	}
	if !((state.Mode == "stopped" && state.PID == 0 && state.Connected == 0 && len(state.Rows) == 0) ||
		((state.Mode == "background" || state.Mode == "foreground") && state.PID > 0 && state.Started != nil &&
			state.Connected >= len(state.Rows) && len(state.Rows) <= 5)) {
		return menuview.State{}, fmt.Errorf("Invalid kuri server state.")
	}
	return state, nil
}
