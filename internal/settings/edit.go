package settings

import (
	"errors"
	"slices"
	"strconv"
)

type Edit struct {
	AutoStart  *bool        `json:"auto_start,omitempty"`
	Register   *bool        `json:"register,omitempty"`
	ConfigPath *string      `json:"config_path,omitempty"`
	Context    *ContextEdit `json:"context,omitempty"`
	Style      *StyleEdit   `json:"style,omitempty"`
	KeepAlive  *bool        `json:"keep_alive,omitempty"`
	Trigger    *string      `json:"trigger,omitempty"`
	Domain     *string      `json:"domain,omitempty"`
	Port       *string      `json:"port,omitempty"`
	Minutes    *string      `json:"minutes,omitempty"`
	MinPort    *string      `json:"min_port,omitempty"`
	MaxPort    *string      `json:"max_port,omitempty"`
	Reset      bool         `json:"reset,omitempty"`
}

type ContextEdit struct {
	Name string `json:"name"`
	On   bool   `json:"on"`
}

type StyleEdit struct {
	Name string `json:"name"`
	On   bool   `json:"on"`
}

func (e Edit) Apply(c *Config) error {
	if e.AutoStart != nil {
		c.General.AutoStart = *e.AutoStart
	}
	if e.Register != nil {
		c.General.LaunchAtLogin = *e.Register
	}
	if e.ConfigPath != nil && c.Kubernetes.ConfigPath != *e.ConfigPath {
		c.Kubernetes.ConfigPath = *e.ConfigPath
		c.Kubernetes.ExcludedContexts = []string{}
	}
	if e.Context != nil {
		selection := e.Context
		c.Kubernetes.ExcludedContexts = slices.DeleteFunc(slices.Clone(c.Kubernetes.ExcludedContexts),
			func(name string) bool { return name == selection.Name })
		if !selection.On {
			c.Kubernetes.ExcludedContexts = append(c.Kubernetes.ExcludedContexts, selection.Name)
		}
	}
	if e.Style != nil {
		style := e.Style
		switch style.Name {
		case "explicit_context":
			c.Server.Styles.ExplicitContext = style.On
		case "explicit_port":
			c.Server.Styles.ExplicitPort = style.On
		case "current_context":
			c.Server.Styles.CurrentContext = style.On
		default:
			return errors.New("Choose a valid domain style.")
		}
	}
	if e.KeepAlive != nil {
		c.PortForward.KeepAlive = *e.KeepAlive
	}
	if e.Trigger != nil {
		c.PortForward.Trigger = *e.Trigger
	}
	if e.Domain != nil {
		c.Server.Domain = *e.Domain
	}
	if e.Port != nil {
		n, err := parsePort(*e.Port)
		if err != nil {
			return errors.New("Enter a server port from 1 to 65535.")
		}
		c.Server.Port = n
	}
	if e.MinPort != nil {
		n, err := parsePort(*e.MinPort)
		if err != nil {
			return errors.New("Enter a minimum port from 1 to 65535.")
		}
		c.PortForward.MinPort = n
	}
	if e.MaxPort != nil {
		n, err := parsePort(*e.MaxPort)
		if err != nil {
			return errors.New("Enter a maximum port from 1 to 65535.")
		}
		c.PortForward.MaxPort = n
	}
	if e.Minutes != nil && c.PortForward.KeepAlive {
		minutes, err := strconv.ParseFloat(*e.Minutes, 64)
		if err != nil {
			return errors.New("Enter a positive keep-alive duration in minutes.")
		}
		c.PortForward.Minutes = minutes
	}
	return c.Validate()
}

func parsePort(value string) (int, error) {
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return 0, errors.New("port is not an integer")
		}
	}
	n, err := strconv.Atoi(value)
	if err != nil || !ValidPort(n) {
		return 0, errors.New("invalid port")
	}
	return n, nil
}
