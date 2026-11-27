package settings

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

type Config struct {
	General     General     `yaml:"general" json:"general"`
	Kubernetes  Kubernetes  `yaml:"kubernetes" json:"kubernetes"`
	Server      Server      `yaml:"server" json:"server"`
	PortForward PortForward `yaml:"port_forward" json:"port_forward"`
}

type General struct {
	LaunchAtLogin bool `yaml:"launch_at_login" json:"launch_at_login"`
	AutoStart     bool `yaml:"auto_start_proxy" json:"auto_start_proxy"`
}

type Kubernetes struct {
	ConfigPath       string                       `yaml:"config_path" json:"config_path"`
	ExcludedContexts []string                     `yaml:"excluded_contexts" json:"excluded_contexts"`
	ContextAliases   map[string]map[string]string `yaml:"context_aliases,omitempty" json:"context_aliases,omitempty"`
}

func (k Kubernetes) Aliases() map[string]string {
	return k.ContextAliases[k.ConfigPath]
}

func (k *Kubernetes) MergeAliases(changes map[string]string) {
	profiles := maps.Clone(k.ContextAliases)
	if profiles == nil {
		profiles = make(map[string]map[string]string)
	}
	aliases := maps.Clone(k.Aliases())
	if aliases == nil {
		aliases = make(map[string]string)
	}
	for name, alias := range changes {
		if alias == name {
			delete(aliases, name)
		} else {
			aliases[name] = alias
		}
	}
	if len(aliases) == 0 {
		delete(profiles, k.ConfigPath)
	} else {
		profiles[k.ConfigPath] = aliases
	}
	k.ContextAliases = profiles
}

type Server struct {
	Domain string       `yaml:"domain" json:"domain"`
	Port   int          `yaml:"port" json:"port"`
	Styles DomainStyles `yaml:"domain_styles" json:"domain_styles"`
}

type DomainStyles struct {
	ExplicitContext bool `yaml:"explicit_context" json:"explicit_context"`
	ExplicitPort    bool `yaml:"explicit_port" json:"explicit_port"`
	CurrentContext  bool `yaml:"current_context" json:"current_context"`
}

const (
	LastRequest           = "last_request"
	ForwardOpened         = "forward_opened"
	SettingsSchemaVersion = 2
	defaultCheckSeconds   = 10
)

type PortForward struct {
	KeepAlive    bool    `yaml:"keep_alive_enabled" json:"keep_alive_enabled"`
	Minutes      float64 `yaml:"keep_alive_minutes" json:"keep_alive_minutes"`
	Trigger      string  `yaml:"keep_alive_trigger" json:"keep_alive_trigger"`
	MinPort      int     `yaml:"min_port" json:"min_port"`
	MaxPort      int     `yaml:"max_port" json:"max_port"`
	AutoHealing  *bool   `yaml:"auto_healing_enabled,omitempty" json:"auto_healing_enabled,omitempty"`
	CheckSeconds *int    `yaml:"connection_check_interval_seconds,omitempty" json:"connection_check_interval_seconds,omitempty"`
}

func Defaults(home string) Config {
	return Config{
		General:    General{AutoStart: true},
		Kubernetes: Kubernetes{ConfigPath: filepath.Join(home, ".kube", "config"), ExcludedContexts: []string{}},
		Server: Server{Domain: "kuri.localhost", Port: 80, Styles: DomainStyles{
			ExplicitContext: true, ExplicitPort: true, CurrentContext: true,
		}},
		PortForward: PortForward{KeepAlive: true, Minutes: 60, Trigger: LastRequest, MinPort: 30000, MaxPort: 39999},
	}
}

var domainLabel = regexp.MustCompile("^[A-Za-z0-9_~!$&'()*+,;=-]+$")
var contextAliasLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func (c *Config) Validate() error {
	domain := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(c.Server.Domain), "."))
	if len(domain) > 253 {
		return errors.New("Enter a valid service domain.")
	}
	for _, label := range strings.Split(domain, ".") {
		if !domainLabel.MatchString(label) {
			return errors.New("Enter a valid service domain.")
		}
	}
	if !ValidPort(c.Server.Port) {
		return errors.New("Enter a server port from 1 to 65535.")
	}
	if !filepath.IsAbs(c.Kubernetes.ConfigPath) {
		return errors.New("Choose an absolute kubeconfig path.")
	}
	fwd := c.PortForward
	if !ValidPort(fwd.MinPort) || !ValidPort(fwd.MaxPort) || fwd.MinPort > fwd.MaxPort {
		return errors.New("Enter a valid port range from 1 to 65535. Min must be less than or equal to Max.")
	}
	if fwd.Duration() <= 0 {
		return errors.New("Enter a positive keep-alive duration in minutes.")
	}
	if fwd.Trigger != LastRequest && fwd.Trigger != ForwardOpened {
		return errors.New("Choose a valid keep-alive timer trigger.")
	}
	if fwd.CheckInterval() <= 0 {
		return errors.New("Enter a positive connection check interval in whole seconds.")
	}
	if fwd.AutoHealingEnabled() {
		c.PortForward.AutoHealing = nil
	}
	if fwd.CheckInterval() == defaultCheckSeconds*time.Second {
		c.PortForward.CheckSeconds = nil
	}
	c.Server.Domain = domain
	c.Kubernetes.ConfigPath = filepath.Clean(c.Kubernetes.ConfigPath)
	if err := c.Kubernetes.validateAliases(); err != nil {
		return err
	}
	excluded := slices.Clone(c.Kubernetes.ExcludedContexts)
	slices.Sort(excluded)
	c.Kubernetes.ExcludedContexts = slices.Compact(excluded)
	if c.Kubernetes.ExcludedContexts == nil {
		c.Kubernetes.ExcludedContexts = []string{}
	}
	return nil
}

func (k *Kubernetes) validateAliases() error {
	if len(k.ContextAliases) == 0 {
		return nil
	}
	profiles := make(map[string]map[string]string)
	for path, aliases := range k.ContextAliases {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return errors.New("Use an absolute, normalized kubeconfig path for stored aliases.")
		}
		validated := make(map[string]string)
		used := make(map[string]bool)
		for name, alias := range aliases {
			if name == alias && name != "" {
				continue
			}
			if name == "" || !contextAliasLabel.MatchString(alias) {
				return errors.New("Enter an alias of up to 63 lowercase letters, digits, or hyphens. Start and end with a letter or digit.")
			}
			if used[alias] {
				return fmt.Errorf("The alias %q is already assigned to another context.", alias)
			}
			used[alias], validated[name] = true, alias
		}
		if len(validated) > 0 {
			profiles[path] = validated
		}
	}
	k.ContextAliases = profiles
	return nil
}

func ValidPort(port int) bool { return port >= 1 && port <= 65535 }

func (f PortForward) Duration() time.Duration {
	nanos := f.Minutes * float64(time.Minute)
	if math.IsNaN(nanos) || nanos < 1 || nanos >= float64(math.MaxInt64) {
		return 0
	}
	return time.Duration(nanos)
}

func (f PortForward) AutoHealingEnabled() bool { return f.AutoHealing == nil || *f.AutoHealing }

func (f PortForward) CheckInterval() time.Duration {
	if f.CheckSeconds == nil {
		return defaultCheckSeconds * time.Second
	}
	seconds := *f.CheckSeconds
	if seconds <= 0 || int64(seconds) > math.MaxInt64/int64(time.Second) {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func (c Config) RequiredSchemaVersion() int {
	if len(c.Kubernetes.ContextAliases) > 0 {
		return 2
	}
	if c.PortForward.AutoHealing != nil || c.PortForward.CheckSeconds != nil {
		return 1
	}
	return 0
}

func (c Config) Fingerprint() string {
	return fingerprint(c)
}

func (c Config) ProxyFingerprint() string {
	c.General = General{}
	if !c.PortForward.KeepAlive {
		c.PortForward.Minutes = 0
		c.PortForward.Trigger = ""
	}
	if !c.PortForward.AutoHealingEnabled() {
		c.PortForward.CheckSeconds = nil
	}
	return fingerprint(c)
}

func fingerprint(c Config) string {
	value, err := json.Marshal(c)
	if err != nil {
		panic(fmt.Errorf("fingerprint unvalidated settings: %w", err))
	}
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
