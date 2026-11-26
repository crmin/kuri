//go:build darwin && cgo

package main

import (
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func TestInstallFromWorkingDirectory(t *testing.T) {
	checkCLIError(t, "install", "Build an executable ./kuri in the current directory", false)
}

func TestLaunchdStartUsesActivatedSockets(t *testing.T) {
	checkCLIError(t, "start", "activate launchd socket HTTP", true)
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
