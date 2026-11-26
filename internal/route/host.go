package route

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"

	"github.com/crmin/kuri/internal/settings"
)

var dnsLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
var digits = regexp.MustCompile(`^[0-9]+$`)

type Target struct {
	Context   string
	Namespace string
	Service   string
	Port      int32
}

type Request struct {
	Target
	Host string
}

type Contexts struct {
	current string
	names   map[string]bool
	aliases map[string]string
	server  settings.Server
}

func NewContexts(current string, names []string) Contexts {
	return NewConfiguredContexts(current, names, settings.Defaults("").Server)
}

func NewConfiguredContexts(current string, names []string, server settings.Server) Contexts {
	contexts := Contexts{current: current, names: make(map[string]bool), aliases: make(map[string]string), server: server}
	for _, name := range names {
		contexts.names[name] = true
		alias := Alias(name)
		if existing, ok := contexts.aliases[alias]; ok && existing != name {
			contexts.aliases[alias] = ""
		} else {
			contexts.aliases[alias] = name
		}
	}
	return contexts
}

func (c Contexts) Enabled(name string) bool { return c.names[name] }

func Alias(name string) string {
	if dnsLabel.MatchString(name) {
		return name
	}
	sum := sha256.Sum256([]byte(name))
	return fmt.Sprintf("ctx-%x", sum[:8])
}

func ForwardLabel(target Target, defaultPort bool) string {
	label := target.Service + "." + target.Namespace + "." + Alias(target.Context)
	if !defaultPort {
		return strconv.Itoa(int(target.Port)) + "." + label
	}
	return label
}

func (c Contexts) ForwardHost(target Target, defaultPort bool) string {
	_, host := c.ForwardAddress(target, defaultPort)
	return host
}

func (c Contexts) ForwardAddress(target Target, defaultPort bool) (label, authority string) {
	base := target.Service + "." + target.Namespace
	explicit := base + "." + Alias(target.Context)
	portLabel := strconv.Itoa(int(target.Port)) + "." + explicit
	options := []string{portLabel}
	if defaultPort {
		options = []string{explicit, base, portLabel}
	}
	for _, label := range options {
		host := label + "." + c.server.Domain
		got, err := c.Parse(host)
		if err != nil {
			continue
		}
		if got.Port == 0 && defaultPort {
			got.Port = target.Port
		}
		if got.Target != target {
			continue
		}
		if c.server.Port != 80 {
			host = net.JoinHostPort(host, strconv.Itoa(c.server.Port))
		}
		return label, host
	}
	return ForwardLabel(target, defaultPort), ""
}

func normalizeHost(host, suffix string) (string, error) {
	if strings.Contains(host, ":") {
		name, port, err := net.SplitHostPort(host)
		number, portErr := strconv.ParseUint(port, 10, 16)
		if err != nil || portErr != nil || number == 0 || !digits.MatchString(port) || strings.HasPrefix(host, "[") {
			return "", errors.New("invalid host port")
		}
		host = name
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if len(host) > 253 || !strings.HasSuffix(host, suffix) {
		return "", fmt.Errorf("expected a host under %s", strings.TrimPrefix(suffix, "."))
	}
	return host, nil
}

func (c Contexts) Parse(host string) (Request, error) {
	suffix := "." + c.server.Domain
	name, err := normalizeHost(host, suffix)
	if err != nil {
		return Request{}, err
	}
	labels := strings.Split(strings.TrimSuffix(name, suffix), ".")
	if len(labels) < 2 || len(labels) > 4 {
		return Request{}, errors.New("invalid kuri host format")
	}
	styles := c.server.Styles
	if (len(labels) == 2 && !styles.CurrentContext) || (len(labels) == 3 && !styles.ExplicitContext) ||
		(len(labels) == 4 && !styles.ExplicitPort) {
		return Request{}, errors.New("this domain style is disabled")
	}
	for _, label := range labels {
		if !dnsLabel.MatchString(label) {
			return Request{}, errors.New("invalid DNS label in host")
		}
	}

	var port int32
	if len(labels) == 4 {
		number, err := strconv.ParseUint(labels[0], 10, 16)
		if err != nil || number == 0 || !digits.MatchString(labels[0]) {
			return Request{}, errors.New("invalid service port in host")
		}
		port = int32(number)
		labels = labels[1:]
	}

	context := c.current
	if len(labels) == 3 {
		context = c.aliases[labels[2]]
		if context == "" {
			return Request{}, errors.New("unknown or ambiguous Kubernetes context in host")
		}
	} else if !c.names[context] {
		return Request{}, errors.New("no valid current Kubernetes context")
	}
	return Request{Target: Target{Context: context, Namespace: labels[1], Service: labels[0], Port: port}, Host: name}, nil
}
