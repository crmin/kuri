//go:build darwin && cgo

package launchd

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	launchdassets "github.com/crmin/kuri/deploy/launchd"
	"golang.org/x/sys/unix"
)

func TestStartupRegistrationLifecycle(t *testing.T) {
	system := newStartupTestSystem(t)
	if out, code := system.run(t, "uninstall", "", false); code != 1 || !strings.Contains(out, "not registered") {
		t.Fatalf("unregistered uninstall: exit %d, output %q", code, out)
	}
	first := system.executable(t, "first build")
	if out, code := system.run(t, "install", first, false); code != 77 {
		t.Fatalf("initial install without privileges: exit %d, output %q", code, out)
	}
	if out, code := system.run(t, "install", first, true); code != 0 || !strings.Contains(out, "Installed") {
		t.Fatalf("install: exit %d, output %q", code, out)
	}
	system.checkLink(t, first)
	system.checkPlist(t, "ProgramArguments.0", system.link)
	system.checkPlist(t, "ProgramArguments.1", "start")
	system.checkPlist(t, "RunAtLoad", "true")
	system.checkPlist(t, "UserName", system.account)
	second := system.executable(t, "second build")
	if out, code := system.run(t, "install", second, false); code != 0 || !strings.Contains(out, "Updated") {
		t.Fatalf("link update: exit %d, output %q", code, out)
	}
	system.checkLink(t, second)
	if got := system.changes(t); got != "bootstrap\n" {
		t.Fatalf("unexpected launchctl changes after updating link: %q", got)
	}
	if out, code := system.run(t, "uninstall", "", false); code != 77 {
		t.Fatalf("uninstall without privileges: exit %d, output %q", code, out)
	}
	if out, code := system.run(t, "uninstall", "", true); code != 0 || !strings.Contains(out, "Uninstalled") {
		t.Fatalf("uninstall: exit %d, output %q", code, out)
	}
	for _, path := range []string{system.link, system.plist} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected %s to be removed, got %v", path, err)
		}
	}
	if _, err := os.Stat(second); err != nil {
		t.Fatalf("uninstall affected the source executable: %v", err)
	}
	if got := system.changes(t); got != "bootstrap\nbootout\n" {
		t.Fatalf("unexpected launchctl changes after uninstall: %q", got)
	}
}

func TestMigratesAppStartupRegistration(t *testing.T) {
	system := newStartupTestSystem(t)
	home := filepath.Join(system.root, "home")
	appBinary := filepath.Join(home, ".kuri", "installed", strings.Repeat("a", 64), "kuri")
	if err := os.MkdirAll(filepath.Dir(appBinary), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(appBinary, []byte("previous app"), 0700); err != nil {
		t.Fatal(err)
	}
	if out, code := system.runScript(t, true, "--startup-install", appBinary, system.account, system.template(), "8080"); code != 0 {
		t.Fatalf("install previous app: %d %s", code, out)
	}
	newBinary := system.executable(t, "new app")
	if out, code := system.run(t, "convert-app", newBinary, false); code != 77 {
		t.Fatalf("unauthorized conversion: %d %s", code, out)
	}
	system.flag(t, "fail-bootstrap")
	if out, code := system.run(t, "convert-app", newBinary, true); code == 0 {
		t.Fatalf("failed bootstrap reported success: %s", out)
	}
	system.checkLink(t, appBinary)
	system.checkPlist(t, "RunAtLoad", "true")
	managed := filepath.Join(system.root, "support", "kuri")
	if _, err := os.Stat(managed); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed conversion left the managed binary: %v", err)
	}
	if out, code := system.run(t, "convert-app", newBinary, true); code != 0 {
		t.Fatalf("convert app: %d %s", code, out)
	}
	system.checkPlist(t, "ProgramArguments.0", managed)
	system.checkPlist(t, "ProgramArguments.1", "__launchd")
	system.checkPlist(t, "RunAtLoad", "false")
	system.checkPlist(t, "Sockets.HTTP.0.SockServiceName", "8080")
	system.checkPlist(t, "Sockets.HTTP.1.SockServiceName", "8080")
	if _, err := os.Lstat(system.link); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("conversion left the old app link: %v", err)
	}
}

func TestStandaloneStartupRegistrationPreserved(t *testing.T) {
	system := newStartupTestSystem(t)
	cli := system.executable(t, "installed CLI")
	if out, code := system.run(t, "install", cli, true); code != 0 {
		t.Fatalf("install CLI: %d %s", code, out)
	}
	if out, code := system.run(t, "convert-app", system.executable(t, "app"), true); code == 0 || !strings.Contains(out, "belongs to the kuri CLI") {
		t.Fatalf("convert standalone CLI: %d %s", code, out)
	}
	system.checkLink(t, cli)
	system.checkPlist(t, "RunAtLoad", "true")
	if got := system.changes(t); got != "bootstrap\n" {
		t.Fatalf("conversion affected the CLI job: %q", got)
	}
}

func TestRefreshManagedProxyReplacesLoadedJob(t *testing.T) {
	system := newStartupTestSystem(t)
	home := filepath.Join(system.root, "home")
	original := system.executable(t, "original")
	if err := os.WriteFile(original, []byte("#!/bin/sh\n# original\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if out, code := system.runScript(t, true, "--first-run", original, system.account, home, system.template(), "8080"); code != 0 {
		t.Fatalf("install managed proxy: %d %s", code, out)
	}
	replacement := system.executable(t, "replacement")
	if err := os.WriteFile(replacement, []byte("#!/bin/sh\n# replacement\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if out, code := system.run(t, "refresh-proxy", replacement, false); code != 77 {
		t.Fatalf("refreshed without approval: %d %s", code, out)
	}
	system.flag(t, "fail-bootstrap")
	if out, code := system.run(t, "refresh-proxy", replacement, true); code == 0 {
		t.Fatalf("refresh succeeded despite bootstrap failure: %s", out)
	}
	program := filepath.Join(system.root, "support", "kuri")
	contents, err := os.ReadFile(program)
	if err != nil || !strings.Contains(string(contents), "original") {
		t.Fatalf("previous executable was not restored: %s %v", contents, err)
	}
	if out, code := system.run(t, "refresh-proxy", replacement, true); code != 0 {
		t.Fatalf("refresh managed proxy: %d %s", code, out)
	}
	contents, err = os.ReadFile(program)
	if err != nil || !strings.Contains(string(contents), "replacement") {
		t.Fatalf("replacement executable: %s %v", contents, err)
	}
	system.checkPlist(t, "RunAtLoad", "false")
	system.checkPlist(t, "Sockets.HTTP.0.SockServiceName", "8080")
	if changes := system.changes(t); changes != "enable\nbootstrap\nbootout\nbootstrap\nbootstrap\nbootout\nbootstrap\n" {
		t.Fatalf("managed job was not replaced and restored as expected: %s", changes)
	}
}

func TestStartupRequestsApprovalOnlyWhenNeeded(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("administrator invocation does not request approval")
	}
	for _, tc := range []struct {
		name, firstExit string
		wantsApproval   bool
		wantsError      bool
	}{
		{"already writable", "0", false, false},
		{"permission required", "77", true, false},
		{"unrelated error", "1", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			var attempts []bool
			err := runStartupWithApproval(t.Context(), "install", &stdout, &stderr, func(admin bool) ([]byte, error) {
				attempts = append(attempts, admin)
				if admin {
					return []byte("Installed\n"), nil
				}
				return exec.CommandContext(t.Context(), "/bin/sh", "-c", `printf 'Installer result\n'; exit "$1"`, "installer", tc.firstExit).CombinedOutput()
			})
			if (err != nil) != tc.wantsError || strings.Contains(stderr.String(), "administrator approval") != tc.wantsApproval ||
				(len(attempts) == 2 && attempts[1]) != tc.wantsApproval || attempts[0] {
				t.Fatalf("attempts %v, stdout %q, stderr %q, error %v", attempts, &stdout, &stderr, err)
			}
		})
	}
}

func TestStartupMigratesLegacyAndRestoresItOnFailure(t *testing.T) {
	system := newStartupTestSystem(t)
	source := system.executable(t, "working directory")
	home := filepath.Join(system.root, "home")
	if out, code := system.runScript(t, true, "--first-run", source, system.account, home, system.template()); code != 0 {
		t.Fatalf("legacy install: exit %d, output %q", code, out)
	}
	system.checkPlist(t, "ProgramArguments.1", "__launchd")
	if out, code := system.run(t, "uninstall", "", false); code != 1 || !strings.Contains(out, "not registered") {
		t.Fatalf("legacy registration should not count as startup: exit %d, output %q", code, out)
	}
	system.flag(t, "fail-bootstrap")
	if out, code := system.run(t, "install", source, true); code == 0 {
		t.Fatalf("failed migration succeeded: %q", out)
	}
	system.checkPlist(t, "ProgramArguments.1", "__launchd")
	if _, err := os.Lstat(system.link); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed migration left its link: %v", err)
	}
	if got := system.changes(t); !strings.HasSuffix(got, "bootout\nbootstrap\nbootstrap\n") {
		t.Fatalf("the previous job was not restored: %q", got)
	}
	if out, code := system.run(t, "install", source, true); code != 0 {
		t.Fatalf("retry migration: exit %d, output %q", code, out)
	}
	system.checkLink(t, source)
	system.checkPlist(t, "ProgramArguments.1", "start")
	if out, code := system.runScript(t, true, source, system.account); code != 1 || !strings.Contains(out, "./kuri install") {
		t.Fatalf("legacy script should preserve the new registration: exit %d, output %q", code, out)
	}
	system.checkPlist(t, "ProgramArguments.1", "start")
}

func TestStartupPreservesAnUnmanagedPath(t *testing.T) {
	system := newStartupTestSystem(t)
	source := system.executable(t, "build")
	if err := os.WriteFile(system.link, []byte("unmanaged"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, code := system.run(t, "install", source, true); code != 1 || !strings.Contains(out, "already exists") {
		t.Fatalf("unmanaged binary: exit %d, output %q", code, out)
	}
	data, err := os.ReadFile(system.link)
	if err != nil || string(data) != "unmanaged" {
		t.Fatalf("unmanaged binary changed: %q, %v", data, err)
	}
	if out, code := system.run(t, "install", system.link, false); code != 1 || !strings.Contains(out, "original binary") {
		t.Fatalf("link as the source: exit %d, output %q", code, out)
	}
}

func TestStartupUpdateDoesNotLoadAnUnloadedRegistration(t *testing.T) {
	system := newStartupTestSystem(t)
	first := system.executable(t, "first")
	if out, code := system.run(t, "install", first, true); code != 0 {
		t.Fatalf("initial install: exit %d, output %q", code, out)
	}
	if err := os.Remove(filepath.Join(system.root, "loaded")); err != nil {
		t.Fatal(err)
	}
	system.flag(t, "disabled")
	second := system.executable(t, "second")
	if out, code := system.run(t, "install", second, false); code != 0 {
		t.Fatalf("update: exit %d, output %q", code, out)
	}
	system.checkLink(t, second)
	if out, code := system.run(t, "uninstall", "", true); code != 0 {
		t.Fatalf("uninstall unloaded registration: exit %d, output %q", code, out)
	}
	if got := system.changes(t); got != "bootstrap\n" {
		t.Fatalf("unexpected launchctl changes on the unloaded registration: %q", got)
	}
}

func TestStartupRejectsAConflictingRegistration(t *testing.T) {
	for _, tc := range []struct{ key, value, reason string }{
		{"Program", "/bin/echo", "not a recognized kuri registration"},
		{"UserName", "someone-else", "belongs to another macOS account"},
		{"EnvironmentVariables.KURI_LAUNCHD_START", "0", "not a recognized kuri registration"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			system := newStartupTestSystem(t)
			first := system.executable(t, "first")
			if out, code := system.run(t, "install", first, true); code != 0 {
				t.Fatalf("initial install: exit %d, output %q", code, out)
			}
			cmd := exec.CommandContext(t.Context(), "/usr/bin/plutil", "-replace", tc.key, "-string", tc.value, system.plist)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("change fixture: %s (%v)", out, err)
			}
			second := system.executable(t, "second")
			if out, code := system.run(t, "install", second, true); code != 1 || !strings.Contains(out, tc.reason) {
				t.Fatalf("conflicting registration: exit %d, output %q", code, out)
			}
			system.checkLink(t, first)
			if got := system.changes(t); got != "bootstrap\n" {
				t.Fatalf("conflict caused a launchctl change: %q", got)
			}
		})
	}
}

func TestStartupUninstallRestoresRegistrationIfLinkRemovalFails(t *testing.T) {
	system := newStartupTestSystem(t)
	source := system.executable(t, "build")
	if out, code := system.run(t, "install", source, true); code != 0 {
		t.Fatalf("initial install: exit %d, output %q", code, out)
	}
	system.flag(t, "fail-remove-link")
	if out, code := system.run(t, "uninstall", "", true); code == 0 {
		t.Fatalf("failed uninstall succeeded: %q", out)
	}
	system.checkLink(t, source)
	system.checkPlist(t, "ProgramArguments.1", "start")
	if got := system.changes(t); got != "bootstrap\nbootout\nbootstrap\n" {
		t.Fatalf("the registration was not restored: %q", got)
	}
}

func TestStartupLinkUpdateWaitsForTheRegistrationLock(t *testing.T) {
	system := newStartupTestSystem(t)
	first := system.executable(t, "first")
	if out, code := system.run(t, "install", first, true); code != 0 {
		t.Fatalf("initial install: exit %d, output %q", code, out)
	}
	lock, err := os.Open(filepath.Join(system.root, "support", ".install.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	second := system.executable(t, "second")
	flag := filepath.Join(system.root, "lock-attempt")
	if err := os.Remove(flag); err != nil {
		t.Fatal(err)
	}
	cmd := system.command(t, false, "--startup-install", second, system.account, system.template())
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(flag); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("link update did not try to acquire the registration lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	system.checkLink(t, first)
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("link update: %s (%v)", out.String(), err)
	}
	system.checkLink(t, second)
}

func TestQuitPauseUsesBootSession(t *testing.T) {
	s := newStartupTestSystem(t)
	path := s.executable(t, "app")
	if err := os.WriteFile(filepath.Join(s.root, "boot"), []byte("first-boot\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, code := s.run(t, "install", path, true); code != 0 {
		t.Fatalf("install: %d %s", code, out)
	}
	menu := func(action, operation, expected string, elevated bool, want int) {
		t.Helper()
		out, code := s.runScript(t, elevated, "--menu-"+action, filepath.Join(s.root, "home"), s.account, s.template(), operation, expected)
		if code != want {
			t.Fatalf("%s: %d %s", action, code, out)
		}
	}
	menu("pause", "Pause1", "", false, 77)
	menu("pause", "Pause1", "", true, 0)
	if _, err := os.Stat(filepath.Join(s.root, "loaded")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("job remained loaded: %v", err)
	}
	if out, code := s.run(t, "install", path, true); code == 0 || !strings.Contains(out, "kuri start") {
		t.Fatalf("paused install: %d %s", code, out)
	}
	s.flag(t, "disabled")
	menu("resume", "Resume1", "Pause1", true, 1)
	if err := os.Remove(filepath.Join(s.root, "disabled")); err != nil {
		t.Fatal(err)
	}
	menu("resume", "Resume1", "Pause1", true, 0)
	s.flag(t, "fail-menu-finish")
	menu("finish-resume", "Resume1", "", true, 5)
	menu("rollback", "AnotherResume", "", true, 0)
	if _, err := os.Stat(filepath.Join(s.root, "loaded")); err != nil {
		t.Fatal("another transaction must not change the loaded job")
	}
	menu("rollback", "Resume1", "", true, 0)
	menu("resume", "DelayedStart", "Pause1", true, 1)
	menu("resume", "Resume2", "Resume1", true, 0)
	menu("finish-resume", "Resume2", "", true, 0)
	if out, code := s.run(t, "install", path, true); code != 0 {
		t.Fatalf("completed resume blocks install: %d %s", code, out)
	}
	// Even when the finish reply is lost, the original operation can roll back.
	menu("rollback", "Resume2", "", true, 0)
	menu("resume", "Resume3", "Resume2", true, 0)
	menu("finish-resume", "Resume3", "", true, 0)
	menu("pause", "Pause2", "", true, 0)
	if err := os.WriteFile(filepath.Join(s.root, "boot"), []byte("second-boot\n"), 0600); err != nil {
		t.Fatal(err)
	}
	menu("resume", "Resume4", "Pause2", true, 1)
	if out, code := s.run(t, "install", path, true); code != 0 {
		t.Fatalf("stale pause blocks install: %d %s", code, out)
	}
	if got := s.changes(t); got != "bootstrap\nbootout\nbootstrap\nbootout\nbootstrap\nbootout\nbootstrap\nbootout\n" {
		t.Fatalf("unexpected service changes: %q", got)
	}
}

func TestMenuPausePreservesProcessObservationOnRetry(t *testing.T) {
	s := newStartupTestSystem(t)
	for name, text := range map[string]string{"boot": "boot-id\n", "process-birth": "First process\n"} {
		if err := os.WriteFile(filepath.Join(s.root, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if out, code := s.run(t, "install", s.executable(t, "app"), true); code != 0 {
		t.Fatalf("install: %d %s", code, out)
	}
	if out, code := s.runScript(t, true, "--menu-pause", s.root, s.account, s.template(), "BadHome", ""); code != 1 || !strings.Contains(out, "different home") {
		t.Fatalf("mismatched home: %d %s", code, out)
	}
	loaded := filepath.Join(s.root, "loaded")
	data, err := os.ReadFile(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(loaded, append(data, []byte("\tpid = 909\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	s.flag(t, "fail-process-observation")
	pause := func(operation string) (string, int) {
		return s.runScript(t, true, "--menu-pause", filepath.Join(s.root, "home"), s.account, s.template(), operation, "")
	}
	for _, operation := range []string{"FirstQuit", "RetryQuit"} {
		if out, code := pause(operation); code == 0 || !strings.Contains(out, "Could not observe") {
			t.Fatalf("%s lost the process: %d %s", operation, code, out)
		}
		data, err := os.ReadFile(filepath.Join(s.root, "support", ".menu-pause"))
		if err != nil || !strings.Contains(string(data), "\npausing\n"+operation+"\n909\nFirst process\n") {
			t.Fatalf("%s state: %s %v", operation, data, err)
		}
	}
	if err := os.Remove(filepath.Join(s.root, "fail-process-observation")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.root, "process-birth"), []byte("A different process\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, code := pause("FinalQuit"); code != 0 {
		t.Fatalf("confirm exit after PID reuse: %d %s", code, out)
	}
	if got := s.changes(t); got != "bootstrap\nbootout\n" {
		t.Fatalf("unexpected mutations: %q", got)
	}
}

func TestCurrentMenuPauseRecord(t *testing.T) {
	for _, tc := range []struct {
		boot            string
		uid             int
		paused, invalid bool
	}{
		{"boot-a", 501, true, false}, {"boot-b", 501, false, false}, {"boot-a", 502, false, true},
	} {
		paused, err := currentMenuPause("boot-a\n501\ndigest\npaused\n", tc.boot, tc.uid)
		if paused != tc.paused || (err != nil) != tc.invalid {
			t.Fatalf("unexpected record result: %v %v", paused, err)
		}
	}
}

type startupTestSystem struct {
	root, account, link, plist, script string
}

func newStartupTestSystem(t *testing.T) startupTestSystem {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("the fixture needs a non-root account to simulate the requesting user")
	}
	account, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	system := startupTestSystem{root: root, account: account.Username, link: filepath.Join(root, "bin", "kuri"),
		plist: filepath.Join(root, "LaunchDaemons", "com.github.crmin.kuri.plist")}
	for _, dir := range []string{"bin", "LaunchDaemons", "home", "tmp", "support"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range map[string]string{
		"id": `if [ "$#" -eq 1 ] && [ "$1" = -u ]; then
    printf '%s\n' "$TEST_EUID"
else
    exec /usr/bin/id "$@"
fi`,
		"dscl":   `printf 'NFSHomeDirectory: %s/home\n' "$TEST_ROOT"`,
		"sysctl": `exec /bin/cat "$TEST_ROOT/boot"`,
		"ps": `if [ ! -e "$TEST_ROOT/loaded" ] && [ -e "$TEST_ROOT/fail-process-observation" ]; then
    printf 'observation failed\n' >&2
    exit 2
fi
if [ ! -e "$TEST_ROOT/process-birth" ]; then exit 1; fi
exec /bin/cat "$TEST_ROOT/process-birth"`,
		"sudo": `shift 4
exec "$@"`,
		"install": `shift 4
exec /usr/bin/install "$@"`,
		"lockf": `: > "$TEST_ROOT/lock-attempt"
exec /usr/bin/lockf "$@"`,
		"mv": `if [ "$#" -eq 3 ] && [ "$3" = "$TEST_ROOT/support/.menu-pause" ] && [ -e "$TEST_ROOT/fail-menu-finish" ]; then
    if /usr/bin/grep -qx completed "$2"; then
        /bin/rm "$TEST_ROOT/fail-menu-finish"
        exit 5
    fi
fi
exec /bin/mv "$@"`,
		"rm": `for arg in "$@"; do
    if [ "$arg" = "$TEST_ROOT/bin/kuri" ] && [ -e "$TEST_ROOT/fail-remove-link" ]; then
        /bin/rm "$TEST_ROOT/fail-remove-link"
        exit 5
    fi
done
exec /bin/rm "$@"`,
		"launchctl": `case "$1" in
    print)
        if [ ! -f "$TEST_ROOT/loaded" ]; then exit 113; fi
        /bin/cat "$TEST_ROOT/loaded"
        ;;
    print-disabled)
        printf 'disabled services = {\n'
        if [ -e "$TEST_ROOT/disabled" ]; then printf '\t"com.github.crmin.kuri" => disabled\n'; fi
        printf '}\n'
        ;;
    bootstrap)
        printf 'bootstrap\n' >> "$TEST_ROOT/changes"
        if [ -e "$TEST_ROOT/fail-bootstrap" ]; then
            /bin/rm "$TEST_ROOT/fail-bootstrap"
            exit 5
        fi
        plist=$3
        value() { /usr/bin/plutil -extract "$1" raw -o - "$plist"; }
        {
            printf '\tpath = %s\n\tprogram = %s\n' "$plist" "$(value ProgramArguments.0)"
            printf '\targuments = {\n\t\t%s\n\t\t%s\n' "$(value ProgramArguments.0)" "$(value ProgramArguments.1)"
            if [ "$(value ProgramArguments)" -eq 3 ]; then printf '\t\t%s\n' "$(value ProgramArguments.2)"; fi
            printf '\t}\n\tenvironment = {\n\t\tHOME => %s\n' "$(value EnvironmentVariables.HOME)"
            if marker=$(value EnvironmentVariables.KURI_LAUNCHD_START 2>/dev/null); then printf '\t\tKURI_LAUNCHD_START => %s\n' "$marker"; fi
            printf '\t}\n\tusername = %s\n' "$(value UserName)"
        } > "$TEST_ROOT/loaded"
        ;;
    bootout)
        printf 'bootout\n' >> "$TEST_ROOT/changes"
        /bin/rm "$TEST_ROOT/loaded"
        ;;
    *) printf '%s\n' "$1" >> "$TEST_ROOT/changes" ;;
esac`,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("#!/bin/sh\nset -eu\n"+body+"\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	uid := strconv.Itoa(os.Geteuid())
	system.script = strings.NewReplacer(
		"/Library/Application Support/kuri", filepath.Join(root, "support"),
		"/Library/LaunchDaemons", filepath.Dir(system.plist),
		"/usr/local/bin/kuri", system.link,
		"/private/var/tmp/kuri-", filepath.Join(root, "tmp", "kuri-"),
		"/usr/bin/id", filepath.Join(root, "id"),
		"/usr/bin/dscl", filepath.Join(root, "dscl"),
		"/usr/sbin/sysctl", filepath.Join(root, "sysctl"),
		"/bin/ps", filepath.Join(root, "ps"),
		"/usr/bin/sudo", filepath.Join(root, "sudo"),
		"/usr/bin/install", filepath.Join(root, "install"),
		"/usr/bin/lockf", filepath.Join(root, "lockf"),
		"/bin/launchctl", filepath.Join(root, "launchctl"),
		"/bin/mv", filepath.Join(root, "mv"),
		"/bin/rm", filepath.Join(root, "rm"),
		`'%u' "$1")" -eq 0`, `'%u' "$1")" -eq `+uid,
		`trusted_path "$binary"`, `true`,
		`trusted_path "$bin_directory"`, `true`,
	).Replace(launchdassets.InstallScript)
	return system
}

func (s startupTestSystem) run(t *testing.T, action, source string, elevated bool) (string, int) {
	t.Helper()
	return s.runScript(t, elevated, "--startup-"+action, source, s.account, s.template())
}

func (s startupTestSystem) template() string {
	return strings.ReplaceAll(launchdassets.Plist, "/Library/Application Support/kuri", filepath.Join(s.root, "support"))
}

func (s startupTestSystem) command(t *testing.T, elevated bool, args ...string) *exec.Cmd {
	t.Helper()
	euid := strconv.Itoa(os.Geteuid())
	if elevated {
		euid = "0"
	}
	cmd := exec.CommandContext(t.Context(), "/bin/sh", append([]string{"-c", s.script, "kuri-install"}, args...)...)
	cmd.Env = append(os.Environ(), "TEST_ROOT="+s.root, "TEST_EUID="+euid)
	return cmd
}

func (s startupTestSystem) runScript(t *testing.T, elevated bool, args ...string) (string, int) {
	t.Helper()
	cmd := s.command(t, elevated, args...)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return string(out), exit.ExitCode()
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(out), 0
}

func (s startupTestSystem) flag(t *testing.T, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(s.root, name), nil, 0600); err != nil {
		t.Fatal(err)
	}
}

func (s startupTestSystem) executable(t *testing.T, directory string) string {
	t.Helper()
	path := filepath.Join(s.root, directory, "kuri")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func (s startupTestSystem) checkLink(t *testing.T, want string) {
	t.Helper()
	if got, err := os.Readlink(s.link); err != nil || got != want {
		t.Fatalf("link = %q, error %v, want %q", got, err, want)
	}
}

func (s startupTestSystem) checkPlist(t *testing.T, key, want string) {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "/usr/bin/plutil", "-extract", key, "raw", "-o", "-", s.plist).CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != want {
		t.Fatalf("plist %s = %q, error %v, want %q", key, out, err, want)
	}
}

func (s startupTestSystem) changes(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(s.root, "changes"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
