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
	command := exec.CommandContext(t.Context(), "xcrun", "clang", "-framework", "AppKit", "-framework", "ServiceManagement", "-framework", "UniformTypeIdentifiers", "-I", ".", source, "settings_darwin.m", "statusitem_darwin.m", "popover_darwin.m", "bookmarks_darwin.m", "-o", fixture)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("compile settings fixture: %v\n%s", err, output)
	}
	output, err := exec.CommandContext(t.Context(), fixture, args...).Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			t.Fatalf("run settings fixture: %v\n%s", err, exit.Stderr)
		}
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

func TestMenuKeyboardShortcuts(t *testing.T) {
	if got := strings.TrimSpace(string(settingsFixtureOutput(t, "menus"))); got != "ok" {
		t.Fatalf("menu shortcuts: %q", got)
	}
}

func TestSettingsTabsAndAbout(t *testing.T) {
	if got := strings.TrimSpace(string(settingsFixtureOutput(t, "tabs"))); got != "General Bookmark Network About" {
		t.Fatalf("settings tabs: %q", got)
	}
}

func TestBookmarkSettingsTable(t *testing.T) {
	if got := strings.TrimSpace(string(settingsFixtureOutput(t, "bookmark-table"))); got != "ok" {
		t.Fatalf("bookmark table: %q", got)
	}
}

func TestAddBookmarkDialog(t *testing.T) {
	if got := strings.TrimSpace(string(settingsFixtureOutput(t, "bookmark-editor"))); got != "ok" {
		t.Fatalf("bookmark editor: %q", got)
	}
}

func TestBookmarkedServicesPopover(t *testing.T) {
	if got := strings.TrimSpace(string(settingsFixtureOutput(t, "bookmark-menu"))); got != "ok" {
		t.Fatalf("bookmark menu: %q", got)
	}
}

func TestSettingsTransferSelection(t *testing.T) {
	if got := strings.TrimSpace(string(settingsFixtureOutput(t, "transfer"))); got != "ok" {
		t.Fatalf("settings transfer: %q", got)
	}
}

func TestImportRefreshPreservesSelection(t *testing.T) {
	output := strings.TrimSpace(string(settingsFixtureOutput(t, "transfer-refresh")))
	name, payload, _ := strings.Cut(output, "\t")
	var request struct {
		Operation string   `json:"transfer_operation"`
		Fields    []string `json:"fields"`
	}
	if err := json.Unmarshal([]byte(payload), &request); name != "refresh" || err != nil ||
		request.Operation != "preview_import" || len(request.Fields) != 1 || request.Fields[0] != "context_aliases" {
		t.Fatalf("invalid refresh request: %s", output)
	}
}

func TestImportPreservesUnselectedDraft(t *testing.T) {
	if got := strings.TrimSpace(string(settingsFixtureOutput(t, "transfer-draft"))); got != "ok" {
		t.Fatalf("settings transfer draft: %q", got)
	}
}

func TestSettingsAliasDialog(t *testing.T) {
	output := strings.TrimSpace(string(settingsFixtureOutput(t, "alias-dialog")))
	key, value, ok := strings.Cut(output, "\t")
	var edit settings.Edit
	if !ok || key != "save_alias" || json.Unmarshal([]byte(value), &edit) != nil || edit.ContextAliases == nil ||
		edit.ContextAliases.Aliases["aws-dev"] != "dev" || edit.ContextAliases.ConfigPath != "/tmp/kubeconfig" ||
		edit.ContextAliases.Fingerprint != "fingerprint" {
		t.Fatalf("alias save: %q", output)
	}
}

func TestSettingsAutoHealingControls(t *testing.T) {
	if got := strings.TrimSpace(string(settingsFixtureOutput(t, "auto-healing"))); got != "ok" {
		t.Fatalf("auto healing controls: %q", got)
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
