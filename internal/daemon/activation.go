package daemon

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/crmin/kuri/internal/launchd"
)

type activationRequest struct {
	mode string
	conn *net.UnixConn
}

func (app *App) RunActivated(ctx context.Context, sockets launchd.Sockets, eager bool) error {
	defer sockets.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	claims := make(chan activationRequest)
	chosen := make(chan struct{})
	go acceptActivation(ctx, sockets.Control, claims, chosen)

	var selected activationRequest
	listeners := sockets.HTTP
	if !eager {
		httpConnected := make(chan struct{}, 1)
		listeners = make([]net.Listener, 0, len(sockets.HTTP))
		for _, listener := range sockets.HTTP {
			first := prepareFirstConnection(listener, httpConnected)
			defer first.Close()
			listeners = append(listeners, first)
		}
		select {
		case <-ctx.Done():
			close(chosen)
			return nil
		case <-httpConnected:
			selected.mode = background
		case selected = <-claims:
		}
	} else {
		selected.mode = background
	}
	close(chosen)

	if selected.conn == nil {
		return app.serve(ctx, selected.mode, listeners, nil, nil)
	}
	defer selected.conn.Close()
	return app.serveActivation(ctx, cancel, listeners, selected)
}

func acceptActivation(ctx context.Context, listener *net.UnixListener, claims chan<- activationRequest, chosen <-chan struct{}) {
	for {
		conn, err := listener.AcceptUnix()
		if err != nil {
			return
		}
		go func() {
			_ = conn.SetDeadline(time.Now().Add(controlTimeout))
			req, err := readRequest(conn)
			if err == nil && req.Action != foreground && req.Action != background {
				err = errors.New("invalid activation mode")
			}
			if err != nil {
				_ = writeReply(conn, failure(err))
				_ = conn.Close()
				return
			}
			select {
			case claims <- activationRequest{mode: req.Action, conn: conn}:
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
		go func() {
			_, _ = io.Copy(io.Discard, conn)
			cancel()
		}()
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

func prepareFirstConnection(listener net.Listener, connected chan<- struct{}) *firstListener {
	first := &firstListener{Listener: listener, first: make(chan accepted, 1)}
	go func() {
		conn, err := listener.Accept()
		first.first <- accepted{conn, err}
		if err == nil {
			select {
			case connected <- struct{}{}:
			default:
			}
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
