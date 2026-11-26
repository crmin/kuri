package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLauncherRunsBundledStart(t *testing.T) {
	dir := t.TempDir()
	const fixture = `#!/bin/sh
printf '%s\n' "$#" "$1" "${KURI_LAUNCHD_START-unset}" "$PATH" >> "$KURI_TEST_RESULT"
exit 23
`
	launcher := buildTestLauncher(t, dir, fixture)
	if err := os.WriteFile(filepath.Join(dir, "kuri"), []byte("#!/bin/sh\nexit 99\n"), 0755); err != nil {
		t.Fatal(err)
	}
	result := filepath.Join(dir, "invocations")
	cmd := exec.Command(launcher, "install")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "KURI_TEST_RESULT="+result, "KURI_LAUNCHD_START=1", "PATH="+dir+":/usr/local/bin")
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 23 {
		t.Fatalf("launcher exit = %v, want fixture exit 23\n%s", err, output)
	}
	got, err := os.ReadFile(result)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{"1", "start", "unset", dir + ":/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin", ""}, "\n")
	if string(got) != want {
		t.Fatalf("invocations = %q, want %q", got, want)
	}
}

func TestFinderStartKeepsBoundedFailureLog(t *testing.T) {
	home := t.TempDir()
	const fixture = `#!/bin/sh
if [ "$KURI_TEST_MODE" = success ]; then
    exit 0
fi
printf 'Invalid kubeconfig.\n' >&2
/usr/bin/head -c 70000 /dev/zero >&2
exit 23
`
	launcher := buildTestLauncher(t, home, fixture)
	discard, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer discard.Close()
	run := func(mode string) error {
		cmd := exec.Command(launcher)
		cmd.Env = append(os.Environ(), "HOME="+home, "KURI_TEST_MODE="+mode)
		cmd.Stderr = discard
		return cmd.Run()
	}
	var exit *exec.ExitError
	if err := run("failure"); !errors.As(err, &exit) || exit.ExitCode() != 23 {
		t.Fatalf("launcher exit = %v, want fixture exit 23", err)
	}
	path := filepath.Join(home, "Library", "Logs", "kuri", "startup.log")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 64*1024 || info.Mode().Perm() != 0600 {
		t.Fatalf("log size = %d, mode = %o", info.Size(), info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.HasPrefix(data, []byte("Invalid kubeconfig.\n")) {
		t.Fatalf("failed startup diagnosis is missing: %v", err)
	}
	if err := run("success"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("successful start changed the latest failure log: %v", err)
	}
}

func buildTestLauncher(t *testing.T, dir, fixture string) string {
	t.Helper()
	bundle := filepath.Join(dir, "kuri test.app", "Contents", "MacOS")
	if err := os.MkdirAll(bundle, 0755); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(bundle, "kuri-launcher")
	if out, err := exec.Command("go", "build", "-o", launcher, ".").CombinedOutput(); err != nil {
		t.Fatalf("build launcher: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(bundle, "kuri"), []byte(fixture), 0755); err != nil {
		t.Fatal(err)
	}
	return launcher
}
