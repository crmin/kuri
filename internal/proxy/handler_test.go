package proxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/crmin/kuri/internal/accesslog"
	"github.com/crmin/kuri/internal/forward"
	"github.com/crmin/kuri/internal/route"
	"github.com/crmin/kuri/internal/settings"
)

type httpBackend struct{ upstream http.Handler }

func (b httpBackend) Resolve(_ context.Context, selector route.Selector, _ string) ([]forward.Destination, error) {
	target := selector.Target
	portName := selector.PortName
	defaultPort := route.Selector{}
	var podPort int32
	switch portName {
	case "", "http":
		portName, target.Port, podPort = "http", 8080, 80
		defaultPort = route.ResolvedSelector(target, portName)
	case "web":
		target.Port, podPort = 3000, 3000
	case "admin":
		target.Port, podPort = 8000, 8000
	default:
		return nil, fmt.Errorf("service has no TCP port named %q", portName)
	}
	return []forward.Destination{{
		Target: target, PortName: portName, DefaultPort: defaultPort, Pod: "pod", TargetPort: podPort,
	}}, nil
}

func (b httpBackend) Open(_ context.Context, destination forward.Destination, port int) (forward.Tunnel, error) {
	listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return forward.Tunnel{}, err
	}
	upstream := b.upstream
	if upstream == nil {
		upstream = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprintf(w, "%s:%d", destination.PortName, destination.TargetPort)
		})
	}
	server := &http.Server{Handler: upstream}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Serve(listener)
	}()
	return forward.Tunnel{Done: done, Close: func() { _ = server.Close(); <-done }}, nil
}

type testLog struct{ events []accesslog.Event }

type aliasConnector struct{ requests []route.Request }

func (c *aliasConnector) AcquireRequest(_ context.Context, req route.Request) (*forward.Lease, func(), error) {
	c.requests = append(c.requests, req)
	return nil, nil, fmt.Errorf("service unavailable in %s", req.Context)
}

func TestProxyAliasMissDoesNotTryContextName(t *testing.T) {
	cfg := settings.Defaults("")
	connector := &aliasConnector{}
	for _, disabled := range [][]string{nil, {"dev-1"}} {
		contexts := route.NewContextRoutes("dev", []string{"dev", "dev-1"}, cfg.Server, disabled, map[string]string{"dev-1": "dev"})
		handler := New(contexts, connector, &testLog{}, func(err error) { t.Error(err) })
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://a.default.dev.kuri.localhost", nil))
		if len(connector.requests) != 1 || connector.requests[0].Context != "dev-1" {
			t.Fatalf("alias matched a different context or tried more than once: %+v", connector.requests)
		}
	}
}

func (l *testLog) Write(event accesslog.Event) error {
	l.events = append(l.events, event)
	return nil
}

func proxyHandler(t *testing.T, upstream http.HandlerFunc) (*Handler, *testLog) {
	t.Helper()
	manager := forward.NewManager(httpBackend{upstream: upstream})
	t.Cleanup(manager.Close)
	log := &testLog{}
	return New(route.NewContexts("dev", []string{"dev"}), manager, log, func(err error) { t.Error(err) }), log
}

func TestProxyPreservesRequest(t *testing.T) {
	handler, log := proxyHandler(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_, _ = fmt.Fprintf(w, "%s %s %s %s %s", r.Method, r.URL.RequestURI(), r.Host, r.Header.Get("X-Forwarded-Host"), body)
	})
	req := httptest.NewRequest(http.MethodPost, "http://api.default.kuri.localhost/test%2Fpath?x=1;x=2", strings.NewReader("payload"))
	req.Header.Set("X-Forwarded-Host", "spoofed")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if got := resp.Body.String(); got != "POST /test%2Fpath?x=1;x=2 api.default.kuri.localhost api.default.kuri.localhost payload" {
		t.Fatalf("unexpected upstream request: %q", got)
	}
	entry := log.events[0]
	if entry.Success == nil || !*entry.Success || entry.Domain != req.Host || entry.Connection == nil ||
		entry.ConfigID == "" || entry.Alias != req.Host || entry.Context != "dev" || entry.Namespace != "default" ||
		entry.Service != "api" || entry.RemotePort != 8080 || entry.Protocol != "tcp" ||
		entry.LocalAddr != "127.0.0.1" || entry.LocalPort < 30000 || entry.LocalPort >= 40000 {
		t.Fatalf("missing or invalid request metadata: %+v", entry)
	}
}

func TestProxyRoutesNamedServicePorts(t *testing.T) {
	for _, keepAlive := range []bool{true, false} {
		t.Run(fmt.Sprintf("keep-alive=%t", keepAlive), func(t *testing.T) {
			cfg := settings.Defaults("")
			cfg.PortForward.KeepAlive = keepAlive
			manager := forward.NewManagerWithPolicy(httpBackend{}, cfg.PortForward)
			t.Cleanup(manager.Close)
			log := &testLog{}
			contexts := route.NewConfiguredContexts("prod", []string{"prod"}, cfg.Server)
			handler := New(contexts, manager, log, func(err error) { t.Error(err) })
			request := func(handler *Handler, name string) *httptest.ResponseRecorder {
				resp := httptest.NewRecorder()
				url := "http://" + name + ".our-product.service.prod.kuri.localhost/"
				handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, url, nil))
				return resp
			}
			for _, port := range []struct {
				name   string
				number int32
			}{{"web", 3000}, {"admin", 8000}} {
				resp := request(handler, port.name)
				if want := fmt.Sprintf("%s:%d", port.name, port.number); resp.Code != http.StatusOK || resp.Body.String() != want {
					t.Fatalf("%s: HTTP %d, %q; want %q", port.name, resp.Code, resp.Body.String(), want)
				}
				conn := log.events[len(log.events)-1].Connection
				if conn == nil || conn.RemotePort != port.number || conn.Context != "prod" || conn.Service != "our-product" || conn.Namespace != "service" {
					t.Fatalf("%s: bad connection metadata: %+v", port.name, conn)
				}
			}
			if rows := manager.Snapshot(); (keepAlive && len(rows) != 2) || (!keepAlive && len(rows) != 0) {
				t.Fatalf("unexpected active forwards: %+v", rows)
			}
			for _, name := range []string{"missing", "3000"} {
				if resp := request(handler, name); resp.Code != http.StatusBadGateway {
					t.Errorf("%s: HTTP %d, want 502", name, resp.Code)
				}
			}
			cfg.Server.Styles.ExplicitPort = false
			disabled := New(route.NewConfiguredContexts("prod", []string{"prod"}, cfg.Server), manager, log, func(err error) { t.Error(err) })
			if resp := request(disabled, "web"); resp.Code != http.StatusBadRequest {
				t.Errorf("disabled named style: HTTP %d, want 400", resp.Code)
			}
		})
	}
}

func TestEphemeralForwardClosesAfterResponse(t *testing.T) {
	policy := settings.Defaults("").PortForward
	policy.KeepAlive = false
	manager := forward.NewManagerWithPolicy(httpBackend{upstream: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "completed")
	})}, policy)
	t.Cleanup(manager.Close)
	logger := &testLog{}
	handler := New(route.NewContexts("dev", []string{"dev"}), manager, logger, func(err error) { t.Error(err) })
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://api.default.kuri.localhost/", nil))
	if response.Body.String() != "completed" || len(manager.Snapshot()) != 0 {
		t.Fatalf("request was unfinished or its tunnel was retained: %s %+v", response.Body, manager.Snapshot())
	}
	active, recent := manager.RecentHistory()
	if active != 0 || len(recent) != 1 || recent[0].Service != "api" {
		t.Fatalf("unexpected recent state: %d %+v", active, recent)
	}
}

func TestUpstreamErrorVsResponse(t *testing.T) {
	handler, log := proxyHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/reject" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	})
	for _, tc := range []struct {
		url     string
		code    int
		success bool
	}{
		{"http://api.default.kuri.localhost/reject", 503, true},
		{"http://api.default.kuri.localhost/close", 502, false},
		{"http://unknown.example.com/", 400, false},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.url, nil)
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		entry := log.events[len(log.events)-1]
		if resp.Code != tc.code || entry.Success == nil || *entry.Success != tc.success ||
			(tc.success && entry.Connection == nil) || (!tc.success && (entry.Connection != nil || entry.Reason == "")) {
			t.Fatalf("url=%s status=%d log=%+v", tc.url, resp.Code, entry)
		}
	}
}
