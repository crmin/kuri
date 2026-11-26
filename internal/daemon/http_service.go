package daemon

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
)

type httpService struct {
	mu      sync.Mutex
	current *httpCycle
	handler http.Handler
	closed  bool
	failed  chan error
	workers sync.WaitGroup
}

type httpCycle struct {
	server    *http.Server
	listeners []net.Listener
	requests  *requestGroup
	cancel    context.CancelCauseFunc
}

func newHTTPService(handler http.Handler) *httpService {
	return &httpService{handler: handler, failed: make(chan error, 1)}
}

func (s *httpService) replace(listeners []net.Listener) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("The reverse proxy is shutting down.")
	}
	s.retireLocked()
	requests, cancel := context.WithCancelCause(context.Background())
	group := &requestGroup{}
	cycle := &httpCycle{
		listeners: listeners,
		requests:  group,
		cancel:    cancel,
		server:    newHTTPServer(requests, group.wrap(s.handler)),
	}
	s.current = cycle
	for _, listener := range listeners {
		s.workers.Go(func() {
			err := cycle.server.Serve(listener)
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.current != cycle || errors.Is(err, http.ErrServerClosed) {
				return
			}
			select {
			case s.failed <- err:
			default:
			}
		})
	}
	return nil
}

func (s *httpService) retireLocked() {
	cycle := s.current
	if cycle == nil {
		return
	}
	s.current = nil
	for _, listener := range cycle.listeners {
		_ = listener.Close()
	}
	s.workers.Go(func() {
		drain(cycle.server, cycle.requests, cycle.cancel, shutdownTimeout)
		cycle.cancel(nil)
	})
}

func (s *httpService) close() {
	s.mu.Lock()
	s.closed = true
	s.retireLocked()
	s.mu.Unlock()
	s.workers.Wait()
}
