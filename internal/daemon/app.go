package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"github.com/crmin/kuri/internal/accesslog"
	"github.com/crmin/kuri/internal/launchd"
	"github.com/crmin/kuri/internal/settings"

	"golang.org/x/sys/unix"
)

type App struct {
	home, activation string
	detached         bool
	activated        bool
	automaticStart   bool
	passiveStart     bool
	handoff          *foregroundHandoff
	localHandoff     chan string
	recovery         *settings.Config
	stdout, stderr   io.Writer
	register         func(context.Context) error
	direct           func(context.Context, string) (int, error)
	portChange       func() (launchd.PortChange, error)
}

func New(home, activation string, stdout, stderr io.Writer) *App {
	app := &App{home: home, activation: activation, stdout: stdout, stderr: stderr, portChange: launchd.ReadPortChange}
	if launchd.Supported() {
		app.register = func(ctx context.Context) error {
			return launchd.Register(ctx, app.home, app.activation)
		}
	}
	app.direct = app.startDirect
	return app
}

func (app *App) RunForeground(ctx context.Context) error {
	_, err := app.run(ctx, foreground, false)
	return err
}

func (app *App) Start(ctx context.Context) (int, error) {
	return app.run(ctx, background, false)
}

func (app *App) StartAutomatic(ctx context.Context) (int, error) {
	return app.run(ctx, background, true)
}

func (app *App) run(ctx context.Context, mode string, automatic bool) (int, error) {
	state, err := app.State(ctx)
	if err != nil {
		return 0, err
	}
	if state.PID != 0 {
		return 0, ErrAlreadyRunning
	}
	useActivation := app.hasActivationSocket()
	if useActivation && app.activation == launchd.ControlPath(os.Geteuid()) {
		registration, err := app.Registration(ctx)
		if err != nil {
			return 0, err
		}
		useActivation = registration.Present()
	}
	if useActivation {
		pid, err := app.activate(ctx, mode, automatic)
		if !errors.Is(err, errActivationUnavailable) {
			return pid, err
		}
	}

	pid, err := app.direct(ctx, mode)
	if !errors.Is(err, errBindPermission) || app.register == nil {
		return pid, err
	}
	if err := app.register(ctx); err != nil {
		return 0, err
	}
	return app.activate(ctx, mode, automatic)
}

func (app *App) startDirect(ctx context.Context, mode string) (int, error) {
	if mode == background {
		return app.startDetached(ctx)
	}
	cfg, err := settings.NewStore(app.home).Read()
	if err != nil {
		return 0, err
	}
	listeners, err := bindHTTP(cfg.Server.Port)
	if err != nil {
		return 0, bindError(err, cfg.Server.Port)
	}
	hosting := *app
	hosting.localHandoff = make(chan string, 1)
	if err := hosting.serve(ctx, foreground, listeners, app.stdout, nil); err != nil {
		return os.Getpid(), err
	}
	select {
	case operation := <-hosting.localHandoff:
		pid, next, err := app.resumeForegroundMode(ctx, operation, true)
		for next != "" && ctx.Err() == nil {
			pid, next, err = app.resumeForegroundMode(ctx, next, next == operation)
		}
		if errors.Is(err, errHandoffRolledBack) {
			return app.startDirect(ctx, foreground)
		}
		return pid, err
	default:
		return os.Getpid(), nil
	}
}

func (app *App) hasActivationSocket() bool {
	info, err := os.Lstat(app.activation)
	return err == nil && info.Mode()&os.ModeSocket != 0 &&
		int(info.Sys().(*syscall.Stat_t).Uid) == os.Geteuid() && info.Mode().Perm()&0077 == 0
}

func (app *App) activate(ctx context.Context, mode string, automatic bool) (int, error) {
	pid, next, err := app.activateSession(ctx, mode, automatic, "")
	for next != "" && ctx.Err() == nil {
		pid, next, err = app.resumeForeground(ctx, next)
	}
	return pid, err
}

func (app *App) activateSession(ctx context.Context, mode string, automatic bool, operation string) (int, string, error) {
	startCtx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	// An inherited socket's peer credentials can identify launchd, which
	// created the listening socket. Its path is protected by installation.
	client, err := (&net.Dialer{}).DialContext(startCtx, "unix", app.activation)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || connectionRefused(err) {
			return 0, "", fmt.Errorf("%w: %w", errActivationUnavailable, err)
		}
		return 0, "", fmt.Errorf("connect to the launchd activation socket: %w", err)
	}
	conn := client.(*net.UnixConn)
	defer conn.Close()
	if deadline, ok := startCtx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	unblock := context.AfterFunc(startCtx, func() { _ = conn.SetDeadline(time.Now()) })
	if err := json.NewEncoder(conn).Encode(request{Version: version, Action: mode, Automatic: automatic,
		ForegroundHandoff: mode == foreground, PortChange: operation}); err != nil {
		unblock()
		return 0, "", err
	}
	reader := bufio.NewReader(conn)
	frame, err := reader.ReadBytes('\n')
	unblock()
	if err != nil {
		return 0, "", fmt.Errorf("read launchd readiness: %w", err)
	}
	state, err := decodeReply(json.NewDecoder(bytes.NewReader(frame)))
	if err != nil {
		return 0, "", fmt.Errorf("start the launchd instance: %w", err)
	}
	if state.PID <= 0 || state.Token == "" || state.Mode != mode {
		return 0, "", errors.New("invalid readiness response from launchd instance")
	}
	if mode == background {
		return state.PID, "", nil
	}

	_ = conn.SetDeadline(time.Time{})
	requestStop := context.AfterFunc(ctx, func() {
		_ = conn.CloseWrite()
		_ = conn.SetReadDeadline(time.Now().Add(startTimeout))
	})
	defer requestStop()
	console := accesslog.NewConsole(app.stdout)
	console.Start()
	var resume string
	for {
		line, err := reader.ReadBytes('\n')
		if bytes.Contains(line, []byte(`"event":"kuri_port_restart"`)) {
			var frame handoffFrame
			if json.Unmarshal(line, &frame) == nil && frame.Event == handoffEvent && frame.Operation != "" {
				if err := json.NewEncoder(conn).Encode(frame); err == nil {
					resume = frame.Operation
				}
				continue
			}
		}
		if len(line) > 0 {
			console.Enqueue(line)
		}
		if err != nil {
			app.flushConsole(console)
			if errors.Is(err, io.EOF) {
				err = nil
			}
			return state.PID, resume, err
		}
	}
}

func (app *App) startDetached(ctx context.Context) (int, error) {
	executable, err := os.Executable()
	if err != nil {
		return 0, err
	}
	readReady, writeReady, err := os.Pipe()
	if err != nil {
		return 0, err
	}
	defer readReady.Close()
	defer writeReady.Close()

	command := exec.Command(executable, "__background")
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	command.ExtraFiles = []*os.File{writeReady}
	if err := command.Start(); err != nil {
		return 0, fmt.Errorf("start background process: %w", err)
	}
	_ = writeReady.Close()
	reaped := make(chan struct{})
	go func() {
		_ = command.Wait()
		close(reaped)
	}()

	wait, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	stopRead := context.AfterFunc(wait, func() { _ = readReady.Close() })
	state, err := decodeReply(json.NewDecoder(readReady))
	stopRead()
	if err != nil {
		if wait.Err() != nil {
			_ = command.Process.Kill()
			<-reaped
			return 0, fmt.Errorf("wait for background readiness: %w", wait.Err())
		}
		return 0, fmt.Errorf("background startup: %w", err)
	}
	if state.PID != command.Process.Pid || state.Mode != background || state.Token == "" {
		_ = command.Process.Kill()
		<-reaped
		return 0, errors.New("invalid readiness response from background process")
	}
	return state.PID, nil
}

func (app *App) RunBackground(ctx context.Context, readiness *os.File) error {
	defer readiness.Close()
	cfg, err := settings.NewStore(app.home).Read()
	if err != nil {
		_ = writeReply(readiness, failure(err))
		return err
	}
	listeners, err := bindHTTP(cfg.Server.Port)
	if err != nil {
		err = bindError(err, cfg.Server.Port)
		_ = writeReply(readiness, failure(err))
		return err
	}
	announced := false
	detached := *app
	detached.detached = true
	err = detached.serve(ctx, background, listeners, nil, func(state State) error {
		announced = true
		err := writeReply(readiness, reply{State: state})
		_ = readiness.Close()
		return err
	})
	if err != nil && !announced {
		_ = writeReply(readiness, failure(err))
	}
	return err
}

func (app *App) State(ctx context.Context) (State, error) {
	ctx, cancel := context.WithTimeout(ctx, controlTimeout)
	defer cancel()
	conn, pid, err := app.connectRuntime(ctx)
	if errors.Is(err, errNoServer) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	defer conn.Close()
	return readState(ctx, conn, pid)
}

func (app *App) ApplySettings(ctx context.Context, state State, fingerprint string, retainPort bool) (State, error) {
	if state.ProxyFingerprint == "" {
		return State{}, ErrIncompatible
	}
	action := "apply_settings"
	if retainPort {
		action = "apply_settings_retain_port"
	}
	return app.sendRuntimeCommand(ctx, state, request{Action: action, Fingerprint: fingerprint})
}

func (app *App) sendRuntimeCommand(ctx context.Context, state State, req request) (State, error) {
	ctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	conn, pid, err := app.connectRuntime(ctx)
	if err != nil {
		return State{}, err
	}
	defer conn.Close()
	if state.PID != pid {
		return State{}, errors.New("The reverse proxy changed before settings could be applied.")
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	req.Version, req.Token = version, state.Token
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return State{}, err
	}
	answer, err := decodeReply(json.NewDecoder(conn))
	if err == nil && (answer.PID != pid || answer.Token != state.Token) {
		return State{}, errors.New("settings response does not match the requested process")
	}
	return answer, err
}

func readState(ctx context.Context, conn *net.UnixConn, pid int) (State, error) {
	answer, err := readOnlyState(ctx, conn, false, false)
	if err != nil {
		return State{}, err
	}
	if answer.State.PID != pid {
		return State{}, errors.New("control response PID does not match the running process")
	}
	return answer.State, nil
}

func readOnlyState(ctx context.Context, conn *net.UnixConn, menu, history bool) (reply, error) {
	interrupt := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer interrupt()
	err := json.NewEncoder(conn).Encode(request{Version: version, Action: "state", Menu: menu, History: history})
	var answer reply
	if err == nil {
		answer, err = decodeControlReply(json.NewDecoder(conn))
	}
	if err != nil && ctx.Err() != nil {
		return reply{}, ctx.Err()
	}
	return answer, err
}

func (app *App) Stop(ctx context.Context) (int, error) {
	state, err := app.State(ctx)
	if err != nil {
		return 0, err
	}
	if state.PID == 0 || state.Mode != background {
		return 0, ErrNoBackground
	}
	return app.stopInstance(ctx, state, nil)
}

func (app *App) stopInstance(ctx context.Context, state State, exited func(context.Context) error) (int, error) {
	wait, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	conn, pid, err := app.connectRuntime(wait)
	if errors.Is(err, errNoServer) || (err == nil && pid != state.PID) {
		if conn != nil {
			conn.Close()
		}
		return 0, ErrNoBackground
	}
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	if exited == nil {
		var release func()
		exited, release, err = subscribeExit(pid)
		if err != nil {
			return 0, fmt.Errorf("watch background process %d: %w", pid, err)
		}
		defer release()
	}

	if err := json.NewEncoder(conn).Encode(request{Version: version, Action: "stop", Token: state.Token}); err != nil {
		return 0, err
	}
	ack, err := decodeReply(json.NewDecoder(conn))
	if err != nil {
		return 0, err
	}
	if ack.PID != pid || ack.Token != state.Token {
		return 0, errors.New("shutdown response does not match the requested process")
	}
	if err := exited(wait); err != nil {
		return 0, fmt.Errorf("wait for background process %d to exit: %w", pid, err)
	}
	return pid, nil
}

func bindHTTP(port int) ([]net.Listener, error) {
	number := strconv.Itoa(port)
	ip4, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", number))
	if err != nil {
		return nil, err
	}
	ip6, err := net.Listen("tcp6", net.JoinHostPort("::1", number))
	if err != nil {
		ip4.Close()
		return nil, err
	}
	return []net.Listener{ip4, ip6}, nil
}

func closeListeners(listeners []net.Listener) {
	for _, listener := range listeners {
		_ = listener.Close()
	}
}

func bindError(err error, port int) error {
	switch {
	case errors.Is(err, unix.EACCES) || errors.Is(err, unix.EPERM):
		return fmt.Errorf("open loopback port %d: %w", port, errBindPermission)
	case errors.Is(err, unix.EADDRINUSE):
		return fmt.Errorf("loopback port %d: %w", port, ErrPortOccupied)
	default:
		return fmt.Errorf("open loopback port %d: %w", port, err)
	}
}

func connectionRefused(err error) bool { return errors.Is(err, unix.ECONNREFUSED) }
