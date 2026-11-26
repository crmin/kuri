package forward

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/crmin/kuri/internal/route"
	"github.com/crmin/kuri/internal/settings"
)

// UpdatePolicy makes new acquisitions follow the saved range and keep-alive
// rules. Requests already using a retired forward can finish on its lease.
func (m *Manager) UpdatePolicy(policy settings.PortForward, enabled func(string) bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.minPort, m.maxPort = policy.MinPort, policy.MaxPort+1
	m.keepAlive = policy.KeepAlive
	m.enabled = enabled
	m.lifetime, m.trigger = 0, ""
	if policy.KeepAlive {
		m.lifetime, m.trigger = policy.Duration(), policy.Trigger
	}
	now := time.Now()
	for _, e := range m.entries {
		if !m.keepAlive || (enabled != nil && !enabled(e.target.Context)) {
			m.retireLocked(e, errExpired)
			continue
		}
		if e.lease != nil && !m.containsPortLocked(e.lease.LocalPort) {
			m.retireLocked(e, errExpired)
			continue
		}
		if !e.opened.IsZero() {
			since := e.opened
			if m.trigger == settings.LastRequest && e.lastAccess.After(since) {
				since = e.lastAccess
			}
			e.deadline = since.Add(m.lifetime)
			if !now.Before(e.deadline) {
				m.retireLocked(e, errExpired)
				continue
			}
			e.wakeLocked()
		}
	}
	m.ports.mu.Lock()
	m.ports.signal()
	m.ports.mu.Unlock()
}

func (m *Manager) containsPortLocked(port int) bool {
	return port >= m.minPort && port < m.maxPort
}

// acquireEphemeral opens a dedicated tunnel for one request. The caller
// releases it after the response body or upgraded connection has finished.
func (m *Manager) acquireEphemeral(ctx context.Context, req route.Request) (*Lease, func(), error) {
	lifetime, cancel := context.WithCancel(m.ctx)
	stopRequest := context.AfterFunc(ctx, cancel)
	closeRequest := func() {
		stopRequest()
		cancel()
		m.workers.Done()
	}
	start, stop := context.WithTimeout(lifetime, startupTimeout)
	defer stop()

	e := &entry{owner: m, host: req.Host, ephemeral: true}
	port, _, err := m.reserve(start, e, nil)
	if err != nil {
		closeRequest()
		return nil, nil, err
	}
	destinations, err := m.backend.Resolve(start, req.Target, "")
	if err != nil || len(destinations) == 0 {
		m.unreserve(e, port)
		closeRequest()
		if err == nil {
			err = errors.New("service has no eligible pod")
		}
		return nil, nil, err
	}
	lease, err := m.open(start, e, destinations, false, port)
	if err != nil {
		closeRequest()
		return nil, nil, err
	}
	if err := lifetime.Err(); err != nil {
		m.release(e, lease)
		closeRequest()
		return nil, nil, err
	}
	e.target, e.lease, e.lastAccess = lease.Destination.Target, lease, time.Now()
	m.mu.Lock()
	m.ephemeral[e] = struct{}{}
	m.recordLocked(e)
	m.mu.Unlock()

	release := sync.OnceFunc(func() {
		m.mu.Lock()
		delete(m.ephemeral, e)
		m.mu.Unlock()
		m.release(e, lease)
		closeRequest()
	})
	context.AfterFunc(lifetime, release)
	return lease, release, nil
}
