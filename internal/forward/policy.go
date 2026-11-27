package forward

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/crmin/kuri/internal/route"
	"github.com/crmin/kuri/internal/settings"
)

type healingPolicy struct {
	ctx      context.Context
	cancel   context.CancelFunc
	interval time.Duration
	changed  chan struct{}
}

type probeSchedule struct {
	policy healingPolicy
	ticker *time.Ticker
	ticks  <-chan time.Time
}

func (s *probeSchedule) stop() {
	if s.ticker != nil {
		s.ticker.Stop()
		s.ticker, s.ticks = nil, nil
	}
}

func (s *probeSchedule) update(policy healingPolicy) {
	if s.policy.changed == policy.changed {
		return
	}
	s.stop()
	s.policy = policy
	if policy.ctx != nil && policy.interval > 0 {
		s.ticker = time.NewTicker(policy.interval)
		s.ticks = s.ticker.C
	}
}

// UpdatePolicy makes new acquisitions follow the saved range and keep-alive
// rules. Requests already using a retired forward can finish on its lease.
func (m *Manager) UpdatePolicy(policy settings.PortForward, enabled func(string) bool, routes ...route.Contexts) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.minPort, m.maxPort = policy.MinPort, policy.MaxPort+1
	m.updateHealingLocked(policy)
	m.keepAlive = policy.KeepAlive
	m.enabled = enabled
	if len(routes) > 0 {
		m.routes = &routes[0]
	}
	m.lifetime, m.trigger = 0, ""
	if policy.KeepAlive {
		m.lifetime, m.trigger = policy.Duration(), policy.Trigger
	}
	now := time.Now()
	for _, e := range m.entries {
		if !m.keepAlive || (enabled != nil && !enabled(e.key.Context)) {
			m.retireLocked(e, errExpired)
			continue
		}
		if e.lease != nil && !m.containsPortLocked(e.lease.LocalPort) {
			m.retireLocked(e, errExpired)
			continue
		}
		if e.lease != nil && !m.refreshProbeHostLocked(e.lease) {
			m.retireLocked(e, errRouteChanged)
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
	for e := range m.ephemeral {
		m.refreshProbeHostLocked(e.lease)
	}
	m.ports.mu.Lock()
	m.ports.signal()
	m.ports.mu.Unlock()
}

func (m *Manager) refreshProbeHostLocked(lease *Lease) bool {
	if m.routes == nil {
		return true
	}
	dest := lease.Destination
	isDefault := route.ResolvedSelector(dest.Target, dest.PortName) == dest.DefaultPort
	host := lease.probeAuthority()
	if m.routes.Matches(host, dest.Target, dest.PortName, isDefault) {
		return true
	}
	_, host = m.routes.ForwardAddress(dest.Target, dest.PortName, isDefault)
	if host == "" {
		return false
	}
	lease.probeHost.Store(&host)
	return true
}

func (m *Manager) updateHealingLocked(policy settings.PortForward) {
	healing := &m.healing
	interval, enabled := policy.CheckInterval(), policy.AutoHealingEnabled()
	if enabled == (healing.ctx != nil) && interval == healing.interval {
		return
	}
	if enabled && healing.ctx == nil {
		healing.ctx, healing.cancel = context.WithCancel(m.ctx)
	} else if !enabled && healing.ctx != nil {
		healing.cancel()
		healing.ctx, healing.cancel = nil, nil
	}
	healing.interval = interval
	close(healing.changed)
	healing.changed = make(chan struct{})
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

	e := &entry{owner: m, host: req.Host, ephemeral: true, ctx: lifetime}
	port, _, err := m.reserve(start, e, nil)
	if err != nil {
		closeRequest()
		return nil, nil, err
	}
	destinations, err := m.backend.Resolve(start, req.Selector, "")
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
	e.key = route.ResolvedSelector(lease.Destination.Target, lease.Destination.PortName)
	e.lease, e.lastAccess = lease, time.Now()
	m.mu.Lock()
	m.refreshProbeHostLocked(lease)
	m.ephemeral[e] = struct{}{}
	m.updateRecentDefaultLocked(e.key.ServiceTarget(), lease.Destination.DefaultPort)
	m.recordLocked(e)
	m.workers.Go(func() { m.watch(e, lease) })
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
