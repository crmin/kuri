package forward

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/crmin/kuri/internal/route"
	"github.com/crmin/kuri/internal/settings"
)

const (
	probeInterval  = 10 * time.Second
	retryInterval  = 2 * time.Second
	retryJitter    = time.Second
	probeTimeout   = 3 * time.Second
	startupTimeout = 20 * time.Second
)

var (
	ErrPortInUse    = errors.New("local port is in use")
	errClosed       = errors.New("port forwarding is shutting down")
	errExpired      = errors.New("port forward expired")
	errEvicted      = errors.New("port forward was evicted to free a local port")
	errRouteChanged = errors.New("the service port routing changed")
)

type Destination struct {
	route.Target
	PortName    string
	DefaultPort route.Selector
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
	Resolve(context.Context, route.Selector, string) ([]Destination, error)
	Open(context.Context, Destination, int) (Tunnel, error)
}

type Row struct {
	route.Target
	PortName    string     `json:"port_name"`
	LocalPort   int        `json:"local_port"`
	Connected   time.Time  `json:"connected"`
	LastAccess  *time.Time `json:"last_access,omitempty"`
	DefaultPort bool       `json:"default_port,omitempty"`
	MenuHost    string     `json:"menu_host,omitempty"`
	MenuLabel   string     `json:"menu_label,omitempty"`
}

type Manager struct {
	backend          Backend
	ctx              context.Context
	cancel           context.CancelFunc
	workers          sync.WaitGroup
	minPort, maxPort int
	checkPort        func(int) error
	probe            func(context.Context, *Lease) error
	ports            *portPool

	mu        sync.Mutex
	entries   map[route.Selector]*entry
	defaults  map[route.Target]route.Selector
	lookups   map[route.Selector]*lookup
	ephemeral map[*entry]struct{}
	keepAlive bool
	lifetime  time.Duration
	trigger   string
	enabled   func(string) bool
	history   []Row
	healing   healingPolicy
	routes    *route.Contexts
}

type portPool struct {
	mu      sync.Mutex
	owners  map[int]*entry
	changed chan struct{}
}

type lookup struct {
	done  chan struct{}
	entry *entry
	err   error
}

type entry struct {
	owner     *Manager
	key       route.Selector
	host      string
	ctx       context.Context
	cancel    context.CancelFunc
	finished  chan struct{}
	wake      chan struct{}
	ephemeral bool

	// Protected by Manager.mu.
	changed    chan struct{}
	deadline   time.Time
	candidates []Destination // While opening or recovering, before a lease is available.
	lease      *Lease
	err        error
	lastAccess time.Time
	opened     time.Time
	users      int
	retireErr  error
}

func NewManager(backend Backend) *Manager {
	return NewManagerWithPolicy(backend, settings.Defaults("").PortForward)
}

func NewManagerWithPolicy(backend Backend, policy settings.PortForward) *Manager {
	manager := newManager(backend, policy.MinPort, policy.MaxPort+1, checkLocalPort, probeHTTP)
	manager.UpdatePolicy(policy, nil)
	return manager
}

func (m *Manager) NextBackend(backend Backend, policy settings.PortForward) *Manager {
	next := NewManagerWithPolicy(backend, policy)
	next.ports = m.ports
	return next
}

func newManager(backend Backend, minPort, maxPort int, check func(int) error, probe func(context.Context, *Lease) error) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	heal, stopHealing := context.WithCancel(ctx)
	return &Manager{
		backend: backend, ctx: ctx, cancel: cancel, minPort: minPort, maxPort: maxPort,
		checkPort: check, probe: probe,
		entries: make(map[route.Selector]*entry), defaults: make(map[route.Target]route.Selector),
		lookups: make(map[route.Selector]*lookup), ephemeral: make(map[*entry]struct{}),
		ports:     &portPool{owners: make(map[int]*entry), changed: make(chan struct{})},
		keepAlive: true, lifetime: 60 * time.Minute, trigger: settings.LastRequest,
		healing: healingPolicy{ctx: heal, cancel: stopHealing, interval: probeInterval, changed: make(chan struct{})},
	}
}

func (m *Manager) Acquire(ctx context.Context, request route.Request) (*Lease, error) {
	lease, release, err := m.AcquireRequest(ctx, request)
	if err == nil {
		release()
	}
	return lease, err
}

func (m *Manager) AcquireRequest(ctx context.Context, request route.Request) (*Lease, func(), error) {
	for {
		lease, release, err := m.acquireRequest(ctx, request)
		if err != errRouteChanged {
			return lease, release, err
		}
	}
}

func (m *Manager) acquireRequest(ctx context.Context, request route.Request) (*Lease, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	m.mu.Lock()
	if m.ctx.Err() != nil {
		m.mu.Unlock()
		return nil, nil, errClosed
	}
	if m.enabled != nil && !m.enabled(request.Context) {
		m.mu.Unlock()
		return nil, nil, errors.New("the Kubernetes context is disabled")
	}
	if !m.keepAlive {
		m.workers.Add(1)
		m.mu.Unlock()
		return m.acquireEphemeral(ctx, request)
	}

	key := request.Selector
	if key.IsDefault() {
		key = m.defaults[key.ServiceTarget()]
	}
	if current := m.entries[key]; m.useLocked(current) {
		m.mu.Unlock()
		return m.await(ctx, request.Selector, current)
	}

	// Resolve without the requesting context so one canceled caller cannot
	// cancel a connection shared by other requests.
	pending := m.lookups[request.Selector]
	if pending == nil {
		pending = &lookup{done: make(chan struct{})}
		m.lookups[request.Selector] = pending
		m.workers.Go(func() { m.load(request, pending) })
	}
	m.mu.Unlock()

	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case <-pending.done:
		if pending.err != nil {
			return nil, nil, pending.err
		}
		return m.await(ctx, request.Selector, pending.entry)
	}
}

func (m *Manager) useLocked(e *entry) bool {
	if e == nil {
		return false
	}
	if !e.deadline.IsZero() {
		now := time.Now()
		if !now.Before(e.deadline) {
			m.retireLocked(e, errExpired)
			return false
		}
		if m.trigger == settings.LastRequest {
			e.deadline = now.Add(m.lifetime)
			e.wakeLocked()
		}
	}
	return true
}

func (m *Manager) load(req route.Request, pending *lookup) {
	ctx, cancel := context.WithTimeout(m.ctx, startupTimeout)
	defer cancel()
	destinations, err := m.backend.Resolve(ctx, req.Selector, "")

	m.mu.Lock()
	defer m.mu.Unlock()
	defer close(pending.done)
	delete(m.lookups, req.Selector)

	if err != nil {
		pending.err = err
		return
	}
	if len(destinations) == 0 {
		pending.err = errors.New("service has no eligible pod")
		return
	}
	if m.ctx.Err() != nil || !m.keepAlive || (m.enabled != nil && !m.enabled(req.Context)) {
		pending.err = errClosed
		return
	}

	selected := destinations[0]
	key := route.ResolvedSelector(selected.Target, selected.PortName)
	e := m.entries[key]
	if e != nil && e.portsChanged(destinations) {
		m.retireLocked(e, errRouteChanged)
		e = nil
	}
	if !m.useLocked(e) {
		entryContext, cancelEntry := context.WithCancel(m.ctx)
		e = &entry{
			owner: m, key: key, host: req.Host, ctx: entryContext, cancel: cancelEntry,
			finished: make(chan struct{}), changed: make(chan struct{}), wake: make(chan struct{}, 1),
			candidates: destinations,
		}
		m.entries[key] = e
		m.workers.Go(func() { m.run(e, destinations) })
	}
	m.setDefaultLocked(key.ServiceTarget(), selected.DefaultPort)
	pending.entry = e
}

// Compare resolved Pod ports by UID so a change in candidate order alone does
// not replace a working tunnel. Hold Manager.mu.
func (e *entry) portsChanged(destinations []Destination) bool {
	previous := e.candidates
	if e.lease != nil {
		previous = []Destination{e.lease.Destination}
	}
	if previous[0].Port != destinations[0].Port {
		return true
	}
	if len(previous) == 1 {
		current := previous[0]
		return slices.ContainsFunc(destinations, func(dest Destination) bool {
			return dest.PodUID == current.PodUID && dest.TargetPort != current.TargetPort
		})
	}
	portsByPod := make(map[string]int32, len(previous))
	for _, dest := range previous {
		portsByPod[dest.PodUID] = dest.TargetPort
	}
	for _, dest := range destinations {
		if port, ok := portsByPod[dest.PodUID]; ok && port != dest.TargetPort {
			return true
		}
	}
	return false
}

func (m *Manager) setDefaultLocked(service route.Target, selected route.Selector) {
	previous := m.defaults[service]
	if selected.IsDefault() {
		delete(m.defaults, service)
	} else {
		m.defaults[service] = selected
	}
	m.updateRecentDefaultLocked(service, selected)
	if old := m.entries[previous]; previous != selected && old != nil {
		old.notifyLocked()
	}
}

func (m *Manager) await(ctx context.Context, selector route.Selector, e *entry) (*Lease, func(), error) {
	for {
		m.mu.Lock()
		changed, lease, err := e.changed, e.lease, e.err
		if e.retireErr != nil {
			err = e.retireErr
		}
		if selector.IsDefault() &&
			(errors.Is(err, errRouteUnavailable) || (err == nil && m.defaults[selector.ServiceTarget()] != e.key)) {
			err = errRouteChanged
		}
		if err == nil && lease != nil && lease.isAlive() {
			if err = ctx.Err(); err == nil {
				e.lastAccess = time.Now()
				e.users++
				m.recordLocked(e)
			}
			m.mu.Unlock()
			if err != nil {
				return nil, nil, err
			}
			return lease, sync.OnceFunc(func() {
				m.mu.Lock()
				defer m.mu.Unlock()
				e.users--
				if e.users == 0 && e.retireErr != nil {
					e.cancel()
				}
			}), nil
		}
		m.mu.Unlock()
		if err != nil {
			return nil, nil, err
		}
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-m.ctx.Done():
			return nil, nil, errClosed
		case <-changed:
		}
	}
}

func (m *Manager) run(e *entry, destinations []Destination) {
	defer close(e.finished)
	defer e.cancel()
	ctx, cancel := context.WithTimeout(e.ctx, startupTimeout)
	lease, err := m.open(ctx, e, destinations, false, 0)
	cancel()
	var healing context.Context

	for err == nil {
		m.mu.Lock()
		if e.opened.IsZero() {
			e.opened = lease.Connected
			e.deadline = e.opened.Add(m.lifetime)
		}
		if healing != nil && healing.Err() != nil {
			m.mu.Unlock()
			m.release(e, lease)
			err = errRouteChanged
			break
		}
		if e.err != nil || e.retireErr != nil || !m.containsPortLocked(lease.LocalPort) || !time.Now().Before(e.deadline) {
			m.mu.Unlock()
			m.release(e, lease)
			err = errExpired
			break
		}
		// A request admitted by the previous handler can still finish on its
		// lease. The next policy update can retire it if the address changed.
		m.refreshProbeHostLocked(lease)
		e.candidates = nil
		e.lease = lease
		e.notifyLocked()
		m.ports.mu.Lock()
		m.ports.signal()
		m.ports.mu.Unlock()
		m.mu.Unlock()

		healing = m.watch(e, lease)
		dead := !lease.isAlive()
		m.mu.Lock()
		e.lease = nil
		e.candidates = []Destination{lease.Destination}
		m.mu.Unlock()
		m.release(e, lease)
		if healing == nil {
			err = errExpired
			if dead {
				err = errRouteChanged
			}
			break
		}
		lease, err = m.recover(e, lease.Destination, healing)
	}
	m.mu.Lock()
	m.removeLocked(e, err)
	m.mu.Unlock()
}

func (e *entry) notifyLocked() {
	close(e.changed)
	e.changed = make(chan struct{})
}

func (e *entry) wakeLocked() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

func (m *Manager) forgetLocked(e *entry) {
	if m.entries[e.key] == e {
		delete(m.entries, e.key)
		service := e.key.ServiceTarget()
		if m.defaults[service] == e.key {
			delete(m.defaults, service)
		}
	}
}

func (m *Manager) retireLocked(e *entry, err error) {
	m.forgetLocked(e)
	e.retireErr = err
	e.wakeLocked()
	if e.users == 0 {
		m.removeLocked(e, err)
	} else {
		e.notifyLocked()
	}
}

func (m *Manager) removeLocked(e *entry, err error) {
	m.forgetLocked(e)
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

func (m *Manager) watch(e *entry, lease *Lease) context.Context {
	var probes probeSchedule
	defer probes.stop()
	var expiry *time.Timer
	var expiresAt <-chan time.Time
	if !e.ephemeral {
		expiry = time.NewTimer(time.Until(m.deadline(e)))
		defer expiry.Stop()
		expiresAt = expiry.C
	}
	for {
		m.mu.Lock()
		expires, retired := e.deadline, e.retireErr != nil
		policy := m.healing
		if !e.ephemeral && !retired && !time.Now().Before(expires) {
			m.retireLocked(e, errExpired)
			retired = true
		}
		m.mu.Unlock()
		if retired {
			select {
			case <-e.ctx.Done():
			case <-lease.tunnel.Done:
			}
			return nil
		}
		if expiry != nil {
			expiry.Reset(time.Until(expires))
		}
		probes.update(policy)
		select {
		case <-e.ctx.Done():
			return nil
		case <-e.wake:
		case <-expiresAt:
		case <-policy.changed:
		case <-lease.tunnel.Done:
			return m.recoveryScope(e)
		case <-probes.ticks:
			if policy.ctx.Err() != nil {
				continue
			}
			ctx, cancel := context.WithTimeout(e.ctx, probeTimeout)
			stop := context.AfterFunc(policy.ctx, cancel)
			host := lease.probeAuthority()
			err := m.probe(ctx, lease)
			stop()
			cancel()
			if err != nil && policy.ctx.Err() == nil && host == lease.probeAuthority() {
				if current := m.recoveryScope(e); current == policy.ctx {
					return current
				}
			}
		}
	}
}

func (m *Manager) recoveryScope(e *entry) context.Context {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e.ephemeral || e.retireErr != nil || e.ctx.Err() != nil {
		return nil
	}
	return m.healing.ctx
}

func (m *Manager) recover(e *entry, failed Destination, healing context.Context) (*Lease, error) {
	scope, stop := context.WithCancel(e.ctx)
	defer stop()
	stopOnPolicy := context.AfterFunc(healing, stop)
	defer stopOnPolicy()
	for scope.Err() == nil && healing.Err() == nil {
		deadline := m.deadline(e)
		if !time.Now().Before(deadline) {
			break
		}
		ctx, cancel := context.WithDeadline(scope, deadline)
		attempt, stopAttempt := context.WithTimeout(ctx, startupTimeout)
		destinations, err := m.backend.Resolve(attempt, e.key, failed.PodUID)
		if err == nil && len(destinations) > 0 {
			m.mu.Lock()
			if m.entries[e.key] != e {
				m.mu.Unlock()
				stopAttempt()
				cancel()
				return nil, errExpired
			}
			e.candidates = destinations
			// A different default can be used even if this tunnel cannot reopen.
			service := e.key.ServiceTarget()
			if m.defaults[service] == e.key {
				m.setDefaultLocked(service, destinations[0].DefaultPort)
			}
			m.mu.Unlock()
			var lease *Lease
			lease, err = m.open(attempt, e, destinations, true, 0)
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

		delay := min(recoveryDelay(), time.Until(m.deadline(e)))
		if delay <= 0 {
			break
		}
		timer := time.NewTimer(delay)
		select {
		case <-scope.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	if healing.Err() != nil {
		return nil, errRouteChanged
	}
	return nil, errExpired
}

func recoveryDelay() time.Duration {
	// Each failed attempt independently waits between 1 and 3 seconds.
	return retryInterval - retryJitter + time.Duration(rand.Int64N(int64(2*retryJitter)+1))
}

func (m *Manager) open(ctx context.Context, e *entry, destinations []Destination, verify bool, reserved int) (*Lease, error) {
	defer func() {
		if reserved != 0 {
			m.unreserve(e, reserved)
		}
	}()
	if reserved != 0 {
		m.mu.Lock()
		withinRange := m.containsPortLocked(reserved)
		m.mu.Unlock()
		if !withinRange {
			m.unreserve(e, reserved)
			reserved = 0
		}
	}
	var last error
	for _, destination := range destinations {
		skipped := make(map[int]bool)
		for ctx.Err() == nil {
			port := reserved
			reserved = 0
			var evicted *entry
			if port == 0 {
				var err error
				port, evicted, err = m.reserve(ctx, e, skipped)
				if err != nil {
					return nil, err
				}
			}
			if evicted != nil {
				select {
				case <-ctx.Done():
					// The old listener still owns the port until it closes.
					<-evicted.finished
					m.unreserve(e, port)
					return nil, ctx.Err()
				case <-evicted.finished:
				}
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
				m.mu.Lock()
				routable := m.refreshProbeHostLocked(lease)
				m.mu.Unlock()
				if !routable {
					m.release(e, lease)
					return nil, errRouteUnavailable
				}
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

func (m *Manager) reserve(ctx context.Context, e *entry, skipped map[int]bool) (int, *entry, error) {
	for {
		if err := ctx.Err(); err != nil {
			return 0, nil, err
		}
		port, evicted, changed := m.tryReserve(e, skipped)
		if port != 0 {
			return port, evicted, nil
		}
		if changed == nil {
			return 0, nil, errors.New("no local port available in the forwarding range")
		}
		select {
		case <-ctx.Done():
			return 0, nil, ctx.Err()
		case <-changed:
		}
	}
}

func (m *Manager) tryReserve(e *entry, skipped map[int]bool) (int, *entry, <-chan struct{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ports.mu.Lock()
	defer m.ports.mu.Unlock()

	var wait <-chan struct{}
	width := m.maxPort - m.minPort
	if width <= 0 {
		return 0, nil, nil
	}
	start := rand.IntN(width)
	for offset := range width {
		port := m.minPort + (start+offset)%width
		if skipped[port] {
			continue
		}
		if current := m.ports.owners[port]; current != nil {
			if current != e && (current.owner != m || current.ephemeral || current.retireErr != nil || current.lease == nil) {
				wait = m.ports.changed
			}
			continue
		}
		if m.checkPort(port) != nil {
			continue
		}
		m.ports.owners[port] = e
		return port, nil, nil
	}

	if e.ephemeral {
		return 0, nil, wait
	}
	var oldest *entry
	for _, current := range m.entries {
		if current == e || current.lease == nil {
			continue
		}
		port := current.lease.LocalPort
		if port < m.minPort || port >= m.maxPort || skipped[port] || m.ports.owners[port] != current {
			continue
		}
		if oldest == nil || current.lease.Connected.Before(oldest.lease.Connected) ||
			(current.lease.Connected.Equal(oldest.lease.Connected) && current.lease.LocalPort < oldest.lease.LocalPort) {
			oldest = current
		}
	}
	if oldest == nil {
		return 0, nil, wait
	}
	port := oldest.lease.LocalPort
	m.ports.owners[port] = e
	m.removeLocked(oldest, errEvicted)
	return port, oldest, nil
}

func (m *Manager) unreserve(e *entry, port int) {
	m.ports.mu.Lock()
	defer m.ports.mu.Unlock()
	if m.ports.owners[port] == e {
		delete(m.ports.owners, port)
		m.ports.signal()
	}
}

func (p *portPool) signal() {
	close(p.changed)
	p.changed = make(chan struct{})
}

func (m *Manager) release(e *entry, lease *Lease) {
	lease.close()
	m.unreserve(e, lease.LocalPort)
}

func (m *Manager) Snapshot() []Row {
	m.mu.Lock()
	var rows []Row
	now := time.Now()
	for _, e := range m.entries {
		if isActive(e, now) {
			rows = append(rows, m.snapshotRow(e))
		}
	}
	for e := range m.ephemeral {
		if e.lease.isAlive() {
			rows = append(rows, m.snapshotRow(e))
		}
	}
	m.mu.Unlock()
	slices.SortFunc(rows, func(a, b Row) int {
		if order := compareTarget(a.Target, b.Target); order != 0 {
			return order
		}
		return strings.Compare(a.PortName, b.PortName)
	})
	return rows
}

func isActive(e *entry, now time.Time) bool {
	return e.lease != nil && e.lease.isAlive() && now.Before(e.deadline)
}

// snapshotRow copies the mutable entry into the caller's snapshot. Hold m.mu.
func (m *Manager) snapshotRow(e *entry) Row {
	lastAccess := e.lastAccess
	dest := e.lease.Destination
	defaultPort := dest.DefaultPort == e.key
	if !e.ephemeral {
		defaultPort = m.defaults[e.key.ServiceTarget()] == e.key
	}
	return Row{
		Target: dest.Target, PortName: dest.PortName, LocalPort: e.lease.LocalPort,
		Connected: e.lease.Connected, LastAccess: &lastAccess,
		DefaultPort: defaultPort,
	}
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
