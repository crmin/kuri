package daemon

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/crmin/kuri/internal/accesslog"
	"github.com/crmin/kuri/internal/forward"
	"github.com/crmin/kuri/internal/menuview"
	"github.com/crmin/kuri/internal/proxy"
	"github.com/crmin/kuri/internal/route"
	"github.com/crmin/kuri/internal/settings"
)

type proxyRuntime struct {
	mu               sync.Mutex
	applyMu          sync.Mutex
	closed           bool
	workers          sync.WaitGroup
	home             string
	retained         bool
	logger           *accesslog.Writer
	logError         func(error)
	current          *proxyBackend
	config           settings.Config
	handler          http.Handler
	routes           route.Contexts
	replaceListeners func([]net.Listener) error
}

type proxyBackend struct {
	kube     *forward.Kubernetes
	manager  *forward.Manager
	requests requestGroup
	scope    context.Context
	cancel   context.CancelCauseFunc
}

func newProxyRuntime(home string, retained bool, cfg settings.Config, logger *accesslog.Writer, logError func(error)) (*proxyRuntime, error) {
	r := &proxyRuntime{home: home, retained: retained, logger: logger, logError: logError}
	backend, err := r.openBackend(cfg, nil)
	if err != nil {
		return nil, err
	}
	r.current = backend
	r.install(cfg)
	return r, nil
}

func (r *proxyRuntime) openBackend(cfg settings.Config, previous *proxyBackend) (*proxyBackend, error) {
	kube, _, err := forward.LoadKubernetes(cfg.Kubernetes.ConfigPath)
	if err != nil {
		return nil, err
	}
	if err := kube.StartCredentials(settings.NewStore(r.home).Directory(), r.retained); err != nil {
		return nil, err
	}
	var manager *forward.Manager
	if previous == nil {
		manager = forward.NewManagerWithPolicy(kube, cfg.PortForward)
	} else {
		manager = previous.manager.NextBackend(kube, cfg.PortForward)
	}
	scope, cancel := context.WithCancelCause(context.Background())
	return &proxyBackend{kube: kube, manager: manager, scope: scope, cancel: cancel}, nil
}

func (r *proxyRuntime) install(cfg settings.Config) {
	routes := r.current.kube.Routes(cfg.Server, cfg.Kubernetes.ExcludedContexts)
	r.current.manager.UpdatePolicy(cfg.PortForward, routes.Enabled)
	r.config = cfg
	r.routes = routes
	r.handler = proxy.New(routes, r.current.manager, r.logger, r.logError)
}

func (r *proxyRuntime) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	backend, handler := r.current, r.handler
	if r.closed || !backend.requests.enter() {
		r.mu.Unlock()
		http.Error(w, "The proxy is shutting down.", http.StatusServiceUnavailable)
		return
	}
	r.mu.Unlock()
	defer backend.requests.leave()
	ctx, cancel := context.WithCancelCause(req.Context())
	defer cancel(nil)
	stop := context.AfterFunc(backend.scope, func() { cancel(context.Cause(backend.scope)) })
	defer stop()
	handler.ServeHTTP(w, req.WithContext(ctx))
}

func (r *proxyRuntime) apply(ctx context.Context, fingerprint string, retainPort bool) error {
	r.applyMu.Lock()
	defer r.applyMu.Unlock()

	store := settings.NewStore(r.home)
	cfg, err := store.Read()
	if err != nil {
		return err
	}
	if cfg.Fingerprint() != fingerprint {
		return ErrSettingsChanged
	}
	r.mu.Lock()
	previous, original, closed := r.current, r.config, r.closed
	r.mu.Unlock()
	if closed {
		return errors.New("The reverse proxy is shutting down.")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if retainPort {
		cfg.Server.Port = original.Server.Port
	}

	var listeners []net.Listener
	defer func() { closeListeners(listeners) }()
	if cfg.Server.Port != original.Server.Port {
		if r.replaceListeners == nil {
			return ErrPortRestartRequired
		}
		listeners, err = bindHTTP(cfg.Server.Port)
		if err != nil {
			return bindError(err, cfg.Server.Port)
		}
	}
	var next *proxyBackend
	defer func() {
		if next != nil {
			if err := next.close(); err != nil {
				r.logError(err)
			}
		}
	}()
	if cfg.Kubernetes.ConfigPath != original.Kubernetes.ConfigPath {
		next, err = r.openBackend(cfg, previous)
		if err != nil {
			return err
		}
	}

	return store.WithSnapshot(ctx, func(latest settings.Config) error {
		if latest.Fingerprint() != fingerprint {
			return ErrSettingsChanged
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		if r.closed {
			return errors.New("The reverse proxy is shutting down.")
		}
		if listeners != nil {
			if err := r.replaceListeners(listeners); err != nil {
				return err
			}
			listeners = nil
		}
		if next != nil {
			r.current, next = next, nil
			previous.manager.UpdatePolicy(original.PortForward, func(string) bool { return false })
			r.workers.Go(func() {
				if err := previous.close(); err != nil {
					r.logError(err)
				}
			})
		}
		r.install(cfg)
		return nil
	})
}

func (r *proxyRuntime) state(state State) State {
	r.mu.Lock()
	defer r.mu.Unlock()
	state.ProxyFingerprint = r.config.ProxyFingerprint()
	state.Port = r.config.Server.Port
	cfg := r.config
	state.AppliedSettings = &cfg
	return state
}

func (r *proxyRuntime) snapshot() []forward.Row {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.withHosts(r.current.manager.Snapshot())
}

func (r *proxyRuntime) menu(state State, history bool) (menuview.State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var count int
	var rows []forward.Row
	if history {
		count, rows = r.current.manager.RecentHistory()
	} else {
		count, rows = r.current.manager.RecentSnapshot(menuview.LegacyRecentLimit)
	}
	state.ProxyFingerprint = r.config.ProxyFingerprint()
	state.Port = r.config.Server.Port
	return makeMenuState(state, count, r.withHosts(rows), true)
}

func (r *proxyRuntime) withHosts(rows []forward.Row) []forward.Row {
	for i := range rows {
		rows[i].MenuLabel, rows[i].MenuHost = r.routes.ForwardAddress(rows[i].Target, rows[i].DefaultPort)
	}
	return rows
}

func (r *proxyRuntime) close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	current := r.current
	r.mu.Unlock()
	err := current.close()
	r.workers.Wait()
	return err
}

func (b *proxyBackend) close() error {
	done := b.requests.seal()
	timer := time.NewTimer(shutdownTimeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		b.cancel(proxy.ErrShutdown)
		<-done
	}
	b.cancel(nil)
	b.manager.Close()
	return b.kube.StopCredentials()
}
