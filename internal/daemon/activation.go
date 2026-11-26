package daemon

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/crmin/kuri/internal/launchd"
	"github.com/crmin/kuri/internal/settings"
	"golang.org/x/sys/unix"
)

type activationRequest struct {
	mode    string
	conn    *net.UnixConn
	handoff bool
}

func (app *App) RunActivated(ctx context.Context, sockets launchd.Sockets, eager bool) error {
	defer sockets.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// RunAtLoad also leaves the sockets registered for later starts. On
	// those starts an explicit request already queued by launchd takes priority.
	store := settings.NewStore(app.home)
	change, changeErr := app.portChange()
	port := listenersPort(sockets.HTTP)
	var suppressed bool
	if changeErr != nil {
		return changeErr
	}
	if port == change.Port && change.ID != "" {
		suppressed, changeErr = change.Pending(app.home)
		if changeErr != nil {
			return changeErr
		}
	}
	allowAutomatic := func() bool {
		if suppressed {
			return false
		}
		allowed, err := store.AutoStart(ctx)
		return err == nil && allowed
	}
	eager = eager && !controlRequestQueued(sockets.Control) && allowAutomatic()
	claims := make(chan activationRequest)
	chosen := make(chan struct{})
	info := reply{State: State{Port: port}}
	if suppressed {
		info.PortChange = change.ID
	}
	go acceptActivation(ctx, sockets.Control, claims, chosen, allowAutomatic, info)

	var selected activationRequest
	listeners := sockets.HTTP
	if !eager {
		httpConnected := make(chan struct{}, 1)
		listeners = make([]net.Listener, 0, len(sockets.HTTP))
		for _, listener := range sockets.HTTP {
			first := prepareFirstConnection(listener, httpConnected, func() bool {
				select {
				case <-chosen:
					return true
				default:
					return allowAutomatic()
				}
			})
			listeners = append(listeners, first)
		}
		defer func() {
			cancel()
			for _, listener := range listeners {
				_ = listener.Close()
			}
		}()
		idle := time.NewTimer(controlTimeout)
		defer idle.Stop()
		select {
		case <-ctx.Done():
			return nil
		case <-idle.C:
			return nil
		case <-httpConnected:
			selected.mode = background
		case selected = <-claims:
		}
	} else {
		selected.mode = background
	}
	close(chosen)

	activated := *app
	activated.activated = true
	if suppressed {
		recovery, err := store.Recovery(change.ID)
		if err != nil {
			if selected.conn != nil {
				_ = writeReply(selected.conn, failure(err))
				_ = selected.conn.Close()
			}
			return err
		}
		activated.recovery = recovery
	}
	if selected.conn == nil {
		return activated.serve(ctx, selected.mode, listeners, nil, nil)
	}
	defer selected.conn.Close()
	return activated.serveActivation(ctx, cancel, listeners, selected)
}

func controlRequestQueued(listener *net.UnixListener) bool {
	raw, err := listener.SyscallConn()
	if err != nil {
		return false
	}
	queued := false
	_ = raw.Control(func(fd uintptr) {
		events := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		if n, err := unix.Poll(events, 0); err == nil && n > 0 {
			queued = events[0].Revents&unix.POLLIN != 0
		}
	})
	return queued
}

func listenersPort(listeners []net.Listener) int {
	if len(listeners) != 2 {
		return 0
	}
	if addr, ok := listeners[0].Addr().(*net.TCPAddr); ok {
		return addr.Port
	}
	return 0
}

func acceptActivation(ctx context.Context, listener *net.UnixListener, claims chan<- activationRequest, chosen <-chan struct{}, allowAutomatic func() bool, info reply) {
	for {
		conn, err := listener.AcceptUnix()
		if err != nil {
			return
		}
		go func() {
			_ = conn.SetDeadline(time.Now().Add(controlTimeout))
			req, err := readRequest(conn)
			if err == nil && req.Action == "activation_info" {
				_ = writeReply(conn, info)
				_ = conn.Close()
				return
			}
			if err == nil && req.Action != foreground && req.Action != background {
				err = errors.New("invalid activation mode")
			}
			if err == nil && req.PortChange != "" && (req.Action != foreground || req.PortChange != info.PortChange) {
				err = errors.New("The foreground port change is no longer active.")
			}
			if err == nil && req.Automatic && !allowAutomatic() {
				err = ErrAutoStartDisabled
			}
			if err != nil {
				_ = writeReply(conn, failure(err))
				_ = conn.Close()
				return
			}
			select {
			case claims <- activationRequest{mode: req.Action, conn: conn, handoff: req.ForegroundHandoff}:
				// The server now owns the startup connection.
			case <-chosen:
				_ = writeReply(conn, failure(ErrAlreadyRunning))
				_ = conn.Close()
			case <-ctx.Done():
				_ = conn.Close()
			}
		}()
	}
}

func (app *App) serveActivation(ctx context.Context, cancel context.CancelFunc, listeners []net.Listener, selected activationRequest) error {
	conn := selected.conn
	var mirror io.Writer
	if selected.mode == foreground {
		mirror = conn
		if selected.handoff {
			_, owner, _ := peer(conn)
			if owner > 0 {
				app.handoff = &foregroundHandoff{conn: conn, owner: owner}
			}
		}
	}
	_ = conn.SetDeadline(time.Now().Add(startTimeout))
	acknowledged := false
	err := app.serve(ctx, selected.mode, listeners, mirror, func(state State) error {
		if err := writeReply(conn, reply{State: state}); err != nil {
			return err
		}
		acknowledged = true
		if selected.mode == background {
			return conn.Close()
		}
		_ = conn.SetDeadline(time.Time{})
		if app.handoff != nil {
			go app.handoff.read(cancel)
		} else {
			go func() {
				_, _ = io.Copy(io.Discard, conn)
				cancel()
			}()
		}
		return nil
	})
	if err != nil && !acknowledged {
		_ = writeReply(conn, failure(err))
	}
	return err
}

type accepted struct {
	conn net.Conn
	err  error
}

type firstListener struct {
	net.Listener
	first chan accepted
	once  sync.Once
}

func prepareFirstConnection(listener net.Listener, connected chan<- struct{}, allow func() bool) *firstListener {
	first := &firstListener{Listener: listener, first: make(chan accepted, 1)}
	go func() {
		for {
			conn, err := listener.Accept()
			if err == nil && !allow() {
				_ = conn.Close()
				continue
			}
			first.first <- accepted{conn, err}
			if err == nil {
				select {
				case connected <- struct{}{}:
				default:
				}
			}
			return
		}
	}()
	return first
}

func (l *firstListener) Accept() (net.Conn, error) {
	var connection accepted
	first := false
	l.once.Do(func() {
		first = true
		connection = <-l.first
	})
	if first {
		return connection.conn, connection.err
	}
	return l.Listener.Accept()
}

func (l *firstListener) Close() error {
	err := l.Listener.Close()
	l.once.Do(func() {
		first := <-l.first
		if first.conn != nil {
			_ = first.conn.Close()
		}
	})
	return err
}
