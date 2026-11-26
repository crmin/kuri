package forward

import (
	"context"
	"errors"
	"net"
	"slices"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/crmin/kuri/internal/route"
)

type stubBackend struct {
	resolve func(context.Context, route.Target, string) ([]Destination, error)
	open    func(context.Context, Destination, int) (Tunnel, error)
}

func (b stubBackend) Resolve(ctx context.Context, target route.Target, failed string) ([]Destination, error) {
	if b.resolve != nil {
		return b.resolve(ctx, target, failed)
	}
	return []Destination{stubDestination(target, "first")}, nil
}

func (b stubBackend) Open(ctx context.Context, destination Destination, port int) (Tunnel, error) {
	if b.open != nil {
		return b.open(ctx, destination, port)
	}
	return stubTunnel(), nil
}

func stubDestination(target route.Target, pod string) Destination {
	if target.Port == 0 {
		target.Port = 80
	}
	return Destination{Target: target, PortName: "http", DefaultPort: 80, Pod: pod, PodUID: pod, TargetPort: 8080}
}

func stubTunnel() Tunnel {
	done := make(chan struct{})
	return Tunnel{Done: done, Close: func() { close(done) }}
}

func testManager(backend Backend) *Manager {
	return newManager(backend, 30000, 30002, func(int) error { return nil },
		func(context.Context, *Lease) error { return nil })
}

func request(service string) route.Request {
	return route.Request{Target: route.Target{Context: "dev", Namespace: "default", Service: service},
		Host: service + ".default.dev.kuri.localhost"}
}

func acquire(t *testing.T, m *Manager, req route.Request) *Lease {
	t.Helper()
	lease, err := m.Acquire(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	return lease
}

func TestSnapshotTracksLastRequestAndDefault(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		manager := testManager(stubBackend{})
		defer manager.Close()
		acquire(t, manager, request("api"))
		row := manager.Snapshot()[0]
		if row.LastAccess == nil || !row.DefaultPort {
			t.Fatalf("unexpected row: %+v", row)
		}
		time.Sleep(probeInterval + time.Second)
		if got := manager.Snapshot()[0]; !got.LastAccess.Equal(*row.LastAccess) {
			t.Fatal("probe or snapshot changed the request timestamp")
		}
		acquire(t, manager, request("api"))
		if got := manager.Snapshot()[0]; !got.LastAccess.After(*row.LastAccess) || !got.Connected.Equal(row.Connected) {
			t.Fatalf("reuse did not update only last access: %+v", got)
		}
	})
}

func TestRecentSnapshotReturnsCountAndLatestRoutes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		manager := newManager(stubBackend{}, 30000, 30010, func(int) error { return nil }, func(context.Context, *Lease) error { return nil })
		defer manager.Close()
		for _, name := range []string{"g", "f", "e", "d", "c", "b", "a"} {
			acquire(t, manager, request(name))
		}
		count, rows := manager.RecentSnapshot(5)
		if count != 7 || len(rows) != 5 || rows[0].Service != "a" || rows[4].Service != "e" {
			t.Fatalf("count %d, rows %+v", count, rows)
		}
		time.Sleep(time.Second)
		acquire(t, manager, request("f"))
		count, rows = manager.RecentSnapshot(5)
		if count != 7 || len(rows) != 5 || rows[0].Service != "f" || rows[4].Service != "d" {
			t.Fatalf("count %d, rows %+v", count, rows)
		}
	})
}

func TestCloseReleasesEveryActiveForward(t *testing.T) {
	var closed atomic.Int64
	manager := testManager(stubBackend{open: func(context.Context, Destination, int) (Tunnel, error) {
		done := make(chan struct{})
		return Tunnel{Done: done, Close: func() { close(done); closed.Add(1) }}, nil
	}})
	defer manager.Close()
	acquire(t, manager, request("first"))
	acquire(t, manager, request("second"))
	if got := len(manager.Snapshot()); got != 2 {
		t.Fatalf("active forwards: %d", got)
	}
	manager.Close()
	if closed.Load() != 2 || len(manager.Snapshot()) != 0 {
		t.Fatalf("close left active forwards: %d", closed.Load())
	}
}

func TestConcurrentColdRequest(t *testing.T) {
	var resolves, opens atomic.Int64
	resolving := make(chan struct{})
	release := make(chan struct{})
	manager := testManager(stubBackend{
		resolve: func(_ context.Context, route route.Target, _ string) ([]Destination, error) {
			resolves.Add(1)
			<-resolving
			return []Destination{stubDestination(route, "first")}, nil
		},
		open: func(context.Context, Destination, int) (Tunnel, error) {
			opens.Add(1)
			<-release
			return stubTunnel(), nil
		},
	})
	defer manager.Close()
	type result struct {
		lease *Lease
		err   error
	}
	results := make(chan result, 12)
	for range cap(results) {
		go func() {
			lease, err := manager.Acquire(t.Context(), request("api"))
			results <- result{lease, err}
		}()
	}
	close(resolving)
	close(release)
	first := <-results
	if first.err != nil {
		t.Fatal(first.err)
	}
	for range cap(results) - 1 {
		if got := <-results; got.lease != first.lease || got.err != nil {
			t.Fatalf("inconsistent result: %+v", got)
		}
	}
	if resolves.Load() != 1 || opens.Load() != 1 {
		t.Fatalf("connect used %d resolutions and %d opens", resolves.Load(), opens.Load())
	}
}

func TestWarmExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		manager := testManager(stubBackend{})
		defer manager.Close()

		first := acquire(t, manager, request("api"))
		time.Sleep(59 * time.Minute)
		if len(manager.Snapshot()) != 1 {
			t.Fatal("cold tunnel expired early")
		}
		if got := acquire(t, manager, request("api")); got != first {
			t.Fatal("did not reuse warm tunnel")
		}
		time.Sleep(59 * time.Second)
		if len(manager.Snapshot()) != 1 {
			t.Fatal("warm tunnel expired early")
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if len(manager.Snapshot()) != 0 {
			t.Fatal("warm tunnel survived its 60-second deadline")
		}
	})
}

func TestWarmRequestReusesTunnelDuringAPIFailure(t *testing.T) {
	var calls atomic.Int64
	manager := testManager(stubBackend{resolve: func(_ context.Context, target route.Target, _ string) ([]Destination, error) {
		if calls.Add(1) > 1 {
			return nil, errors.New("API is unavailable")
		}
		return []Destination{stubDestination(target, "first")}, nil
	}})
	defer manager.Close()
	initial := acquire(t, manager, request("api"))
	explicit := request("api")
	explicit.Port = 80
	explicit.Host = "80." + explicit.Host
	if acquire(t, manager, explicit) != initial || acquire(t, manager, request("api")) != initial || calls.Load() != 1 {
		t.Fatal("an established route required an API lookup")
	}
}

func TestProbeReconnectUntilExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var probes, opens atomic.Int64
		manager := testManager(stubBackend{open: func(context.Context, Destination, int) (Tunnel, error) {
			opens.Add(1)
			return stubTunnel(), nil
		}})
		manager.probe = func(context.Context, *Lease) error {
			if probes.Add(1) == 1 {
				return errors.New("connection interrupted")
			}
			return nil
		}
		defer manager.Close()
		initial := acquire(t, manager, request("api"))
		_ = acquire(t, manager, request("api"))
		time.Sleep(9 * time.Second)
		if probes.Load() != 0 {
			t.Fatal("probed before the check interval")
		}
		time.Sleep(time.Second)
		synctest.Wait()
		rows := manager.Snapshot()
		if opens.Load() != 2 || len(rows) != 1 || !rows[0].Connected.After(initial.Connected) {
			t.Fatalf("recovery failed: opens=%d rows=%+v", opens.Load(), rows)
		}
		time.Sleep(50 * time.Second)
		synctest.Wait()
		if len(manager.Snapshot()) != 0 {
			t.Fatal("recovery extended the original deadline")
		}
	})
}

func TestExhaustedPoolEvictsOldest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		manager := testManager(stubBackend{})
		manager.maxPort++
		manager.checkPort = func(port int) error {
			if port == 30002 {
				return errors.New("port already used by another process")
			}
			return nil
		}
		defer manager.Close()
		oldest := acquire(t, manager, request("a"))
		time.Sleep(time.Second)
		acquire(t, manager, request("b"))
		acquire(t, manager, request("a")) // Refreshing activity does not change connection age.
		newest := acquire(t, manager, request("c"))
		if newest.LocalPort != oldest.LocalPort || len(manager.Snapshot()) != 2 {
			t.Fatalf("eviction did not reuse oldest port: %+v", manager.Snapshot())
		}
	})
}

func TestEvictedRequestCannotDialReassignedPort(t *testing.T) {
	manager := testManager(stubBackend{})
	manager.maxPort = manager.minPort + 1
	defer manager.Close()
	previous := acquire(t, manager, request("a"))
	current := acquire(t, manager, request("b"))
	if previous.LocalPort != current.LocalPort {
		t.Fatal("port was not reused")
	}
	conn, err := previous.dial(t.Context(), "tcp", previous.Address())
	if conn != nil {
		conn.Close()
	}
	if !errors.Is(err, net.ErrClosed) {
		t.Fatalf("previous lease still tries to connect: %v", err)
	}
}

func TestRecoveryTriesAlternateReadyPod(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var opened []string
		manager := testManager(stubBackend{
			resolve: func(_ context.Context, target route.Target, failed string) ([]Destination, error) {
				if failed == "first" {
					return []Destination{stubDestination(target, "alternate"), stubDestination(target, "first")}, nil
				}
				return []Destination{stubDestination(target, "first")}, nil
			},
			open: func(_ context.Context, dest Destination, _ int) (Tunnel, error) {
				opened = append(opened, dest.Pod)
				return stubTunnel(), nil
			},
		})
		manager.probe = func(_ context.Context, lease *Lease) error {
			if lease.Destination.Pod == "first" {
				return errors.New("unreachable pod")
			}
			return nil
		}
		defer manager.Close()
		acquire(t, manager, request("api"))
		time.Sleep(10 * time.Second)
		synctest.Wait()
		if !slices.Equal(opened, []string{"first", "alternate"}) {
			t.Fatalf("opened %v", opened)
		}
		if got := acquire(t, manager, request("api")).Destination.Pod; got != "alternate" {
			t.Fatalf("traffic was not recovered, pod = %q", got)
		}
	})
}

func TestRequestWaitsForReplacementWhenProbeIsInProgress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		firstDone := make(chan struct{})
		finishProbe := make(chan struct{})
		attempts := 0
		manager := testManager(stubBackend{
			open: func(context.Context, Destination, int) (Tunnel, error) {
				attempts++
				if attempts == 1 {
					return Tunnel{Done: firstDone, Close: func() {}}, nil
				}
				return stubTunnel(), nil
			},
		})
		manager.probe = func(_ context.Context, lease *Lease) error {
			if lease.tunnel.Done == firstDone {
				<-finishProbe
			}
			return nil
		}
		defer manager.Close()
		defer func() {
			select {
			case <-finishProbe:
			default:
				close(finishProbe)
			}
		}()

		req := request("api")
		first := acquire(t, manager, req)
		time.Sleep(probeInterval)
		synctest.Wait()
		close(firstDone)
		result := make(chan *Lease, 1)
		go func() {
			lease, _ := manager.Acquire(t.Context(), req)
			result <- lease
		}()
		synctest.Wait()
		if len(result) != 0 || len(manager.Snapshot()) != 0 {
			t.Error("returned an already closed tunnel while the probe was running")
		}
		close(finishProbe)
		if next := <-result; next == first || next == nil {
			t.Fatal("request did not receive the replacement tunnel")
		}
	})
}

func TestRecoveryRetriesWithoutRequestAndAppliesJitter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		attempts := make(chan time.Time, 2)
		firstDone := make(chan struct{})
		backend := stubBackend{
			resolve: func(_ context.Context, target route.Target, failed string) ([]Destination, error) {
				if failed == "" {
					return []Destination{stubDestination(target, "first")}, nil
				}
				attempts <- time.Now()
				if len(attempts) == 1 {
					return nil, errors.New("Kubernetes service not found")
				}
				return []Destination{stubDestination(target, "replacement")}, nil
			},
			open: func(_ context.Context, destination Destination, _ int) (Tunnel, error) {
				if destination.Pod == "first" {
					return Tunnel{Done: firstDone, Close: func() {}}, nil
				}
				return stubTunnel(), nil
			},
		}
		manager := testManager(backend)
		defer manager.Close()
		req := request("api")
		old := acquire(t, manager, req)
		disconnected := time.Now()
		close(firstDone)
		synctest.Wait()
		if len(attempts) != 1 {
			t.Fatalf("first recovery was not immediate, attempts=%d", len(attempts))
		}
		time.Sleep(3 * time.Second)
		synctest.Wait()
		first, second := <-attempts, <-attempts
		if !first.Equal(disconnected) || second.Sub(first) < time.Second || second.Sub(first) > 3*time.Second {
			t.Fatalf("recovery attempted at %v and %v", first.Sub(disconnected), second.Sub(disconnected))
		}
		if got := acquire(t, manager, req); got == old {
			t.Fatal("recovery reused the closed tunnel")
		}
	})
}

func TestRecoveryDelayUsesRandomJitter(t *testing.T) {
	delays := make(map[time.Duration]bool)
	for range 16 {
		delay := recoveryDelay()
		if delay < time.Second || delay > 3*time.Second {
			t.Fatalf("recovery delay outside jitter bounds: %s", delay)
		}
		delays[delay] = true
	}
	if len(delays) == 1 {
		t.Fatal("recovery attempts used a fixed delay")
	}
}
