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
	"github.com/crmin/kuri/internal/proxy"
	"golang.org/x/sys/unix"
)

const shutdownTimeout = 5 * time.Second

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

	kube, contexts, err := forward.LoadKubernetes(filepath.Join(app.home, ".kube", "config"))
	if err != nil {
		return err
	}
	manager := forward.NewManager(kube)
	defer manager.Close()
	logger, err := accesslog.Open(filepath.Join(dir, "access.log"), mirror)
	if err != nil {
		return fmt.Errorf("open access log: %w", err)
	}
	control, err := openRuntimeControl(filepath.Join(dir, "ctl"))
	if err != nil {
		return err
	}
	defer control.Close()

	run, stop := context.WithCancel(ctx)
	defer stop()
	requests, cancelRequests := context.WithCancelCause(context.Background())
	defer cancelRequests(nil)
	var group requestGroup
	server := &http.Server{
		BaseContext:       func(net.Listener) context.Context { return requests },
		Handler:           group.wrap(proxy.New(contexts, manager, logger, func(err error) { fmt.Fprintln(app.stderr, err) })),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 * 1024,
	}

	state := State{PID: os.Getpid(), Mode: mode, Started: time.Now().UTC(), Token: rand.Text()}
	if err := logger.Write(accesslog.Event{Time: state.Started, Event: "startup", PID: state.PID, Mode: mode}); err != nil {
		return err
	}
	if ready != nil {
		if err := ready(state); err != nil {
			_ = logger.Write(accesslog.Event{Event: "stop", PID: state.PID, Mode: mode, Reason: "startup client disconnected"})
			return err
		}
	}

	controlDone := make(chan struct{})
	stopped := &stopResult{done: make(chan struct{})}
	go func() {
		defer close(controlDone)
		serveRuntimeControl(control, state, manager.Snapshot, stop, stopped)
	}()

	var workers sync.WaitGroup
	exit := make(chan error, len(listeners))
	for _, listener := range listeners {
		workers.Go(func() {
			exit <- server.Serve(listener)
		})
	}
	select {
	case <-run.Done():
	case err = <-exit:
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
	}

	drain(server, &group, cancelRequests, shutdownTimeout)
	workers.Wait()
	manager.Close()
	logErr := logger.Write(accesslog.Event{Event: "stop", PID: state.PID, Mode: mode})
	stopped.err = logErr
	close(stopped.done)
	_ = control.Close()
	<-controlDone
	return errors.Join(err, logErr)
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

func serveRuntimeControl(listener *net.UnixListener, state State, snapshot func() []forward.Row, stop func(), stopped *stopResult) {
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
				current := state
				current.Rows = snapshot()
				_ = writeReply(conn, reply{State: current})
			case "stop":
				if state.Mode != background || req.Token != state.Token {
					_ = writeReply(conn, failure(ErrNoBackground))
					return
				}
				_ = conn.SetWriteDeadline(time.Now().Add(startTimeout))
				stop()
				<-stopped.done
				if stopped.err != nil {
					_ = writeReply(conn, failure(stopped.err))
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
		group.mu.Lock()
		if group.done != nil {
			group.mu.Unlock()
			http.Error(w, "The proxy is shutting down.", http.StatusServiceUnavailable)
			return
		}
		group.active++
		group.mu.Unlock()
		defer func() {
			group.mu.Lock()
			group.active--
			if group.active == 0 && group.done != nil {
				close(group.done)
			}
			group.mu.Unlock()
		}()
		handler.ServeHTTP(w, r)
	})
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
