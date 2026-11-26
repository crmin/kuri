package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/crmin/kuri/internal/forward"
	"github.com/crmin/kuri/internal/menuview"
	"github.com/crmin/kuri/internal/settings"
)

const (
	version        = 1
	foreground     = "foreground"
	background     = "background"
	startTimeout   = 40 * time.Second
	controlTimeout = 5 * time.Second
)

var (
	ErrNoBackground          = errors.New("No background kuri process is running.")
	ErrAlreadyRunning        = errors.New("kuri is already running.")
	ErrAutoStartDisabled     = errors.New("Automatic proxy startup is disabled.")
	ErrSettingsChanged       = errors.New("Settings changed before the reverse proxy could apply them.")
	ErrPortRestartRequired   = errors.New("Changing this listener requires restarting the reverse proxy.")
	ErrPortOccupied          = errors.New("port is already in use by another process")
	ErrIncompatible          = errors.New("Incompatible kuri control protocol. Update the installed kuri binary.")
	errNoServer              = errors.New("kuri is not running")
	errBindPermission        = errors.New("permission denied")
	errActivationUnavailable = errors.New("kuri LaunchDaemon activation socket is unavailable")
)

type State struct {
	PID              int              `json:"pid"`
	Mode             string           `json:"mode"`
	Started          time.Time        `json:"started"`
	Rows             []forward.Row    `json:"services"`
	Token            string           `json:"token,omitempty"`
	Port             int              `json:"port,omitempty"`
	ProxyFingerprint string           `json:"proxy_fingerprint,omitempty"`
	Activated        bool             `json:"activated,omitempty"`
	ForegroundOwner  int              `json:"foreground_owner,omitempty"`
	AppliedSettings  *settings.Config `json:"applied_settings,omitempty"`
}

type request struct {
	Version           int    `json:"version"`
	Action            string `json:"action"`
	Token             string `json:"token,omitempty"`
	Menu              bool   `json:"menu,omitempty"`
	Automatic         bool   `json:"automatic,omitempty"`
	History           bool   `json:"history,omitempty"`
	Fingerprint       string `json:"fingerprint,omitempty"`
	ForegroundHandoff bool   `json:"foreground_handoff,omitempty"`
	PortChange        string `json:"port_change,omitempty"`
}

type reply struct {
	Version    int             `json:"version"`
	State      State           `json:"state"`
	Code       string          `json:"code,omitempty"`
	Error      string          `json:"error,omitempty"`
	Menu       *menuview.State `json:"menu,omitempty"`
	PortChange string          `json:"port_change,omitempty"`
}

func writeReply(out io.Writer, value reply) error {
	if value.Version == 0 {
		value.Version = version
	}
	return json.NewEncoder(out).Encode(value)
}

func failure(err error) reply {
	switch {
	case errors.Is(err, ErrNoBackground):
		return reply{Code: "not_running", Error: err.Error()}
	case errors.Is(err, ErrAlreadyRunning):
		return reply{Code: "already_running", Error: err.Error()}
	case errors.Is(err, ErrAutoStartDisabled):
		return reply{Code: "auto_start_disabled", Error: err.Error()}
	case errors.Is(err, ErrSettingsChanged):
		return reply{Code: "settings_changed", Error: err.Error()}
	case errors.Is(err, ErrPortRestartRequired):
		return reply{Code: "restart_required", Error: err.Error()}
	case errors.Is(err, ErrPortOccupied):
		return reply{Code: "port_occupied", Error: err.Error()}
	case errors.Is(err, ErrIncompatible):
		return reply{Code: "incompatible", Error: err.Error()}
	case errors.Is(err, errBindPermission):
		return reply{Code: "bind_permission", Error: err.Error()}
	default:
		return reply{Code: "error", Error: err.Error()}
	}
}

func decodeReply(decoder *json.Decoder) (State, error) {
	answer, err := decodeControlReply(decoder)
	return answer.State, err
}

func decodeControlReply(decoder *json.Decoder) (reply, error) {
	var answer reply
	if err := decoder.Decode(&answer); err != nil {
		return reply{}, err
	}
	if answer.Version != version {
		return reply{}, ErrIncompatible
	}
	if answer.Error == "" {
		return answer, nil
	}
	switch answer.Code {
	case "not_running":
		return reply{}, ErrNoBackground
	case "already_running":
		return reply{}, ErrAlreadyRunning
	case "auto_start_disabled":
		return reply{}, ErrAutoStartDisabled
	case "settings_changed":
		return reply{}, ErrSettingsChanged
	case "restart_required":
		return reply{}, ErrPortRestartRequired
	case "port_occupied":
		return reply{}, ErrPortOccupied
	case "incompatible":
		return reply{}, ErrIncompatible
	case "bind_permission":
		return reply{}, errBindPermission
	default:
		return reply{}, errors.New(answer.Error)
	}
}

func sendRequest(conn *net.UnixConn, action string) error {
	return json.NewEncoder(conn).Encode(request{Version: version, Action: action})
}

func readRequest(conn *net.UnixConn) (request, error) {
	uid, _, err := peer(conn)
	if err != nil || uid != os.Geteuid() {
		return request{}, errors.New("unauthorized control connection")
	}
	var req request
	if err := json.NewDecoder(io.LimitReader(conn, 4096)).Decode(&req); err != nil {
		return request{}, errors.New("invalid control request")
	}
	if req.Version != version {
		return request{}, ErrIncompatible
	}
	return req, nil
}

func dialControl(ctx context.Context, path string) (*net.UnixConn, int, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
	if err != nil {
		return nil, 0, err
	}
	unix := conn.(*net.UnixConn)
	uid, pid, err := peer(unix)
	if err != nil || uid != os.Geteuid() || pid <= 0 {
		unix.Close()
		return nil, 0, errors.New("could not authenticate the kuri control socket")
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = unix.SetDeadline(deadline)
	}
	return unix, pid, nil
}

func (app *App) connectRuntime(ctx context.Context) (*net.UnixConn, int, error) {
	conn, pid, err := dialControl(ctx, filepath.Join(app.home, ".kuri", "ctl"))
	if errors.Is(err, os.ErrNotExist) || connectionRefused(err) {
		return nil, 0, errNoServer
	}
	if err != nil {
		return nil, 0, fmt.Errorf("connect to kuri control socket: %w", err)
	}
	return conn, pid, nil
}
