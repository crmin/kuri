//go:build darwin && cgo

package launchd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStartupRegistrationStateForOldAndNewPlists(t *testing.T) {
	const template = `{
		"Label": "com.github.crmin.kuri",
		"UserName": "kuri-user",
		"ProgramArguments": ["/Library/Application Support/kuri/kuri", "__launchd", "--eager"],
		"RunAtLoad": true,
		"EnvironmentVariables": {"HOME": "/Users/kuri-user"},
		"Sockets": {"HTTP": [
			{"SockFamily": "IPv4", "SockNodeName": "127.0.0.1", "SockServiceName": 8080},
			{"SockFamily": "IPv6", "SockNodeName": "::1", "SockServiceName": 8080}
		]}
	}`
	got, err := decodeRegistration([]byte(template), "kuri-user", "/Users/kuri-user")
	if err != nil || !got.AtBoot || got.Port != 8080 || got.Program != managedProgram {
		t.Fatalf("registration: %+v, %v", got, err)
	}
	if _, err := decodeRegistration([]byte(template), "different-user", "/Users/kuri-user"); err == nil {
		t.Fatal("accepted another account's registration")
	}
}

func TestStopRejectsIncompatibleLoadedRegistration(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("registration executable is never probed as root")
	}
	program := filepath.Join(t.TempDir(), "kuri")
	if err := os.WriteFile(program, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	reg := Registration{Program: program}
	for _, state := range []string{"running", "not running"} {
		loaded := "\tpath = " + registrationPath + "\n\tprogram = " + program + "\n\tstate = " + state + "\n"
		if err := checkProxyPauseSupport(t.Context(), reg, loaded, 0, nil); err == nil || !strings.Contains(err.Error(), "./kuri install") {
			t.Fatalf("%s old service: %v", state, err)
		}
	}
	if err := checkProxyPauseSupport(t.Context(), reg, "", 0, nil); err != nil {
		t.Fatalf("unloaded service: %v", err)
	}
	if err := os.WriteFile(program, []byte("#!/bin/sh\n[ \"$1\" = __proxy_pause_version ] && printf '1\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	loaded := "\tpath = " + registrationPath + "\n\tprogram = " + program + "\n"
	if err := checkProxyPauseSupport(t.Context(), reg, loaded, 0, nil); err != nil {
		t.Fatalf("compatible service: %v", err)
	}
	loaded += "\tpid = 123\n"
	if err := checkProxyPauseSupport(t.Context(), reg, loaded, 0, func(context.Context) bool { return false }); err == nil {
		t.Fatal("accepted old idle activation against the replacement executable")
	}
	if err := checkProxyPauseSupport(t.Context(), reg, loaded, 0, func(context.Context) bool { return true }); err != nil {
		t.Fatalf("compatible idle activation: %v", err)
	}
	if err := checkProxyPauseSupport(t.Context(), reg, loaded, 123, nil); err != nil {
		t.Fatalf("running proxy will be stopped and should not need a probe: %v", err)
	}
}

func TestUpgradePolicyReplacesOnlyManagedExecutable(t *testing.T) {
	system := newStartupTestSystem(t)
	original := system.executable(t, "original")
	replacement := system.executable(t, "replacement")
	if err := os.WriteFile(replacement, []byte("#!/bin/sh\nprintf '1\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	out, code := system.runScript(t, true, "--first-run", original, system.account, filepath.Join(system.root, "home"), system.template())
	if code != 0 {
		t.Fatalf("register fixture: %d %s", code, out)
	}
	args := []string{"--upgrade-policy", replacement, system.account, filepath.Join(system.root, "home"), system.template()}
	if out, code := system.runScript(t, false, args...); code != 77 {
		t.Fatalf("upgrade without approval: %d %s", code, out)
	}
	if out, code := system.runScript(t, true, args...); code != 0 {
		t.Fatalf("upgrade with approval: %d %s", code, out)
	}
	actual, err := os.ReadFile(filepath.Join(system.root, "support", "kuri"))
	want, sourceErr := os.ReadFile(replacement)
	if err != nil || sourceErr != nil || string(actual) != string(want) {
		t.Fatalf("managed program: %q want %q, %v %v", actual, want, err, sourceErr)
	}
	changes, err := os.ReadFile(filepath.Join(system.root, "changes"))
	if err != nil || string(changes) != "enable\nbootstrap\n" {
		t.Fatalf("upgrade changed the loaded registration: %q %v", changes, err)
	}
}

func TestLaunchdPortReconcileAndRollback(t *testing.T) {
	system := newStartupTestSystem(t)
	binary := system.executable(t, "original")
	if out, code := system.run(t, "install", binary, true); code != 0 {
		t.Fatalf("install fixture: %d %s", code, out)
	}
	if err := os.WriteFile(filepath.Join(system.root, "boot"), []byte("boot-fix\n"), 0600); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(system.root, "home")
	command := func(port, operation string, privileged bool) (string, int) {
		return system.runScript(t, privileged, "--configure-port", home, system.account, system.template(), port, operation)
	}
	if out, code := command("8080", "first", false); code != 77 {
		t.Fatalf("port change without approval: %d %s", code, out)
	}
	if out, code := command("8080", "first", true); code != 0 || strings.TrimSpace(out) != "loaded" {
		t.Fatalf("port change: %d %s", code, out)
	}
	for _, key := range []string{"Sockets.HTTP.0.SockServiceName", "Sockets.HTTP.1.SockServiceName"} {
		system.checkPlist(t, key, "8080")
	}
	if err := os.WriteFile(filepath.Join(system.root, "fail-bootstrap"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if out, code := command("9080", "second", true); code == 0 {
		t.Fatalf("failed bootstrap reported success: %d %s", code, out)
	}
	for _, key := range []string{"Sockets.HTTP.0.SockServiceName", "Sockets.HTTP.1.SockServiceName"} {
		system.checkPlist(t, key, "8080")
	}
	if _, err := os.Stat(filepath.Join(system.root, "loaded")); err != nil {
		t.Fatal("previous registration not loaded", err)
	}
	record, err := os.ReadFile(filepath.Join(system.root, "support", ".port-change"))
	if err != nil || !strings.HasSuffix(string(record), "\nsecond\n8080\n") {
		t.Fatalf("rollback did not protect the previous port: %q, %v", record, err)
	}
}

func TestSavedPortIsUsedAtNextBoot(t *testing.T) {
	system := newStartupTestSystem(t)
	executable := system.executable(t, "startup")
	port := "18480"
	if out, code := system.runScript(t, true, "--startup-install", executable, system.account, system.template(), port); code != 0 {
		t.Fatalf("install with saved port: %d %s", code, out)
	}
	system.checkPlist(t, "Sockets.HTTP.0.SockServiceName", port)
	system.checkPlist(t, "Sockets.HTTP.1.SockServiceName", port)
	system.checkPlist(t, "RunAtLoad", "true")
	record := "old-boot\n501\nchangeOnOldBoot\n" + port + "\n"
	if old, err := parsePortChange(record, "old-boot", 501); err != nil || old.Port != 18480 {
		t.Fatalf("old boot transition %+v, %v", old, err)
	}
	if newBoot, err := parsePortChange(record, "new-boot", 501); err != nil || newBoot.ID != "" {
		t.Fatalf("previous boot's interruption suppressed the new boot: %+v, %v", newBoot, err)
	}
}

func TestStartupInstallHandsOffDirectBackgroundServer(t *testing.T) {
	system := newStartupTestSystem(t)
	source := system.executable(t, "staged-app-cli")
	dir := filepath.Join(system.root, "home", ".kuri", ".startup-handoff-test1")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	args := []string{"--startup-install", source, system.account, system.template(), "8080", "--keep-paused", dir}
	if out, code := system.runScript(t, false, args...); code != 77 {
		t.Fatalf("unprivileged handoff: %d %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "ready")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("server could be stopped before approval: %v", err)
	}
	command := system.command(t, true, args...)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if command.ProcessState == nil {
			command.Process.Kill()
			command.Wait()
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "ready")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("authorization did not reach the runtime handoff")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(system.plist); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("registration changed before the direct proxy released its port", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "continue"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	system.checkLink(t, source)
	system.checkPlist(t, "Sockets.HTTP.0.SockServiceName", "8080")
}

func TestResetHandoffAbortKeepsStartupRegistration(t *testing.T) {
	system := newStartupTestSystem(t)
	binary := system.executable(t, "registered")
	if out, code := system.run(t, "install", binary, true); code != 0 {
		t.Fatalf("install fixture: %d %s", code, out)
	}
	changesPath := filepath.Join(system.root, "changes")
	before, err := os.ReadFile(changesPath)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(system.root, "home", ".kuri", ".startup-handoff-reset")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "abort"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	out, code := system.runScript(t, true, "--startup-uninstall", "", system.account, system.template(), "80", "", dir)
	if code == 0 {
		t.Fatalf("uninstall succeeded after settings save aborted: %s", out)
	}
	after, err := os.ReadFile(changesPath)
	if err != nil || string(before) != string(after) {
		t.Fatalf("aborted reset changed the loaded registration: %q -> %q, %v", before, after, err)
	}
	system.checkLink(t, binary)
	system.checkPlist(t, "RunAtLoad", "true")
}

func TestResetUpdatesRetainedActivationPort(t *testing.T) {
	system := newStartupTestSystem(t)
	home := filepath.Join(system.root, "home")
	binary := system.executable(t, "socket-only")
	if out, code := system.runScript(t, true, "--first-run", binary, system.account, home, system.template(), "18880"); code != 0 {
		t.Fatalf("register socket activation fixture: %d %s", code, out)
	}
	if err := os.WriteFile(filepath.Join(system.root, "boot"), []byte("boot-reset\n"), 0600); err != nil {
		t.Fatal(err)
	}
	out, code := system.runScript(t, true, "--configure-port", home, system.account, system.template(), "80", "reset")
	if code != 0 || strings.TrimSpace(out) != "loaded" {
		t.Fatalf("reset socket activation port: %d %s", code, out)
	}
	system.checkPlist(t, "ProgramArguments.1", "__launchd")
	system.checkPlist(t, "RunAtLoad", "false")
	for _, key := range []string{"Sockets.HTTP.0.SockServiceName", "Sockets.HTTP.1.SockServiceName"} {
		system.checkPlist(t, key, "80")
	}
}
