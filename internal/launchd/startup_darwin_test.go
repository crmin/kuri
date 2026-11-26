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
	menu := func(action string, elevated bool, want int) {
		t.Helper()
		out, code := s.runScript(t, elevated, "--menu-"+action, "", s.account, s.template())
		if code != want {
			t.Fatalf("%s: %d %s", action, code, out)
		}
	}
	menu("pause", false, 77)
	menu("pause", true, 0)
	if _, err := os.Stat(filepath.Join(s.root, "loaded")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("job remained loaded: %v", err)
	}
	if out, code := s.run(t, "install", path, true); code == 0 || !strings.Contains(out, "kuri start") {
		t.Fatalf("paused install: %d %s", code, out)
	}
	s.flag(t, "disabled")
	menu("resume", true, 1)
	if err := os.Remove(filepath.Join(s.root, "disabled")); err != nil {
		t.Fatal(err)
	}
	menu("resume", true, 0)
	menu("finish-resume", true, 0)
	menu("pause", true, 0)
	if err := os.WriteFile(filepath.Join(s.root, "boot"), []byte("second-boot\n"), 0600); err != nil {
		t.Fatal(err)
	}
	menu("resume", true, 1)
	if out, code := s.run(t, "install", path, true); code != 0 {
		t.Fatalf("stale pause blocks install: %d %s", code, out)
	}
	if got := s.changes(t); got != "bootstrap\nbootout\nbootstrap\nbootout\n" {
		t.Fatalf("unexpected service changes: %q", got)
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
		"sudo": `shift 4
exec "$@"`,
		"install": `shift 4
exec /usr/bin/install "$@"`,
		"lockf": `: > "$TEST_ROOT/lock-attempt"
exec /usr/bin/lockf "$@"`,
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
		"/usr/bin/sudo", filepath.Join(root, "sudo"),
		"/usr/bin/install", filepath.Join(root, "install"),
		"/usr/bin/lockf", filepath.Join(root, "lockf"),
		"/bin/launchctl", filepath.Join(root, "launchctl"),
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
