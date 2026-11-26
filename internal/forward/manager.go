package forward

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/crmin/kuri/internal/route"
)

const (
	coldLifetime   = 60 * time.Minute
	warmLifetime   = 60 * time.Second
	probeInterval  = 10 * time.Second
	retryInterval  = 2 * time.Second
	probeTimeout   = 3 * time.Second
	startupTimeout = 20 * time.Second
)

var (
	ErrPortInUse = errors.New("local port is in use")
	errClosed    = errors.New("port forwarding is shutting down")
	errExpired   = errors.New("port forward expired")
	errEvicted   = errors.New("port forward was evicted to free a local port")
)

type Destination struct {
	route.Target
	PortName    string
	DefaultPort int32
	Pod         string
	PodUID      string
	TargetPort  int32
}

type Tunnel struct {
	Done <-chan struct{}
	// Close waits for the local listener to release its port.
	Close func()
}

type Backend interface {
	Resolve(context.Context, route.Target, string) ([]Destination, error)
	Open(context.Context, Destination, int) (Tunnel, error)
}

type Row struct {
	route.Target
	PortName  string    `json:"port_name"`
	LocalPort int       `json:"local_port"`
	Connected time.Time `json:"connected"`
}

type Manager struct {
	backend          Backend
	ctx              context.Context
	cancel           context.CancelFunc
	workers          sync.WaitGroup
	minPort, maxPort int
	checkPort        func(int) error
	probe            func(context.Context, *Lease) error

	mu           sync.Mutex
	entries      map[route.Target]*entry
	defaults     map[route.Target]int32
	lookups      map[route.Target]*lookup
	reservations map[int]*entry
}

type lookup struct {
	done  chan struct{}
	entry *entry
	err   error
}

type entry struct {
	target   route.Target
	host     string
	ctx      context.Context
	cancel   context.CancelFunc
	finished chan struct{}
	wake     chan struct{}

	// Protected by Manager.mu.
	changed  chan struct{}
	deadline time.Time
	lease    *Lease
	err      error
}

func NewManager(backend Backend) *Manager {
	return newManager(backend, 30000, 40000, checkLocalPort, probeHTTP)
}

func newManager(backend Backend, minPort, maxPort int, check func(int) error, probe func(context.Context, *Lease) error) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		backend: backend, ctx: ctx, cancel: cancel, minPort: minPort, maxPort: maxPort,
		checkPort: check, probe: probe,
		entries: make(map[route.Target]*entry), defaults: make(map[route.Target]int32),
		lookups: make(map[route.Target]*lookup), reservations: make(map[int]*entry),
	}
}

func (m *Manager) Acquire(ctx context.Context, request route.Request) (*Lease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	if m.ctx.Err() != nil {
		m.mu.Unlock()
		return nil, errClosed
	}

	target := request.Target
	if target.Port == 0 {
		target.Port = m.defaults[target]
	}
	if current := m.entries[target]; m.useLocked(current) {
		m.mu.Unlock()
		return m.await(ctx, current)
	}

	// Resolve without the requesting context so one canceled caller cannot
	// cancel a connection shared by other requests.
	pending := m.lookups[request.Target]
	if pending == nil {
		pending = &lookup{done: make(chan struct{})}
		m.lookups[request.Target] = pending
		m.workers.Go(func() { m.load(request, pending) })
	}
	m.mu.Unlock()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-pending.done:
		if pending.err != nil {
			return nil, pending.err
		}
		return m.await(ctx, pending.entry)
	}
}

func (m *Manager) useLocked(e *entry) bool {
	if e == nil {
		return false
	}
	if !e.deadline.IsZero() {
		now := time.Now()
		if !now.Before(e.deadline) {
			m.removeLocked(e, errExpired)
			return false
		}
		e.deadline = now.Add(warmLifetime)
		select {
		case e.wake <- struct{}{}:
		default:
		}
	}
	return true
}

func defaultTarget(key route.Target) route.Target {
	key.Port = 0
	return key
}

func (m *Manager) load(req route.Request, pending *lookup) {
	ctx, cancel := context.WithTimeout(m.ctx, startupTimeout)
	defer cancel()
	destinations, err := m.backend.Resolve(ctx, req.Target, "")

	m.mu.Lock()
	defer m.mu.Unlock()
	defer close(pending.done)
	delete(m.lookups, req.Target)

	if err != nil {
		pending.err = err
		return
	}
	if len(destinations) == 0 {
		pending.err = errors.New("service has no eligible pod")
		return
	}
	if m.ctx.Err() != nil {
		pending.err = errClosed
		return
	}

	selected := destinations[0]
	key := selected.Target
	e := m.entries[key]
	if !m.useLocked(e) {
		entryContext, cancelEntry := context.WithCancel(m.ctx)
		e = &entry{
			target: key, host: req.Host, ctx: entryContext, cancel: cancelEntry,
			finished: make(chan struct{}), changed: make(chan struct{}), wake: make(chan struct{}, 1),
		}
		m.entries[key] = e
		m.workers.Go(func() { m.run(e, destinations) })
	}
	if selected.DefaultPort != 0 {
		m.defaults[defaultTarget(key)] = selected.DefaultPort
	} else {
		delete(m.defaults, defaultTarget(key))
	}
	pending.entry = e
}

func (m *Manager) await(ctx context.Context, e *entry) (*Lease, error) {
	for {
		m.mu.Lock()
		changed, lease, err := e.changed, e.lease, e.err
		m.mu.Unlock()
		if err != nil {
			return nil, err
		}
		if lease != nil {
			return lease, ctx.Err()
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-m.ctx.Done():
			return nil, errClosed
		case <-changed:
		}
	}
}

func (m *Manager) run(e *entry, destinations []Destination) {
	defer close(e.finished)
	defer e.cancel()
	ctx, cancel := context.WithTimeout(e.ctx, startupTimeout)
	lease, err := m.open(ctx, e, destinations, false)
	cancel()

	for err == nil {
		m.mu.Lock()
		if e.deadline.IsZero() {
			e.deadline = lease.Connected.Add(coldLifetime)
		}
		if e.err != nil || !time.Now().Before(e.deadline) {
			m.mu.Unlock()
			m.release(e, lease)
			err = errExpired
			break
		}
		e.lease = lease
		e.notifyLocked()
		m.mu.Unlock()

		recoverable := m.watch(e, lease)
		m.mu.Lock()
		e.lease = nil
		m.mu.Unlock()
		m.release(e, lease)
		if !recoverable {
			err = errExpired
			break
		}
		lease, err = m.recover(e, lease.Destination)
	}
	m.mu.Lock()
	m.removeLocked(e, err)
	m.mu.Unlock()
}

func (e *entry) notifyLocked() {
	close(e.changed)
	e.changed = make(chan struct{})
}

func (m *Manager) removeLocked(e *entry, err error) {
	if m.entries[e.target] == e {
		delete(m.entries, e.target)
		key := defaultTarget(e.target)
		if m.defaults[key] == e.target.Port {
			delete(m.defaults, key)
		}
	}
	if e.err == nil {
		e.err = err
		e.cancel()
		e.notifyLocked()
	}
}

func (m *Manager) deadline(e *entry) time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return e.deadline
}

func (m *Manager) watch(e *entry, lease *Lease) bool {
	probes := time.NewTicker(probeInterval)
	defer probes.Stop()
	timer := time.NewTimer(time.Until(m.deadline(e)))
	defer timer.Stop()
	for {
		expires := m.deadline(e)
		if !time.Now().Before(expires) {
			return false
		}
		timer.Reset(time.Until(expires))
		select {
		case <-e.ctx.Done():
			return false
		case <-e.wake:
		case <-timer.C:
		case <-lease.tunnel.Done:
			return true
		case <-probes.C:
			ctx, cancel := context.WithTimeout(e.ctx, probeTimeout)
			err := m.probe(ctx, lease)
			cancel()
			if err != nil {
				return true
			}
		}
	}
}

func (m *Manager) recover(e *entry, failed Destination) (*Lease, error) {
	for e.ctx.Err() == nil {
		deadline := m.deadline(e)
		if !time.Now().Before(deadline) {
			break
		}
		ctx, cancel := context.WithDeadline(e.ctx, deadline)
		attempt, stopAttempt := context.WithTimeout(ctx, startupTimeout)
		destinations, err := m.backend.Resolve(attempt, e.target, failed.PodUID)
		if err == nil && len(destinations) > 0 {
			m.mu.Lock()
			key := defaultTarget(e.target)
			if m.entries[e.target] == e && m.defaults[key] == e.target.Port {
				if destinations[0].DefaultPort == 0 {
					delete(m.defaults, key)
				} else {
					m.defaults[key] = destinations[0].DefaultPort
				}
			}
			m.mu.Unlock()
			var lease *Lease
			lease, err = m.open(attempt, e, destinations, true)
			if err == nil {
				stopAttempt()
				cancel()
				return lease, nil
			}
		}
		stopAttempt()
		cancel()
		if errors.Is(err, errRouteUnavailable) {
			return nil, err
		}

		delay := min(retryInterval, time.Until(m.deadline(e)))
		if delay <= 0 {
			break
		}
		timer := time.NewTimer(delay)
		select {
		case <-e.ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	return nil, errExpired
}

func (m *Manager) open(ctx context.Context, e *entry, destinations []Destination, verify bool) (*Lease, error) {
	var last error
	for _, destination := range destinations {
		skipped := make(map[int]bool)
		for ctx.Err() == nil {
			port, evicted, err := m.reserve(e, skipped)
			if err != nil {
				return nil, err
			}
			if evicted != nil {
				<-evicted.finished
			}
			tunnel, err := m.backend.Open(ctx, destination, port)
			if err != nil {
				m.unreserve(e, port)
				if errors.Is(err, ErrPortInUse) {
					skipped[port] = true
					continue
				}
				last = err
				break
			}
			lease := newLease(destination, e.host, port, tunnel)
			if verify {
				check, cancel := context.WithTimeout(ctx, probeTimeout)
				err := m.probe(check, lease)
				cancel()
				if err != nil {
					m.release(e, lease)
					last = fmt.Errorf("recovered tunnel did not respond: %w", err)
					break
				}
				lease.Connected = time.Now()
			}
			return lease, nil
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if last == nil {
		last = errors.New("service has no eligible pod")
	}
	return nil, last
}

func (m *Manager) reserve(e *entry, skipped map[int]bool) (int, *entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, offset := range rand.Perm(m.maxPort - m.minPort) {
		port := m.minPort + offset
		if skipped[port] || m.reservations[port] != nil || m.checkPort(port) != nil {
			continue
		}
		m.reservations[port] = e
		return port, nil, nil
	}

	var oldest *entry
	for _, current := range m.entries {
		if current == e || current.lease == nil {
			continue
		}
		if oldest == nil || current.lease.Connected.Before(oldest.lease.Connected) ||
			(current.lease.Connected.Equal(oldest.lease.Connected) && current.lease.LocalPort < oldest.lease.LocalPort) {
			oldest = current
		}
	}
	if oldest == nil {
		return 0, nil, errors.New("no local port available in the forwarding range")
	}
	port := oldest.lease.LocalPort
	m.reservations[port] = e
	m.removeLocked(oldest, errEvicted)
	return port, oldest, nil
}

func (m *Manager) unreserve(e *entry, port int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.reservations[port] == e {
		delete(m.reservations, port)
	}
}

func (m *Manager) release(e *entry, lease *Lease) {
	lease.close()
	m.unreserve(e, lease.LocalPort)
}

func (m *Manager) Snapshot() []Row {
	m.mu.Lock()
	defer m.mu.Unlock()
	var rows []Row
	now := time.Now()
	for _, e := range m.entries {
		if e.lease != nil && now.Before(e.deadline) {
			rows = append(rows, Row{e.target, e.lease.Destination.PortName, e.lease.LocalPort, e.lease.Connected})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Context != b.Context {
			return a.Context < b.Context
		}
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		if a.Service != b.Service {
			return a.Service < b.Service
		}
		return a.Port < b.Port
	})
	return rows
}

func (m *Manager) Close() {
	m.mu.Lock()
	m.cancel()
	m.mu.Unlock()
	m.workers.Wait()
}

func checkLocalPort(port int) error {
	listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return err
	}
	return listener.Close()
}
