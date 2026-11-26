package daemon

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/crmin/kuri/internal/accesslog"
	"github.com/crmin/kuri/internal/forward"
	"github.com/crmin/kuri/internal/menuview"
	"github.com/crmin/kuri/internal/proxy"
	"github.com/crmin/kuri/internal/settings"
	"golang.org/x/sys/unix"
)

const (
	shutdownTimeout     = 5 * time.Second
	consoleFlushTimeout = time.Second
)

func (app *App) serve(ctx context.Context, mode string, listeners []net.Listener, mirror io.Writer, ready func(State) error) error {
	defer func() {
		for _, listener := range listeners {
			_ = listener.Close()
		}
	}()
	dir := filepath.Join(app.home, ".kuri")
	lock, err := lockRuntime(dir)
	if err != nil {
		return err
	}
	defer lock.Close()

	console := accesslog.NewConsole(mirror)
	logger, err := accesslog.Open(filepath.Join(dir, "access.log"), console)
	if err != nil {
		return fmt.Errorf("open access log: %w", err)
	}
	runtime, control, err := app.prepareRuntime(ctx, listeners, logger)
	if err != nil {
		return err
	}
	defer runtime.close()
	defer control.Close()

	run, stop := context.WithCancel(ctx)
	defer stop()
	http := newHTTPService(runtime)
	if !app.activated {
		runtime.replaceListeners = http.replace
	}

	state := runtime.state(State{PID: os.Getpid(), Mode: mode, Started: time.Now().UTC(), Token: rand.Text(), Activated: app.activated})
	if app.handoff != nil {
		state.ForegroundOwner = app.handoff.owner
	} else if app.localHandoff != nil {
		state.ForegroundOwner = os.Getpid()
	}
	if err := logger.Write(accesslog.Event{Time: state.Started, Event: "startup", PID: state.PID, Mode: mode}); err != nil {
		return err
	}
	if err := http.replace(listeners); err != nil {
		return err
	}
	defer http.close()
	if ready != nil {
		if err := ready(state); err != nil {
			_ = logger.Write(accesslog.Event{Event: "stop", PID: state.PID, Mode: mode, Reason: "startup client disconnected"})
			return err
		}
	}

	console.Start()

	controlDone := make(chan struct{})
	stopped := &stopResult{done: make(chan struct{})}
	go func() {
		defer close(controlDone)
		commands := controlCommands{
			state:    func() State { return runtime.state(state) },
			snapshot: runtime.snapshot,
			menu:     func(history bool) (menuview.State, error) { return runtime.menu(state, history) },
			apply: func(fingerprint string, retainPort bool) error {
				applyCtx, cancel := context.WithTimeout(run, startTimeout)
				defer cancel()
				return runtime.apply(applyCtx, fingerprint, retainPort)
			},
			stop: stop, stopped: stopped,
		}
		if app.handoff != nil {
			commands.preparePortRestart = func(operation string) error { return app.handoff.prepare(run, operation) }
		}
		if app.localHandoff != nil {
			commands.handoffDirect = func(operation string) error {
				change, err := app.portChange()
				if err != nil {
					return err
				}
				if operation == "" || change.ID != operation {
					return errors.New("The foreground port change is no longer active.")
				}
				select {
				case app.localHandoff <- operation:
					return nil
				default:
					return errors.New("The foreground proxy is already switching to launchd.")
				}
			}
		}
		serveRuntimeControl(control, commands)
	}()

	select {
	case <-run.Done():
	case err = <-http.failed:
	}

	stop()
	http.close()
	credentialErr := runtime.close()
	logErr := logger.Write(accesslog.Event{Event: "stop", PID: state.PID, Mode: mode})
	app.flushConsole(console)
	stopped.err = logErr
	close(stopped.done)
	_ = control.Close()
	<-controlDone
	return errors.Join(err, credentialErr, logErr)
}

// Publish the control socket while holding the settings snapshot lock. A
// concurrent save either sees this socket after writing or wins the race and
// makes us rebuild from the newer settings before accepting requests.
func (app *App) prepareRuntime(ctx context.Context, listeners []net.Listener, logger *accesslog.Writer) (*proxyRuntime, *net.UnixListener, error) {
	store := settings.NewStore(app.home)
	for {
		saved, err := store.Read()
		if err != nil {
			return nil, nil, err
		}
		if port := listenersPort(listeners); port != 0 && port != saved.Server.Port {
			return nil, nil, fmt.Errorf("The listener uses port %d. Apply the saved port %d before starting kuri.", port, saved.Server.Port)
		}
		cfg := saved
		if app.recovery != nil && app.recovery.Server.Port == cfg.Server.Port {
			cfg = *app.recovery
			cfg.General = saved.General
		}
		runtime, err := newProxyRuntime(app.home, app.detached, cfg, logger, func(err error) { fmt.Fprintln(app.stderr, err) })
		if err != nil {
			return nil, nil, err
		}
		var control *net.UnixListener
		err = store.WithSnapshot(ctx, func(latest settings.Config) error {
			if latest.Fingerprint() != saved.Fingerprint() {
				return ErrSettingsChanged
			}
			control, err = openRuntimeControl(filepath.Join(store.Directory(), "ctl"))
			return err
		})
		if err == nil {
			return runtime, control, nil
		}
		if closeErr := runtime.close(); closeErr != nil {
			return nil, nil, errors.Join(err, closeErr)
		}
		if !errors.Is(err, ErrSettingsChanged) {
			return nil, nil, err
		}
	}
}

func newHTTPServer(ctx context.Context, handler http.Handler) *http.Server {
	return &http.Server{
		BaseContext:       func(net.Listener) context.Context { return ctx },
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 * 1024,
	}
}

func (app *App) flushConsole(console *accesslog.Console) {
	if console == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), consoleFlushTimeout)
	defer cancel()
	if err := console.Close(ctx); err != nil {
		// A redirected stderr may share the same stalled pipe.
		reported := make(chan struct{})
		go func() {
			fmt.Fprintln(app.stderr, err)
			close(reported)
		}()
		select {
		case <-reported:
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func lockRuntime(directory string) (*os.File, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, fmt.Errorf("create kuri directory: %w", err)
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("the kuri runtime path must be a directory")
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return nil, fmt.Errorf("secure kuri directory: %w", err)
	}
	lock, err := os.OpenFile(filepath.Join(directory, "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open kuri process lock: %w", err)
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lock.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrAlreadyRunning
		}
		return nil, fmt.Errorf("acquire kuri process lock: %w", err)
	}
	return lock, nil
}

func openRuntimeControl(path string) (*net.UnixListener, error) {
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("the kuri control path is not a Unix socket")
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("remove stale kuri control socket: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("check kuri control socket: %w", err)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("open kuri control socket: %w", err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		listener.Close()
		return nil, fmt.Errorf("secure kuri control socket: %w", err)
	}
	return listener, nil
}

type stopResult struct {
	done chan struct{}
	err  error
}

type controlCommands struct {
	state              func() State
	snapshot           func() []forward.Row
	menu               func(bool) (menuview.State, error)
	apply              func(string, bool) error
	preparePortRestart func(string) error
	handoffDirect      func(string) error
	stop               func()
	stopped            *stopResult
}

func serveRuntimeControl(listener *net.UnixListener, commands controlCommands) {
	var clients sync.WaitGroup
	defer clients.Wait()
	for {
		conn, err := listener.AcceptUnix()
		if err != nil {
			return
		}
		clients.Go(func() {
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(controlTimeout))
			req, err := readRequest(conn)
			if err != nil {
				_ = writeReply(conn, failure(err))
				return
			}
			switch req.Action {
			case "state":
				if req.Menu {
					menu, err := commands.menu(req.History)
					if err != nil {
						_ = writeReply(conn, failure(err))
						return
					}
					_ = writeReply(conn, reply{Menu: &menu})
					return
				}
				current := commands.state()
				current.Rows = commands.snapshot()
				_ = writeReply(conn, reply{State: current})
			case "apply_settings", "apply_settings_retain_port":
				state := commands.state()
				if req.Token != state.Token || commands.apply == nil {
					_ = writeReply(conn, failure(ErrIncompatible))
					return
				}
				_ = conn.SetDeadline(time.Now().Add(startTimeout))
				if err := commands.apply(req.Fingerprint, req.Action == "apply_settings_retain_port"); err != nil {
					_ = writeReply(conn, failure(err))
					return
				}
				_ = writeReply(conn, reply{State: commands.state()})
			case "prepare_port_restart":
				state := commands.state()
				if req.Token != state.Token || commands.preparePortRestart == nil {
					_ = writeReply(conn, failure(ErrIncompatible))
					return
				}
				_ = conn.SetDeadline(time.Now().Add(startTimeout))
				if err := commands.preparePortRestart(req.PortChange); err != nil {
					_ = writeReply(conn, failure(err))
					return
				}
				_ = writeReply(conn, reply{State: state})
			case "handoff_direct_foreground":
				state := commands.state()
				if req.Token != state.Token || commands.handoffDirect == nil {
					_ = writeReply(conn, failure(ErrIncompatible))
					return
				}
				if err := commands.handoffDirect(req.PortChange); err != nil {
					_ = writeReply(conn, failure(err))
					return
				}
				_ = writeReply(conn, reply{State: state})
				commands.stop()
			case "stop":
				state := commands.state()
				if state.Mode != background || req.Token != state.Token {
					_ = writeReply(conn, failure(ErrNoBackground))
					return
				}
				_ = conn.SetWriteDeadline(time.Now().Add(startTimeout))
				commands.stop()
				<-commands.stopped.done
				if commands.stopped.err != nil {
					_ = writeReply(conn, failure(commands.stopped.err))
					return
				}
				_ = writeReply(conn, reply{State: state})
			default:
				_ = writeReply(conn, failure(errors.New("unknown control action")))
			}
		})
	}
}

type requestGroup struct {
	mu     sync.Mutex
	active int
	done   chan struct{}
}

func (group *requestGroup) wrap(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !group.enter() {
			http.Error(w, "The proxy is shutting down.", http.StatusServiceUnavailable)
			return
		}
		defer group.leave()
		handler.ServeHTTP(w, r)
	})
}

func (group *requestGroup) enter() bool {
	group.mu.Lock()
	defer group.mu.Unlock()
	if group.done != nil {
		return false
	}
	group.active++
	return true
}

func (group *requestGroup) leave() {
	group.mu.Lock()
	defer group.mu.Unlock()
	group.active--
	if group.active == 0 && group.done != nil {
		close(group.done)
	}
}

func (group *requestGroup) seal() <-chan struct{} {
	group.mu.Lock()
	defer group.mu.Unlock()
	group.done = make(chan struct{})
	if group.active == 0 {
		close(group.done)
	}
	return group.done
}

func drain(server *http.Server, group *requestGroup, cancel context.CancelCauseFunc, timeout time.Duration) {
	deadline, release := context.WithTimeout(context.Background(), timeout)
	defer release()
	_ = server.Shutdown(deadline)
	done := group.seal()
	select {
	case <-done:
	case <-deadline.Done():
		cancel(proxy.ErrShutdown)
		_ = server.Close()
		<-done
	}
}
