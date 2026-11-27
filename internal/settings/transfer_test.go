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
