package route

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
)

const suffix = ".kuri.localhost"

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
}

func NewContexts(current string, names []string) Contexts {
	contexts := Contexts{current: current, names: make(map[string]bool), aliases: make(map[string]string)}
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
	host := ForwardLabel(target, defaultPort) + suffix
	want := target
	if defaultPort {
		want.Port = 0
	}
	parsed, err := c.Parse(host)
	if err != nil || parsed.Target != want {
		return ""
	}
	return host
}

func NormalizeHost(host string) (string, error) {
	if strings.Contains(host, ":") {
		name, port, err := net.SplitHostPort(host)
		if err != nil || port != "80" || strings.HasPrefix(host, "[") {
			return "", errors.New("invalid host port")
		}
		host = name
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if len(host) > 253 || !strings.HasSuffix(host, suffix) {
		return "", errors.New("expected a host under kuri.localhost")
	}
	return host, nil
}

func (c Contexts) Parse(host string) (Request, error) {
	name, err := NormalizeHost(host)
	if err != nil {
		return Request{}, err
	}
	labels := strings.Split(strings.TrimSuffix(name, suffix), ".")
	if len(labels) < 2 || len(labels) > 4 {
		return Request{}, errors.New("invalid kuri host format")
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
