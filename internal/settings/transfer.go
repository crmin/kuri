package settings

import (
	"bytes"
	"errors"
	"io"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"time"

	"go.yaml.in/yaml/v3"
	"golang.org/x/sys/unix"
)

type TransferField string

const (
	TransferDomain       TransferField = "domain"
	TransferPort         TransferField = "port"
	TransferDomainStyles TransferField = "domain_styles"
	TransferKeepAlive    TransferField = "keep_alive_enabled"
	TransferMinutes      TransferField = "keep_alive_minutes"
	TransferTrigger      TransferField = "keep_alive_trigger"
	TransferAutoHealing  TransferField = "auto_healing_enabled"
	TransferCheckSeconds TransferField = "connection_check_interval_seconds"
	TransferPortRange    TransferField = "port_range"
	TransferAliases      TransferField = "context_aliases"
)

type Transfer struct {
	Server     *TransferServer     `yaml:"server,omitempty" json:"server,omitempty"`
	Forward    *TransferForward    `yaml:"port_forward,omitempty" json:"port_forward,omitempty"`
	Kubernetes *TransferKubernetes `yaml:"kubernetes,omitempty" json:"kubernetes,omitempty"`
}

type TransferServer struct {
	Domain *string         `yaml:"domain,omitempty" json:"domain,omitempty"`
	Port   *int            `yaml:"port,omitempty" json:"port,omitempty"`
	Styles *TransferStyles `yaml:"domain_styles,omitempty" json:"domain_styles,omitempty"`
}

type TransferStyles struct {
	ExplicitContext *bool `yaml:"explicit_context,omitempty" json:"explicit_context,omitempty"`
	ExplicitPort    *bool `yaml:"explicit_port,omitempty" json:"explicit_port,omitempty"`
	CurrentContext  *bool `yaml:"current_context,omitempty" json:"current_context,omitempty"`
}

type TransferForward struct {
	KeepAlive    *bool    `yaml:"keep_alive_enabled,omitempty" json:"keep_alive_enabled,omitempty"`
	Minutes      *float64 `yaml:"keep_alive_minutes,omitempty" json:"keep_alive_minutes,omitempty"`
	Trigger      *string  `yaml:"keep_alive_trigger,omitempty" json:"keep_alive_trigger,omitempty"`
	AutoHealing  *bool    `yaml:"auto_healing_enabled,omitempty" json:"auto_healing_enabled,omitempty"`
	CheckSeconds *int     `yaml:"connection_check_interval_seconds,omitempty" json:"connection_check_interval_seconds,omitempty"`
	MinPort      *int     `yaml:"min_port,omitempty" json:"min_port,omitempty"`
	MaxPort      *int     `yaml:"max_port,omitempty" json:"max_port,omitempty"`
}

type TransferKubernetes struct {
	Aliases map[string]string `yaml:"context_aliases" json:"context_aliases"`
}

var errTransferFormat = errors.New("Choose a valid kuri settings YAML file.")

func ReadTransfer(path string, contextNames []string) (Transfer, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return Transfer{}, errors.New("Could not open the settings file. Check that the file is readable and try again.")
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxSettingsSize {
		return Transfer{}, errTransferFormat
	}
	data, err := io.ReadAll(io.LimitReader(file, maxSettingsSize+1))
	if err != nil || len(data) > maxSettingsSize {
		return Transfer{}, errTransferFormat
	}
	return DecodeTransfer(bytes.NewReader(data), contextNames)
}

func DecodeTransfer(source io.Reader, contextNames []string) (Transfer, error) {
	decoder := yaml.NewDecoder(source)
	var doc, extra yaml.Node
	if err := decoder.Decode(&doc); err != nil || len(doc.Content) != 1 || !errors.Is(decoder.Decode(&extra), io.EOF) {
		return Transfer{}, errTransferFormat
	}
	sections, err := transferMapping(doc.Content[0])
	if err != nil {
		return Transfer{}, err
	}
	var result Transfer
	recognized := false
	for _, section := range []string{"general", "server", "port_forward", "kubernetes"} {
		node := sections[section]
		if node == nil {
			continue
		}
		fields, err := transferMapping(node)
		if err != nil {
			return Transfer{}, err
		}
		recognized = true
		switch section {
		case "server":
			if domain := fields["domain"]; domain != nil && domain.Tag != "!!str" && domain.Tag != "!!null" {
				return Transfer{}, errTransferFormat
			}
			if !transferIntegers(fields, "port") {
				return Transfer{}, errTransferFormat
			}
			if err := node.Decode(&result.Server); err != nil {
				return Transfer{}, errTransferFormat
			}
			if styles := fields["domain_styles"]; styles != nil {
				if _, err := transferMapping(styles); err != nil {
					return Transfer{}, err
				}
			}
		case "port_forward":
			if !transferIntegers(fields, "min_port", "max_port", "connection_check_interval_seconds") {
				return Transfer{}, errTransferFormat
			}
			if err := node.Decode(&result.Forward); err != nil {
				return Transfer{}, errTransferFormat
			}
			if trigger := fields["keep_alive_trigger"]; trigger != nil && trigger.Tag != "!!str" && trigger.Tag != "!!null" {
				return Transfer{}, errTransferFormat
			}
			if minutes := result.Forward.Minutes; minutes != nil && (math.IsNaN(*minutes) || math.IsInf(*minutes, 0)) {
				return Transfer{}, errTransferFormat
			}
		case "kubernetes":
			if aliases := fields["context_aliases"]; aliases != nil {
				values, err := transferAliasMapping(aliases, fields["config_path"])
				if err != nil {
					return Transfer{}, err
				}
				selected := make(map[string]string)
				for _, name := range contextNames {
					value := values[name]
					if value == nil || value.Tag == "!!null" {
						continue
					}
					if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
						return Transfer{}, errTransferFormat
					}
					selected[name] = value.Value
				}
				if len(selected) > 0 {
					result.Kubernetes = &TransferKubernetes{Aliases: selected}
				}
			}
		}
	}
	if !recognized {
		return Transfer{}, errTransferFormat
	}
	return result, nil
}

func transferIntegers(fields map[string]*yaml.Node, keys ...string) bool {
	for _, key := range keys {
		if value := fields[key]; value != nil && value.ShortTag() != "!!int" && value.ShortTag() != "!!null" {
			return false
		}
	}
	return true
}

// A local settings file carries the source profile. Only that profile's
// aliases are portable; the destination always uses its own kubeconfig.
func transferAliasMapping(aliases, sourcePath *yaml.Node) (map[string]*yaml.Node, error) {
	values, err := transferMapping(aliases)
	if err != nil || sourcePath == nil {
		return values, err
	}
	for key, value := range values {
		if !filepath.IsAbs(key) || value.Kind != yaml.MappingNode {
			continue
		}
		if sourcePath.Kind != yaml.ScalarNode || sourcePath.Tag != "!!str" {
			return nil, errTransferFormat
		}
		if profile := values[sourcePath.Value]; profile != nil {
			return transferMapping(profile)
		}
		return nil, nil
	}
	return values, nil
}

func transferMapping(node *yaml.Node) (map[string]*yaml.Node, error) {
	if node.Kind != yaml.MappingNode {
		return nil, errTransferFormat
	}
	fields := make(map[string]*yaml.Node)
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || fields[key.Value] != nil {
			return nil, errTransferFormat
		}
		fields[key.Value] = node.Content[i+1]
	}
	return fields, nil
}

func (t Transfer) Fields() []TransferField {
	fields := make([]TransferField, 0, 10)
	if s := t.Server; s != nil {
		if s.Domain != nil {
			fields = append(fields, TransferDomain)
		}
		if s.Port != nil {
			fields = append(fields, TransferPort)
		}
		if s.Styles != nil && *s.Styles != (TransferStyles{}) {
			fields = append(fields, TransferDomainStyles)
		}
	}
	if p := t.Forward; p != nil {
		if p.KeepAlive != nil {
			fields = append(fields, TransferKeepAlive)
		}
		if p.Minutes != nil {
			fields = append(fields, TransferMinutes)
		}
		if p.Trigger != nil {
			fields = append(fields, TransferTrigger)
		}
		if p.AutoHealing != nil {
			fields = append(fields, TransferAutoHealing)
		}
		if p.CheckSeconds != nil {
			fields = append(fields, TransferCheckSeconds)
		}
		if p.MinPort != nil || p.MaxPort != nil {
			fields = append(fields, TransferPortRange)
		}
	}
	if t.Kubernetes != nil {
		fields = append(fields, TransferAliases)
	}
	return fields
}

func (t Transfer) Select(fields []TransferField) Transfer {
	var out Transfer
	if s := t.Server; s != nil {
		part := TransferServer{}
		if slices.Contains(fields, TransferDomain) {
			part.Domain = s.Domain
		}
		if slices.Contains(fields, TransferPort) {
			part.Port = s.Port
		}
		if slices.Contains(fields, TransferDomainStyles) {
			part.Styles = s.Styles
		}
		if part != (TransferServer{}) {
			out.Server = &part
		}
	}
	if p := t.Forward; p != nil {
		part := TransferForward{}
		if slices.Contains(fields, TransferKeepAlive) {
			part.KeepAlive = p.KeepAlive
		}
		if slices.Contains(fields, TransferMinutes) {
			part.Minutes = p.Minutes
		}
		if slices.Contains(fields, TransferTrigger) {
			part.Trigger = p.Trigger
		}
		if slices.Contains(fields, TransferAutoHealing) {
			part.AutoHealing = p.AutoHealing
		}
		if slices.Contains(fields, TransferCheckSeconds) {
			part.CheckSeconds = p.CheckSeconds
		}
		if slices.Contains(fields, TransferPortRange) {
			part.MinPort, part.MaxPort = p.MinPort, p.MaxPort
		}
		if part != (TransferForward{}) {
			out.Forward = &part
		}
	}
	if slices.Contains(fields, TransferAliases) {
		out.Kubernetes = t.Kubernetes
	}
	return out
}

func (t Transfer) Apply(c *Config) {
	if s := t.Server; s != nil {
		assign(&c.Server.Domain, s.Domain)
		assign(&c.Server.Port, s.Port)
		if styles := s.Styles; styles != nil {
			assign(&c.Server.Styles.ExplicitContext, styles.ExplicitContext)
			assign(&c.Server.Styles.ExplicitPort, styles.ExplicitPort)
			assign(&c.Server.Styles.CurrentContext, styles.CurrentContext)
		}
	}
	if p := t.Forward; p != nil {
		f := &c.PortForward
		assign(&f.KeepAlive, p.KeepAlive)
		assign(&f.Minutes, p.Minutes)
		assign(&f.Trigger, p.Trigger)
		assign(&f.MinPort, p.MinPort)
		assign(&f.MaxPort, p.MaxPort)
		if p.AutoHealing != nil {
			f.AutoHealing = new(*p.AutoHealing)
		}
		if p.CheckSeconds != nil {
			f.CheckSeconds = new(*p.CheckSeconds)
		}
	}
}

func assign[T any](target *T, source *T) {
	if source != nil {
		*target = *source
	}
}

func TransferFromConfig(cfg Config) Transfer {
	s, f := cfg.Server, cfg.PortForward
	aliases := maps.Clone(cfg.Kubernetes.Aliases())
	if aliases == nil {
		aliases = make(map[string]string)
	}
	return Transfer{
		Server: &TransferServer{Domain: &s.Domain, Port: &s.Port, Styles: &TransferStyles{
			ExplicitContext: &s.Styles.ExplicitContext, ExplicitPort: &s.Styles.ExplicitPort, CurrentContext: &s.Styles.CurrentContext,
		}},
		Forward: &TransferForward{KeepAlive: &f.KeepAlive, Minutes: &f.Minutes, Trigger: &f.Trigger,
			AutoHealing: new(f.AutoHealingEnabled()), CheckSeconds: new(int(f.CheckInterval() / time.Second)), MinPort: &f.MinPort, MaxPort: &f.MaxPort,
		},
		Kubernetes: &TransferKubernetes{Aliases: aliases},
	}
}

func (s Store) ExportTransfer(path string, fields []TransferField) error {
	if !filepath.IsAbs(path) {
		return errors.New("Choose a location for the exported settings file.")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return errors.New("Choose an accessible folder for the exported settings file.")
	}
	target := filepath.Join(parent, filepath.Base(path))
	activeParent, _ := filepath.EvalSymlinks(s.Directory())
	active, activeErr := os.Stat(s.Path())
	chosen, chosenErr := os.Stat(target)
	if filepath.Clean(path) == s.Path() || (activeParent != "" && target == filepath.Join(activeParent, filepath.Base(s.Path()))) ||
		(activeErr == nil && chosenErr == nil && os.SameFile(active, chosen)) {
		return errors.New("Choose a different file. kuri is using this settings file.")
	}
	cfg, err := s.Read()
	if err != nil {
		return err
	}
	selected := TransferFromConfig(cfg).Select(fields)
	if len(selected.Fields()) == 0 {
		return errors.New("Select at least one setting to export.")
	}
	data, err := yaml.Marshal(selected)
	if err == nil {
		err = replaceSettingsFile(parent, target, data)
	}
	if err != nil {
		return errors.New("Could not save the exported settings file. Check the folder permissions and try again.")
	}
	return nil
}
