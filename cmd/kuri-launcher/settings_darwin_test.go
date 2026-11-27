//go:build cgo

package main

import (
	"bufio"
	"bytes"
	_ "embed"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/crmin/kuri/internal/settings"
)

//go:embed testdata/settings_payload.m
var settingsPayloadFixture []byte

func TestSettingsEditPayloads(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "settings_payload.m")
	if err := os.WriteFile(source, settingsPayloadFixture, 0600); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(dir, "settings_payload")
	command := exec.CommandContext(t.Context(), "xcrun", "clang", "-framework", "AppKit", "-I", ".", source, "settings_darwin.m", "-o", fixture)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("compile settings fixture: %v\n%s", err, output)
	}
	output, err := exec.CommandContext(t.Context(), fixture).Output()
	if err != nil {
		t.Fatalf("run settings fixture: %v", err)
	}
	payloads := bufio.NewScanner(bytes.NewReader(output))
	check := func(name string, want settings.Edit) {
		t.Helper()
		if !payloads.Scan() {
			t.Fatalf("missing %s payload: %v", name, payloads.Err())
		}
		t.Run(name, func(t *testing.T) {
			var got settings.Edit
			decoder := json.NewDecoder(bytes.NewReader(payloads.Bytes()))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&got); err != nil {
				t.Fatalf("decode %s: %v", payloads.Text(), err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("unexpected payload %s for %+v", payloads.Text(), want)
			}
		})
	}
	for _, on := range []bool{true, false} {
		suffix := "/" + strconv.FormatBool(on)
		check("register"+suffix, settings.Edit{Register: new(on)})
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
	if payloads.Scan() || payloads.Err() != nil {
		t.Errorf("unexpected fixture output: %q (%v)", payloads.Text(), payloads.Err())
	}
}
