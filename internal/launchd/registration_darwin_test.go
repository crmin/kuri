//go:build darwin && cgo

package launchd

import (
	"os"
	"path/filepath"
	"testing"
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
