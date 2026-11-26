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
)

const (
	version        = 1
	foreground     = "foreground"
	background     = "background"
	startTimeout   = 40 * time.Second
	controlTimeout = 5 * time.Second
)

var (
	ErrNoBackground   = errors.New("No background kuri process is running.")
	ErrAlreadyRunning = errors.New("kuri is already running.")
	ErrIncompatible   = errors.New("Incompatible kuri control protocol. Update the installed kuri binary.")
	errNoServer       = errors.New("kuri is not running")
)

type State struct {
	PID     int           `json:"pid"`
	Mode    string        `json:"mode"`
	Started time.Time     `json:"started"`
	Rows    []forward.Row `json:"services"`
	Token   string        `json:"token,omitempty"`
}

type request struct {
	Version int    `json:"version"`
	Action  string `json:"action"`
	Token   string `json:"token,omitempty"`
}

type reply struct {
	Version int    `json:"version"`
	State   State  `json:"state"`
	Code    string `json:"code,omitempty"`
	Error   string `json:"error,omitempty"`
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
	case errors.Is(err, ErrIncompatible):
		return reply{Code: "incompatible", Error: err.Error()}
	default:
		return reply{Code: "error", Error: err.Error()}
	}
}

func decodeReply(decoder *json.Decoder) (State, error) {
	var answer reply
	if err := decoder.Decode(&answer); err != nil {
		return State{}, err
	}
	if answer.Version != version {
		return State{}, ErrIncompatible
	}
	if answer.Error == "" {
		return answer.State, nil
	}
	switch answer.Code {
	case "not_running":
		return State{}, ErrNoBackground
	case "already_running":
		return State{}, ErrAlreadyRunning
	case "incompatible":
		return State{}, ErrIncompatible
	default:
		return State{}, errors.New(answer.Error)
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
