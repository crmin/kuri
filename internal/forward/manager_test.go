package forward

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/crmin/kuri/internal/route"
	"github.com/crmin/kuri/internal/settings"
)

type stubBackend struct {
	resolve func(context.Context, route.Selector, string) ([]Destination, error)
	open    func(context.Context, Destination, int) (Tunnel, error)
}

func (b stubBackend) Resolve(ctx context.Context, selector route.Selector, failed string) ([]Destination, error) {
	if b.resolve != nil {
		return b.resolve(ctx, selector, failed)
	}
	return []Destination{stubDestination(selector, "first")}, nil
}

func (b stubBackend) Open(ctx context.Context, destination Destination, port int) (Tunnel, error) {
	if b.open != nil {
		return b.open(ctx, destination, port)
	}
	return stubTunnel(), nil
}

func stubDestination(selector route.Selector, pod string) Destination {
	target := selector.Target
	name := selector.PortName
	if selector.IsDefault() {
		name = "http"
	}
	if target.Port == 0 {
		target.Port = 80
	}
	return Destination{Target: target, PortName: name, DefaultPort: route.ResolvedSelector(target, "http"),
		Pod: pod, PodUID: pod, TargetPort: 8080}
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
	return route.Request{Selector: route.Selector{Target: route.Target{Context: "dev", Namespace: "default", Service: service}},
		Host: service + ".default.dev.kuri.localhost"}
}

func namedRequest(service, portName string) route.Request {
	req := request(service)
	req.PortName = portName
	req.Host = portName + "." + req.Host
	return req
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
	testConcurrentColdRequest(t, request("api"))
}

func TestConcurrentColdNamedRequest(t *testing.T) {
	testConcurrentColdRequest(t, namedRequest("api", "web"))
}

func testConcurrentColdRequest(t *testing.T, req route.Request) {
	t.Helper()
	var resolves, opens atomic.Int64
	resolving := make(chan struct{})
	release := make(chan struct{})
	manager := testManager(stubBackend{
		resolve: func(_ context.Context, selector route.Selector, _ string) ([]Destination, error) {
			resolves.Add(1)
			<-resolving
			return []Destination{stubDestination(selector, "first")}, nil
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
			lease, err := manager.Acquire(t.Context(), req)
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
		time.Sleep(59*time.Minute + 59*time.Second)
		if len(manager.Snapshot()) != 1 {
			t.Fatal("warm tunnel expired early")
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if len(manager.Snapshot()) != 0 {
			t.Fatal("warm tunnel survived its 60-minute deadline")
		}
	})
}

func TestWarmNamedRequestReusesDefaultForward(t *testing.T) {
	defaultReq, namedReq := request("api"), namedRequest("api", "http")
	for _, first := range []route.Request{defaultReq, namedReq} {
		t.Run(first.Host, func(t *testing.T) {
			var calls atomic.Int64
			manager := testManager(stubBackend{resolve: func(_ context.Context, selector route.Selector, _ string) ([]Destination, error) {
				if calls.Add(1) > 1 {
					return nil, errors.New("API is unavailable")
				}
				return []Destination{stubDestination(selector, "first")}, nil
			}})
			defer manager.Close()
			initial := acquire(t, manager, first)
			if acquire(t, manager, namedReq) != initial || acquire(t, manager, defaultReq) != initial || calls.Load() != 1 {
				t.Fatal("an established route required an API lookup")
			}
		})
	}
}

func TestFreshLookupReplacesChangedNamedForward(t *testing.T) {
	for _, tc := range []struct {
		name         string
		port, target int32
	}{
		{"service port", 3100, 3000},
		{"pod port", 3000, 3100},
		{"candidate order", 3000, 3000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			web := namedRequest("api", "web")
			old := stubDestination(web.Selector, "old")
			old.Port, old.TargetPort, old.DefaultPort = 3000, 3000, route.Selector{}
			updated := old
			updated.Port, updated.TargetPort, updated.DefaultPort = tc.port, tc.target, web.Selector
			alternate := updated
			alternate.Pod, alternate.PodUID, alternate.TargetPort = "alternate", "alternate", 3200
			manager := testManager(stubBackend{resolve: func(_ context.Context, selector route.Selector, _ string) ([]Destination, error) {
				if selector.IsDefault() {
					return []Destination{alternate, updated}, nil
				}
				return []Destination{old}, nil
			}})
			defer manager.Close()
			first, release, err := manager.AcquireRequest(t.Context(), web)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			got := acquire(t, manager, request("api"))
			if tc.port == old.Port && tc.target == old.TargetPort {
				if got != first {
					t.Fatal("candidate order alone replaced the tunnel")
				}
				return
			}
			if got == first || got.Destination != alternate || acquire(t, manager, web) != got {
				t.Fatalf("requests did not switch to the new tunnel: %+v", got.Destination)
			}
			if !first.isAlive() {
				t.Fatal("replacement interrupted an acquired lease")
			}
			release()
			select {
			case <-first.tunnel.Done:
			case <-time.After(time.Second):
				t.Fatal("old tunnel did not close after release")
			}
		})
	}
}

func TestFreshLookupReplacesOpeningNamedForward(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		web := namedRequest("api", "web")
		old := stubDestination(web.Selector, "pod")
		old.DefaultPort = route.Selector{}
		updated := old
		updated.TargetPort, updated.DefaultPort = 3100, web.Selector
		manager := testManager(stubBackend{
			resolve: func(_ context.Context, selector route.Selector, _ string) ([]Destination, error) {
				if selector.IsDefault() {
					return []Destination{updated}, nil
				}
				return []Destination{old}, nil
			},
			open: func(ctx context.Context, dest Destination, _ int) (Tunnel, error) {
				if dest == old {
					<-ctx.Done()
					return Tunnel{}, ctx.Err()
				}
				return stubTunnel(), nil
			},
		})
		defer manager.Close()
		pending := make(chan *Lease, 1)
		go func() {
			lease, _ := manager.Acquire(t.Context(), web)
			pending <- lease
		}()
		synctest.Wait()
		if len(pending) != 0 {
			t.Fatal("named request finished before the initial tunnel was open")
		}
		fresh := acquire(t, manager, request("api"))
		if fresh.Destination != updated || <-pending != fresh {
			t.Fatalf("pending request did not follow the replacement: %+v", fresh.Destination)
		}
	})
}

func TestPendingDefaultRequestUsesNewDefaultDuringFailedRecovery(t *testing.T) {
	for _, observedBy := range []string{"recovery", "other lookup"} {
		t.Run(observedBy, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				web, http := namedRequest("api", "web"), namedRequest("api", "http")
				defaultReq := request("api")
				firstDone, observe := make(chan struct{}), make(chan struct{})
				old := stubDestination(web.Selector, "old")
				old.DefaultPort = web.Selector
				recovering := stubDestination(web.Selector, "unreachable")
				recovering.DefaultPort = http.Selector
				current := stubDestination(http.Selector, "healthy")
				manager := testManager(stubBackend{
					resolve: func(ctx context.Context, selector route.Selector, failed string) ([]Destination, error) {
						if failed != "" {
							select {
							case <-observe:
							case <-ctx.Done():
								return nil, ctx.Err()
							}
							if observedBy == "other lookup" {
								return nil, errors.New("no eligible web Pod")
							}
							return []Destination{recovering}, nil
						}
						select {
						case <-observe:
							return []Destination{current}, nil
						default:
							return []Destination{old}, nil
						}
					},
					open: func(_ context.Context, dest Destination, _ int) (Tunnel, error) {
						switch dest.Pod {
						case old.Pod:
							return Tunnel{Done: firstDone, Close: func() {}}, nil
						case recovering.Pod:
							return Tunnel{}, errors.New("web port unreachable")
						default:
							return stubTunnel(), nil
						}
					},
				})
				defer manager.Close()
				acquire(t, manager, defaultReq)
				close(firstDone)
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				ready := make(chan error, 1)
				go func() {
					lease, err := manager.Acquire(ctx, defaultReq)
					if err == nil && lease.Destination != current {
						err = fmt.Errorf("unexpected default: %+v", lease.Destination)
					}
					ready <- err
				}()
				synctest.Wait()
				if len(ready) != 0 {
					t.Fatal("default request did not wait for the mapping to change")
				}
				close(observe)
				if observedBy == "other lookup" {
					acquire(t, manager, http)
				}
				if err := <-ready; err != nil {
					t.Fatalf("pending default request did not connect to the healthy port: %v", err)
				}
			})
		})
	}
}

func TestNamedForwardRecoveryRefreshesPort(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		defaultReq := request("api")
		web, http := namedRequest("api", "web"), namedRequest("api", "http")
		firstDone, resume := make(chan struct{}), make(chan struct{})
		initial := Destination{Target: defaultReq.Target, PortName: "web", DefaultPort: web.Selector, PodUID: "old-pod", TargetPort: 3100}
		initial.Port = 3000
		updated := Destination{Target: defaultReq.Target, PortName: "web", DefaultPort: http.Selector, PodUID: "new-pod", TargetPort: 3200}
		updated.Port = 3100
		newDefault := Destination{Target: defaultReq.Target, PortName: "http", DefaultPort: http.Selector, PodUID: "new-pod", TargetPort: 8000}
		newDefault.Port = 8000
		manager := testManager(stubBackend{
			resolve: func(ctx context.Context, selector route.Selector, failed string) ([]Destination, error) {
				if failed != "" {
					if failed != initial.PodUID || selector != web.Selector {
						return nil, fmt.Errorf("unexpected recovery selector %+v, failed Pod %q", selector, failed)
					}
					select {
					case <-resume:
						return []Destination{updated}, nil
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}
				select {
				case <-resume:
					if !selector.IsDefault() {
						return nil, fmt.Errorf("unexpected second lookup %+v", selector)
					}
					return []Destination{newDefault}, nil
				default:
					return []Destination{initial}, nil
				}
			},
			open: func(_ context.Context, dest Destination, _ int) (Tunnel, error) {
				if dest == initial {
					return Tunnel{Done: firstDone, Close: func() {}}, nil
				}
				return stubTunnel(), nil
			},
		})
		defer manager.Close()
		acquire(t, manager, defaultReq)
		close(firstDone)
		type result struct {
			lease *Lease
			err   error
		}
		ready := make(chan result, 1)
		go func() {
			lease, err := manager.Acquire(t.Context(), defaultReq)
			ready <- result{lease, err}
		}()
		synctest.Wait()
		if len(ready) != 0 {
			t.Fatal("default request did not wait for the recovery")
		}
		close(resume)
		if got := <-ready; got.err != nil || got.lease.Destination != newDefault {
			t.Fatalf("waiting default request used the wrong port: %+v", got)
		}
		_, before := manager.RecentHistory()
		for _, row := range before {
			if row.PortName == "web" && row.DefaultPort {
				t.Fatalf("previous default would still get a default copy URL: %+v", row)
			}
		}
		time.Sleep(time.Millisecond)
		if got := acquire(t, manager, web); got.Destination != updated {
			t.Fatalf("named request used the wrong recovered port: %+v", got.Destination)
		}
		count, recent := manager.RecentHistory()
		if count != 2 || len(recent) != 2 || recent[0].PortName != "web" || recent[0].Port != updated.Port || recent[0].DefaultPort {
			t.Fatalf("unexpected recovered history: %d %+v", count, recent)
		}
	})
}

func TestNamedForwardRetiresWhenPortIsRenamed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		web, admin := namedRequest("api", "web"), namedRequest("api", "admin")
		firstDone, resume := make(chan struct{}), make(chan struct{})
		old := stubDestination(web.Selector, "old")
		old.DefaultPort = web.Selector
		renamed := stubDestination(admin.Selector, "replacement")
		renamed.DefaultPort = admin.Selector
		manager := testManager(stubBackend{
			resolve: func(ctx context.Context, selector route.Selector, failed string) ([]Destination, error) {
				if failed != "" {
					select {
					case <-resume:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}
				select {
				case <-firstDone:
					if selector == web.Selector {
						return nil, errRouteUnavailable
					}
					if !selector.IsDefault() || failed != "" {
						return nil, fmt.Errorf("unexpected resolution %+v after failure of %q", selector, failed)
					}
					return []Destination{renamed}, nil
				default:
					return []Destination{old}, nil
				}
			},
			open: func(_ context.Context, dest Destination, _ int) (Tunnel, error) {
				if dest == old {
					return Tunnel{Done: firstDone, Close: func() {}}, nil
				}
				return stubTunnel(), nil
			},
		})
		defer manager.Close()
		acquire(t, manager, web)
		close(firstDone)
		namedResult, defaultResult := make(chan error, 1), make(chan error, 1)
		go func() {
			_, err := manager.Acquire(t.Context(), web)
			namedResult <- err
		}()
		go func() {
			lease, err := manager.Acquire(t.Context(), request("api"))
			if err == nil && lease.Destination != renamed {
				err = fmt.Errorf("default route used %+v", lease.Destination)
			}
			defaultResult <- err
		}()
		synctest.Wait()
		if len(namedResult) != 0 || len(defaultResult) != 0 {
			t.Fatal("requests did not wait for recovery resolution")
		}
		close(resume)
		if err := <-namedResult; !errors.Is(err, errRouteUnavailable) {
			t.Fatalf("waiting request for the old name did not fail: %v", err)
		}
		if err := <-defaultResult; err != nil {
			t.Fatalf("waiting default request did not follow the rename: %v", err)
		}
		if rows := manager.Snapshot(); len(rows) != 1 || rows[0].PortName != "admin" {
			t.Fatalf("unexpected connections after rename: %+v", rows)
		}
		if _, err := manager.Acquire(t.Context(), web); !errors.Is(err, errRouteUnavailable) {
			t.Fatalf("old name did not fail: %v", err)
		}
		if got := acquire(t, manager, request("api")); got.Destination != renamed {
			t.Fatalf("default route did not find the renamed port: %+v", got.Destination)
		}
	})
}

func TestProbeReconnectUntilExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var probes, opens atomic.Int64
		manager := testManager(stubBackend{open: func(context.Context, Destination, int) (Tunnel, error) {
			opens.Add(1)
			return stubTunnel(), nil
		}})
		manager.UpdatePolicy(settings.PortForward{KeepAlive: true, Minutes: 1, Trigger: settings.LastRequest,
			MinPort: 30000, MaxPort: 30001}, nil)
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

func TestLiveAutoHealingPolicy(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		manager := testManager(stubBackend{})
		defer manager.Close()
		var probes atomic.Int32
		manager.probe = func(context.Context, *Lease) error { probes.Add(1); return nil }
		policy := settings.Defaults("").PortForward
		_ = acquire(t, manager, request("api"))
		policy.CheckSeconds = new(3)
		manager.UpdatePolicy(policy, nil)
		synctest.Wait()
		for range 3 {
			time.Sleep(time.Second)
			_ = acquire(t, manager, request("api"))
		}
		synctest.Wait()
		if probes.Load() != 1 {
			t.Fatalf("expected a probe while requests continue, got %d", probes.Load())
		}
		policy.AutoHealing = new(false)
		manager.UpdatePolicy(policy, nil)
		time.Sleep(20 * time.Second)
		synctest.Wait()
		if probes.Load() != 1 {
			t.Fatalf("disabled policy issued more probes: %d", probes.Load())
		}
	})
}

func TestAutoHealingChecksActiveEphemeralTunnel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		manager := testManager(stubBackend{})
		defer manager.Close()
		var probes atomic.Int32
		manager.probe = func(context.Context, *Lease) error { probes.Add(1); return errors.New("probe failed") }
		policy := settings.Defaults("").PortForward
		policy.KeepAlive, policy.CheckSeconds = false, new(2)
		manager.UpdatePolicy(policy, nil)
		lease, release, err := manager.AcquireRequest(t.Context(), request("api"))
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if probes.Load() != 1 || !lease.isAlive() {
			t.Fatalf("ephemeral probe altered request lifetime: %d", probes.Load())
		}
		release()
		time.Sleep(3 * time.Second)
		synctest.Wait()
		if probes.Load() != 1 {
			t.Fatal("probe survived request completion")
		}
	})
}

func TestDisableAutoHealingCancelsRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		resolving := make(chan struct{})
		canceled := make(chan struct{})
		manager := testManager(stubBackend{resolve: func(ctx context.Context, selector route.Selector, failed string) ([]Destination, error) {
			if failed != "" {
				close(resolving)
				<-ctx.Done()
				close(canceled)
				return nil, ctx.Err()
			}
			return []Destination{stubDestination(selector, "pod")}, nil
		}})
		defer manager.Close()
		policy := settings.Defaults("").PortForward
		manager.probe = func(context.Context, *Lease) error { return errors.New("failed") }
		_ = acquire(t, manager, request("api"))
		<-resolving
		policy.AutoHealing = new(false)
		manager.UpdatePolicy(policy, nil)
		<-canceled
		synctest.Wait()
		if got := acquire(t, manager, request("api")); !got.isAlive() {
			t.Fatal("new request inherited canceled recovery")
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
	manager.UpdatePolicy(settings.PortForward{KeepAlive: true, Minutes: 60, Trigger: settings.LastRequest,
		MinPort: 30000, MaxPort: 30000}, nil)
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
			resolve: func(_ context.Context, selector route.Selector, failed string) ([]Destination, error) {
				if failed == "first" {
					return []Destination{stubDestination(selector, "alternate"), stubDestination(selector, "first")}, nil
				}
				return []Destination{stubDestination(selector, "first")}, nil
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
			resolve: func(_ context.Context, selector route.Selector, failed string) ([]Destination, error) {
				if failed == "" {
					return []Destination{stubDestination(selector, "first")}, nil
				}
				attempts <- time.Now()
				if len(attempts) == 1 {
					return nil, errors.New("Kubernetes service not found")
				}
				return []Destination{stubDestination(selector, "replacement")}, nil
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

func TestConcurrentEphemeralRequestsWaitForPort(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var resolves atomic.Int64
		manager := testManager(stubBackend{resolve: func(_ context.Context, selector route.Selector, _ string) ([]Destination, error) {
			resolves.Add(1)
			return []Destination{stubDestination(selector, "first")}, nil
		}})
		defer manager.Close()
		manager.UpdatePolicy(settings.PortForward{MinPort: 30000, MaxPort: 30000}, nil)
		first, release, err := manager.AcquireRequest(t.Context(), request("api"))
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			second, closeSecond, err := manager.AcquireRequest(t.Context(), request("api"))
			if err == nil {
				defer closeSecond()
				if second == first || second.LocalPort != first.LocalPort {
					err = errors.New("ephemeral port was reused without opening a new tunnel")
				}
			}
			done <- err
		}()
		synctest.Wait()
		if resolves.Load() != 1 {
			t.Fatalf("port waiter started Kubernetes resolution: %d calls", resolves.Load())
		}
		select {
		case err := <-done:
			t.Fatalf("request with no free port did not wait: %v", err)
		default:
		}
		release()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		active, history := manager.RecentHistory()
		if active != 0 || len(history) != 1 || history[0].Service != "api" {
			t.Fatalf("active %d, history %+v", active, history)
		}
	})
}

func TestEphemeralPortReservationReleasedOnLookupFailure(t *testing.T) {
	var calls atomic.Int64
	manager := testManager(stubBackend{resolve: func(_ context.Context, selector route.Selector, _ string) ([]Destination, error) {
		if calls.Add(1) == 1 {
			return nil, errors.New("fixture lookup failed")
		}
		return []Destination{stubDestination(selector, "first")}, nil
	}})
	defer manager.Close()
	manager.UpdatePolicy(settings.PortForward{MinPort: 30000, MaxPort: 30000}, nil)
	if _, _, err := manager.AcquireRequest(t.Context(), request("api")); err == nil {
		t.Fatal("lookup unexpectedly succeeded")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	lease, release, err := manager.AcquireRequest(ctx, request("api"))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if lease.LocalPort != 30000 {
		t.Fatalf("released port was not reused: %d", lease.LocalPort)
	}
}

func TestKeepAliveTimerModes(t *testing.T) {
	for _, trigger := range []string{settings.LastRequest, settings.ForwardOpened} {
		t.Run(trigger, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				manager := testManager(stubBackend{})
				defer manager.Close()
				manager.UpdatePolicy(settings.PortForward{KeepAlive: true, Minutes: 1, Trigger: trigger,
					MinPort: 30000, MaxPort: 30000}, nil)
				first := acquire(t, manager, request("api"))
				time.Sleep(59 * time.Second)
				if second := acquire(t, manager, request("api")); second != first {
					t.Fatal("did not reuse the lease")
				}
				time.Sleep(time.Second)
				synctest.Wait()
				if active := len(manager.Snapshot()) != 0; active != (trigger == settings.LastRequest) {
					t.Fatalf("active = %t, trigger = %s", active, trigger)
				}
			})
		})
	}
}

func TestLiveForwardPolicyRecalculatesActiveLeases(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		manager := testManager(stubBackend{})
		defer manager.Close()
		lease, release, err := manager.AcquireRequest(t.Context(), request("api"))
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Minute)
		manager.UpdatePolicy(settings.PortForward{KeepAlive: true, Minutes: 1, Trigger: settings.ForwardOpened,
			MinPort: 30000, MaxPort: 30001}, nil)
		synctest.Wait()
		if !lease.isAlive() || len(manager.Snapshot()) != 0 {
			t.Fatal("expired lease was retained for new requests or closed during the existing request")
		}
		release()
		synctest.Wait()
		if lease.isAlive() {
			t.Fatal("retired lease survived after the request finished")
		}
	})
}

func TestRecentHistorySurvivesForwardRemoval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		manager := testManager(stubBackend{})
		defer manager.Close()
		manager.UpdatePolicy(settings.PortForward{MinPort: 30000, MaxPort: 30000}, nil)
		for i := range 12 {
			_, release, err := manager.AcquireRequest(t.Context(), request(fmt.Sprintf("svc%d", i)))
			if err != nil {
				t.Fatal(err)
			}
			release()
			time.Sleep(time.Second)
		}
		_, release, err := manager.AcquireRequest(t.Context(), request("svc7"))
		if err != nil {
			t.Fatal(err)
		}
		release()
		active, recent := manager.RecentHistory()
		if active != 0 || len(recent) != 10 || recent[0].Service != "svc7" || recent[1].Service != "svc11" || recent[9].Service != "svc2" {
			t.Fatalf("recent history active=%d rows=%+v", active, recent)
		}
	})
}
