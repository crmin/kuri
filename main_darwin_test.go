//go:build darwin && cgo

package main

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/crmin/kuri/internal/menuview"
)

func TestInstallFromWorkingDirectory(t *testing.T) {
	checkCLIError(t, "install", "Build an executable ./kuri in the current directory", false)
}

func TestLaunchdStartUsesActivatedSockets(t *testing.T) {
	checkCLIError(t, "start", "activate launchd socket HTTP", true)
}

func TestMenuStateCommand(t *testing.T) {
	home, err := os.MkdirTemp("", "kuri-menu-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	path := filepath.Join(home, ".kuri")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(path, "ctl"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	started := time.Now().UTC()
	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			return
		}
		defer conn.Close()
		var query map[string]any
		if err := json.NewDecoder(conn).Decode(&query); err == nil && query["action"] == "state" {
			_ = json.NewEncoder(conn).Encode(map[string]any{"version": 1, "state": map[string]any{
				"pid": os.Getpid(), "mode": "background", "started": started, "token": "private-test-token",
			}})
		}
	}()
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestCLIProcess$", "--", "__menu_state")
	cmd.Env = append(os.Environ(), "KURI_TEST_CLI=1", "HOME="+home)
	out, err := cmd.CombinedOutput()
	var got menuview.State
	if err != nil || json.Unmarshal(out, &got) != nil || got.PID != os.Getpid() || got.Mode != "background" ||
		got.Started == nil || !got.Started.Equal(started) || strings.Contains(string(out), "private-test-token") {
		t.Fatalf("kuri menu state: %s (%v)", out, err)
	}
}

func checkCLIError(t *testing.T, arg, want string, activated bool) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestCLIProcess$", "--", arg)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "KURI_TEST_CLI=1", "HOME="+cmd.Dir)
	if activated {
		cmd.Env = append(cmd.Env, "KURI_LAUNCHD_START=1")
	}
	out, err := cmd.CombinedOutput()
	var exited *exec.ExitError
	if !errors.As(err, &exited) || exited.ExitCode() != 1 || !strings.Contains(string(out), want) {
		t.Fatalf("kuri %s: got %q (%v), want exit 1 with %q", arg, out, err, want)
	}
}

func TestCLIProcess(t *testing.T) {
	if os.Getenv("KURI_TEST_CLI") == "1" {
		os.Exit(run(os.Args[slices.Index(os.Args, "--")+1:]))
	}
}
