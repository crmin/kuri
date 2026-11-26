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
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

type App struct {
	home, activation string
	detached         bool
	stdout, stderr   io.Writer
}

func New(home, activation string, stdout, stderr io.Writer) *App {
	return &App{home: home, activation: activation, stdout: stdout, stderr: stderr}
}

func (app *App) RunForeground(ctx context.Context) error {
	state, err := app.State(ctx)
	if err != nil {
		return err
	}
	if state.PID != 0 {
		return ErrAlreadyRunning
	}
	listeners, err := bindHTTP()
	if err == nil {
		return app.serve(ctx, foreground, listeners, app.stdout, nil)
	}
	if errors.Is(err, unix.EACCES) || errors.Is(err, unix.EPERM) || errors.Is(err, unix.EADDRINUSE) {
		if app.hasActivationSocket() {
			_, err := app.activate(ctx, foreground)
			return err
		}
	}
	return bindError(err)
}

func (app *App) Start(ctx context.Context) (int, error) {
	state, err := app.State(ctx)
	if err != nil {
		return 0, err
	}
	if state.PID != 0 {
		return 0, ErrAlreadyRunning
	}
	if app.hasActivationSocket() {
		return app.activate(ctx, background)
	}
	return app.startDetached(ctx)
}

func (app *App) hasActivationSocket() bool {
	info, err := os.Lstat(app.activation)
	return err == nil && info.Mode()&os.ModeSocket != 0 &&
		int(info.Sys().(*syscall.Stat_t).Uid) == os.Geteuid() && info.Mode().Perm()&0077 == 0
}

func (app *App) activate(ctx context.Context, mode string) (int, error) {
	startCtx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	// An inherited socket's peer credentials can identify launchd, which
	// created the listening socket. Its path is protected by installation.
	client, err := (&net.Dialer{}).DialContext(startCtx, "unix", app.activation)
	if err != nil {
		return 0, fmt.Errorf("connect to the launchd activation socket: %w", err)
	}
	conn := client.(*net.UnixConn)
	defer conn.Close()
	if deadline, ok := startCtx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	unblock := context.AfterFunc(startCtx, func() { _ = conn.SetDeadline(time.Now()) })
	if err := sendRequest(conn, mode); err != nil {
		unblock()
		return 0, err
	}
	reader := bufio.NewReader(conn)
	frame, err := reader.ReadBytes('\n')
	unblock()
	if err != nil {
		return 0, fmt.Errorf("read launchd readiness: %w", err)
	}
	state, err := decodeReply(json.NewDecoder(bytes.NewReader(frame)))
	if err != nil {
		return 0, fmt.Errorf("start the launchd instance: %w", err)
	}
	if state.PID <= 0 || state.Token == "" || state.Mode != mode {
		return 0, errors.New("invalid readiness response from launchd instance")
	}
	if mode == background {
		return state.PID, nil
	}

	_ = conn.SetDeadline(time.Time{})
	requestStop := context.AfterFunc(ctx, func() {
		_ = conn.CloseWrite()
		_ = conn.SetReadDeadline(time.Now().Add(startTimeout))
	})
	defer requestStop()
	_, err = io.Copy(app.stdout, reader)
	return state.PID, err
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
	listeners, err := bindHTTP()
	if err != nil {
		err = bindError(err)
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
	if err := sendRequest(conn, "state"); err != nil {
		return State{}, err
	}
	state, err := decodeReply(json.NewDecoder(conn))
	if err != nil {
		return State{}, err
	}
	if state.PID != pid {
		return State{}, errors.New("control response PID does not match the running process")
	}
	return state, nil
}

func (app *App) Stop(ctx context.Context) (int, error) {
	state, err := app.State(ctx)
	if err != nil {
		return 0, err
	}
	if state.PID == 0 || state.Mode != background {
		return 0, ErrNoBackground
	}
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
	exited, release, err := subscribeExit(pid)
	if err != nil {
		return 0, fmt.Errorf("watch background process %d: %w", pid, err)
	}
	defer release()

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

func bindHTTP() ([]net.Listener, error) {
	ip4, err := net.Listen("tcp4", "127.0.0.1:80")
	if err != nil {
		return nil, err
	}
	ip6, err := net.Listen("tcp6", "[::1]:80")
	if err != nil {
		ip4.Close()
		return nil, err
	}
	return []net.Listener{ip4, ip6}, nil
}

func bindError(err error) error {
	switch {
	case errors.Is(err, unix.EACCES) || errors.Is(err, unix.EPERM):
		return errors.New("Permission denied on loopback port 80. Install the kuri LaunchDaemon to run as a normal user.")
	case errors.Is(err, unix.EADDRINUSE):
		return errors.New("Loopback port 80 is already in use.")
	default:
		return fmt.Errorf("open loopback port 80: %w", err)
	}
}

func connectionRefused(err error) bool { return errors.Is(err, unix.ECONNREFUSED) }
