package settings

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	ConfigPath       string   `yaml:"config_path" json:"config_path"`
	ExcludedContexts []string `yaml:"excluded_contexts" json:"excluded_contexts"`
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
	LastRequest   = "last_request"
	ForwardOpened = "forward_opened"
)

type PortForward struct {
	KeepAlive bool    `yaml:"keep_alive_enabled" json:"keep_alive_enabled"`
	Minutes   float64 `yaml:"keep_alive_minutes" json:"keep_alive_minutes"`
	Trigger   string  `yaml:"keep_alive_trigger" json:"keep_alive_trigger"`
	MinPort   int     `yaml:"min_port" json:"min_port"`
	MaxPort   int     `yaml:"max_port" json:"max_port"`
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
	c.Server.Domain = domain
	c.Kubernetes.ConfigPath = filepath.Clean(c.Kubernetes.ConfigPath)
	excluded := slices.Clone(c.Kubernetes.ExcludedContexts)
	slices.Sort(excluded)
	c.Kubernetes.ExcludedContexts = slices.Compact(excluded)
	if c.Kubernetes.ExcludedContexts == nil {
		c.Kubernetes.ExcludedContexts = []string{}
	}
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

func (c Config) Fingerprint() string {
	return fingerprint(c)
}

func (c Config) ProxyFingerprint() string {
	c.General = General{}
	if !c.PortForward.KeepAlive {
		c.PortForward.Minutes = 0
		c.PortForward.Trigger = ""
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
