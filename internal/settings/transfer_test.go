package settings

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestSettingsTransferPresenceAndRoundTrip(t *testing.T) {
	const source = `server:
  domain_styles:
    explicit_context: false
    future_style: value
  newer_option: ignored
port_forward:
  keep_alive_enabled: false
  keep_alive_minutes: 3.5
  auto_healing_enabled: false
  connection_check_interval_seconds: 18
  min_port: 30400
new_section:
  error: ignored
`
	transfer, err := DecodeTransfer(strings.NewReader(source), nil)
	if err != nil {
		t.Fatal(err)
	}
	if actual := transfer.Fields(); !slices.Equal(actual, []TransferField{TransferDomainStyles, TransferKeepAlive,
		TransferMinutes, TransferAutoHealing, TransferCheckSeconds, TransferPortRange}) {
		t.Fatalf("available fields: %v", actual)
	}
	cfg := Defaults(t.TempDir())
	selected := transfer.Select(transfer.Fields())
	if err := (Edit{Transfer: &selected}).Apply(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Styles.ExplicitContext || !cfg.Server.Styles.CurrentContext || !cfg.Server.Styles.ExplicitPort ||
		cfg.PortForward.KeepAlive || cfg.PortForward.AutoHealingEnabled() || cfg.PortForward.Minutes != 3.5 ||
		cfg.PortForward.CheckSeconds == nil || *cfg.PortForward.CheckSeconds != 18 ||
		cfg.PortForward.MinPort != 30400 || cfg.PortForward.MaxPort != 39999 {
		t.Fatalf("incorrectly merged partial settings: %+v", cfg)
	}
	store := NewStore(t.TempDir())
	if _, err := store.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "settings.yaml")
	if err := store.ExportTransfer(dst, []TransferField{TransferAutoHealing, TransferCheckSeconds}); err != nil {
		t.Fatal(err)
	}
	exported, err := ReadTransfer(dst, nil)
	if err != nil || !slices.Equal(exported.Fields(), []TransferField{TransferAutoHealing, TransferCheckSeconds}) ||
		exported.Forward.AutoHealing == nil || !*exported.Forward.AutoHealing || *exported.Forward.CheckSeconds != 10 {
		t.Fatalf("selected defaults missing from export: %+v %v", exported, err)
	}
}

func TestTransferRejectsMalformedFiles(t *testing.T) {
	for _, input := range []string{
		"- one", "unknown: {}", "port_forward: []", "server: {port: [5]}", "server: {domain: 123}",
		"server: {domain: abc, domain: xyz}", "server: {}\n---\nserver: {}", "port_forward: {keep_alive_minutes: .nan}",
		"kubernetes: {context_aliases: [dev]}", "kubernetes: {context_aliases: {dev: [fail]}}",
	} {
		if _, err := DecodeTransfer(strings.NewReader(input), []string{"dev"}); err != errTransferFormat {
			t.Errorf("accepted %q or leaked parser error: %v", input, err)
		}
	}
	got, err := DecodeTransfer(strings.NewReader("server: {future_field: 1}"), nil)
	if err != nil || len(got.Fields()) != 0 {
		t.Fatalf("future version's mapping should have no selectable fields: %+v %v", got, err)
	}
}

func TestExportContextAliasesUsesActiveKubeconfig(t *testing.T) {
	store := NewStore(t.TempDir())
	_, _, err := store.Update(t.Context(), func(c *Config) error {
		c.Kubernetes.MergeAliases(map[string]string{"aws-dev": "dev", "aws-prod": "prod"})
		path := c.Kubernetes.ConfigPath
		c.Kubernetes.ConfigPath += "-other"
		c.Kubernetes.MergeAliases(map[string]string{"secret": "other"})
		c.Kubernetes.ConfigPath = path
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "aliases.yaml")
	if err := store.ExportTransfer(path, []TransferField{TransferAliases}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(raw), "aws-dev: dev") || !strings.Contains(string(raw), "aws-prod: prod") ||
		strings.Contains(string(raw), "secret") || strings.Contains(string(raw), ".kube") {
		t.Fatalf("unexpected transfer file: %s, %v", raw, err)
	}
	imported, err := ReadTransfer(path, []string{"aws-dev", "secret"})
	if err != nil || len(imported.Kubernetes.Aliases) != 1 || imported.Kubernetes.Aliases["aws-dev"] != "dev" {
		t.Fatalf("alias matches: %+v, %v", imported.Kubernetes, err)
	}
}

func TestImportIntegerFieldsRejectFractionalYAML(t *testing.T) {
	for _, input := range []string{
		"server: {port: 8080.9}",
		"port_forward: {min_port: 30000.9}",
		"port_forward: {max_port: 39999.9}",
		"port_forward: {connection_check_interval_seconds: 1.9}",
	} {
		if _, err := DecodeTransfer(strings.NewReader(input), nil); err != errTransferFormat {
			t.Errorf("accepted %q: %v", input, err)
		}
	}
	input := `server: {port: null}
port_forward: {keep_alive_minutes: 1.75, future_port: 1.9}`
	transfer, err := DecodeTransfer(strings.NewReader(input), nil)
	if err != nil || !slices.Equal(transfer.Fields(), []TransferField{TransferMinutes}) || *transfer.Forward.Minutes != 1.75 {
		t.Fatalf("valid transfer: %+v, %v", transfer, err)
	}
}

func TestImportLocalSettingsUsesSourceAliasProfile(t *testing.T) {
	store := NewStore(t.TempDir())
	_, _, err := store.Update(t.Context(), func(c *Config) error {
		c.Kubernetes.ConfigPath = "/source/other"
		c.Kubernetes.MergeAliases(map[string]string{"dev": "other", "test": "other-test"})
		c.Kubernetes.ConfigPath = "/source/current"
		c.Kubernetes.MergeAliases(map[string]string{"dev": "source-dev", "absent": "source-absent"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	transfer, err := ReadTransfer(store.Path(), []string{"dev", "test"})
	if err != nil || transfer.Kubernetes == nil || len(transfer.Kubernetes.Aliases) != 1 || transfer.Kubernetes.Aliases["dev"] != "source-dev" {
		t.Fatalf("source profile: %+v, %v", transfer.Kubernetes, err)
	}
	_, _, err = store.Update(t.Context(), func(c *Config) error {
		c.Kubernetes.ConfigPath = "/source/default"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	transfer, err = ReadTransfer(store.Path(), []string{"dev", "test"})
	if err != nil || transfer.Kubernetes != nil {
		t.Fatalf("aliases from an inactive profile: %+v, %v", transfer.Kubernetes, err)
	}
	portable, err := DecodeTransfer(strings.NewReader("kubernetes: {config_path: /unused, context_aliases: {dev: portable}}"), []string{"dev"})
	if err != nil || portable.Kubernetes == nil || portable.Kubernetes.Aliases["dev"] != "portable" {
		t.Fatalf("portable aliases with an extra field: %+v, %v", portable.Kubernetes, err)
	}
	normalized, err := DecodeTransfer(strings.NewReader("kubernetes: {config_path: /source/./current, context_aliases: {/source/current: {dev: source-dev}}}"), []string{"dev"})
	if err != nil || normalized.Kubernetes == nil || normalized.Kubernetes.Aliases["dev"] != "source-dev" {
		t.Fatalf("normalized source path: %+v, %v", normalized.Kubernetes, err)
	}
}

func TestExportRefusesActiveSettingsPath(t *testing.T) {
	store := NewStore(t.TempDir())
	if _, err := store.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	links := t.TempDir()
	directory := filepath.Join(links, "directory")
	if err := os.Symlink(store.Directory(), directory); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(links, "hardlink.yaml")
	if err := os.Link(store.Path(), file); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{store.Path(), filepath.Join(directory, "settings.yaml"), file} {
		if err := store.ExportTransfer(target, []TransferField{TransferPort}); err == nil {
			t.Fatalf("overwrote via %s", target)
		}
	}
	if after, err := os.ReadFile(store.Path()); err != nil || string(after) != string(before) {
		t.Fatalf("active settings changed: %v", err)
	}
}
