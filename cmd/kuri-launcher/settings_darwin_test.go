//go:build cgo

package main

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/crmin/kuri/internal/settings"
)

//go:embed testdata/settings_payload.m
var settingsPayloadFixture []byte

func settingsFixtureOutput(t *testing.T, args ...string) []byte {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "settings_payload.m")
	if err := os.WriteFile(source, settingsPayloadFixture, 0600); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(dir, "settings_payload")
	command := exec.CommandContext(t.Context(), "xcrun", "clang", "-framework", "AppKit", "-framework", "ServiceManagement", "-framework", "UniformTypeIdentifiers", "-I", ".", source, "settings_darwin.m", "-o", fixture)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("compile settings fixture: %v\n%s", err, output)
	}
	output, err := exec.CommandContext(t.Context(), fixture, args...).Output()
	if err != nil {
		t.Fatalf("run settings fixture: %v", err)
	}
	return output
}

func TestLoginItemSettingsRefresh(t *testing.T) {
	got := strings.TrimSpace(string(settingsFixtureOutput(t, "login-item")))
	want := strings.Join([]string{
		"Open at Login", "Open System Settings",
		"Under Open at Login, click +, select kuri, and click Add.",
		"openLoginItemsSettings:",
	}, "\n")
	if got != want {
		t.Fatalf("login item row: got %q, want %q", got, want)
	}
}

func TestSettingsCloseShortcut(t *testing.T) {
	if got := strings.TrimSpace(string(settingsFixtureOutput(t, "shortcuts"))); got != "ok" {
		t.Fatalf("shortcut fixture: %q", got)
	}
}

func TestSettingsTabsAndAbout(t *testing.T) {
	if got := strings.TrimSpace(string(settingsFixtureOutput(t, "tabs"))); got != "General Network About" {
		t.Fatalf("settings tabs: %q", got)
	}
}

func TestSettingsTransferSelection(t *testing.T) {
	if got := strings.TrimSpace(string(settingsFixtureOutput(t, "transfer"))); got != "ok" {
		t.Fatalf("settings transfer: %q", got)
	}
}

func TestSettingsDomainStyleLabel(t *testing.T) {
	output := strings.TrimSpace(string(settingsFixtureOutput(t, "style-labels")))
	lines := strings.Split(output, "\n")
	want := []string{"portName.service.namespace.ctx.kuri.localhost", "portName.service.namespace.ctx.example.test:8081"}
	if len(lines) != len(want) {
		t.Fatalf("unexpected settings fixture output: %q", output)
	}
	for i, title := range want {
		state := strconv.Itoa(1 - i)
		if expected := strings.Join([]string{title, title, title, state}, "\t"); lines[i] != expected {
			t.Errorf("got %q, want %q", lines[i], expected)
		}
	}
}

func TestSettingsEditPayloads(t *testing.T) {
	output := settingsFixtureOutput(t)
	payloads := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		name, payload, found := strings.Cut(scanner.Text(), "\t")
		if !found || name == "" {
			t.Fatalf("invalid fixture result: %q", scanner.Text())
		}
		if _, exists := payloads[name]; exists {
			t.Fatalf("duplicate fixture case %q", name)
		}
		payloads[name] = payload
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read fixture results: %v", err)
	}
	check := func(name string, want settings.Edit) {
		payload, found := payloads[name]
		delete(payloads, name)
		t.Run(name, func(t *testing.T) {
			if !found {
				t.Fatal("missing fixture result")
			}
			var got settings.Edit
			decoder := json.NewDecoder(strings.NewReader(payload))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&got); err != nil {
				t.Fatalf("decode %s: %v", payload, err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("unexpected payload %s for %+v", payload, want)
			}
		})
	}
	for _, on := range []bool{true, false} {
		suffix := "/" + strconv.FormatBool(on)
		check("auto_start"+suffix, settings.Edit{AutoStart: new(on)})
		check("keep_alive"+suffix, settings.Edit{KeepAlive: new(on)})
		for _, style := range []string{"explicit_context", "explicit_port", "current_context"} {
			check(style+suffix, settings.Edit{Style: &settings.StyleEdit{Name: style, On: on}})
		}
		check("context"+suffix, settings.Edit{Context: &settings.ContextEdit{Name: "dev", On: on}})
	}
	check("save_server", settings.Edit{Domain: new("kuri.localhost"), Port: new("80")})
	forward := settings.Edit{MinPort: new("30000"), MaxPort: new("39999")}
	check("save_forward_without_keep_alive", forward)
	forward.Minutes = new("60")
	check("save_forward_with_keep_alive", forward)
	for name := range payloads {
		t.Errorf("unexpected fixture case %q", name)
	}
}
