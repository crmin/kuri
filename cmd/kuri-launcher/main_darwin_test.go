package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLauncherRunsBundledStart(t *testing.T) {
	dir := t.TempDir()
	bundle := filepath.Join(dir, "kuri test.app", "Contents", "MacOS")
	if err := os.MkdirAll(bundle, 0755); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(bundle, "kuri-launcher")
	if out, err := exec.Command("go", "build", "-o", launcher, ".").CombinedOutput(); err != nil {
		t.Fatalf("build launcher: %v\n%s", err, out)
	}
	const fixture = `#!/bin/sh
printf '%s\n' "$#" "$1" "${KURI_LAUNCHD_START-unset}" "$PATH" >> "$KURI_TEST_RESULT"
exit 23
`
	if err := os.WriteFile(filepath.Join(bundle, "kuri"), []byte(fixture), 0755); err != nil {
		t.Fatal(err)
	}
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
