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

func (b httpBackend) Resolve(_ context.Context, target route.Target, _ string) ([]forward.Destination, error) {
	if target.Port == 0 {
		target.Port = 8080
	}
	return []forward.Destination{{
		Target: target, PortName: "http", DefaultPort: 8080, Pod: "pod", TargetPort: 80,
	}}, nil
}

func (b httpBackend) Open(_ context.Context, _ forward.Destination, port int) (forward.Tunnel, error) {
	listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return forward.Tunnel{}, err
	}
	server := &http.Server{Handler: b.upstream}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Serve(listener)
	}()
	return forward.Tunnel{Done: done, Close: func() { _ = server.Close(); <-done }}, nil
}

type testLog struct{ events []accesslog.Event }

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
