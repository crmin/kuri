package daemon

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/crmin/kuri/internal/forward"
	"github.com/crmin/kuri/internal/route"
	"github.com/crmin/kuri/internal/settings"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/streaming/pkg/httpstream"
	"k8s.io/streaming/pkg/httpstream/spdy"
)

func TestLiveAliasAndAutoHealingWithKubernetesPortForward(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	started := make(chan httpstream.Connection, 8)
	probes := make(chan string, 32)
	api := kubernetesPortForwardFixture(t, started, probes)
	app := testApp(t)
	store := settings.NewStore(app.home)
	_, cfg, err := store.Update(ctx, func(c *settings.Config) error {
		c.PortForward.CheckSeconds = new(1)
		c.Kubernetes.MergeAliases(map[string]string{"aws-dev": "first"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	yaml := fmt.Sprintf(`apiVersion: v1
kind: Config
current-context: aws-dev
clusters:
- name: test
  cluster:
    server: %q
contexts:
- name: aws-dev
  context:
    cluster: test
`, api.URL)
	if err := os.WriteFile(cfg.Kubernetes.ConfigPath, []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ready, done := make(chan State, 1), make(chan error, 1)
	go func() {
		done <- app.serve(ctx, foreground, []net.Listener{listener}, nil, func(s State) error { ready <- s; return nil })
	}()
	var state State
	select {
	case state = <-ready:
	case err := <-done:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	request := func(cfg settings.Config, portName string) {
		t.Helper()
		kube, _, err := forward.LoadKubernetes(cfg.Kubernetes.ConfigPath)
		if err != nil {
			t.Fatal(err)
		}
		routes := kube.Routes(cfg.Server, cfg.Kubernetes.ExcludedContexts, cfg.Kubernetes.Aliases())
		_, authority := routes.BookmarkAddress(route.Target{Context: "aws-dev", Namespace: "default", Service: "api"}, portName)
		if authority == "" {
			t.Fatal("bookmark did not yield a host")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+listener.Addr().String()+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = authority
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != http.StatusOK || string(body) != req.Host {
			t.Fatalf("forwarded %s: status %d, body %q, err %v", req.Host, response.StatusCode, body, err)
		}
	}
	awaitConnection := func() httpstream.Connection {
		t.Helper()
		select {
		case connection := <-started:
			return connection
		case <-ctx.Done():
			t.Fatal("port forward was not established")
			return nil
		}
	}
	request(cfg, "")
	first := awaitConnection()
	request(cfg, "http")
	_, next, err := store.Update(ctx, func(c *settings.Config) error {
		c.Kubernetes.MergeAliases(map[string]string{"aws-dev": "second"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.ApplySettings(ctx, state, next.Fingerprint(), false, state.KubeconfigFingerprint); err != nil {
		t.Fatal(err)
	}
	request(next, "")
	for host := ""; host != "api.default.second.kuri.localhost"; {
		select {
		case host = <-probes:
		case <-ctx.Done():
			t.Fatal("no connection check with the new host at the configured interval")
		}
	}
	select {
	case <-started:
		t.Fatal("changing the alias replaced the existing forward")
	default:
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	_ = awaitConnection()
	request(next, "http")
}

func kubernetesPortForwardFixture(t *testing.T, started chan<- httpstream.Connection, probes chan<- string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/api/v1/namespaces/default/services/api":
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"kind":"Service","apiVersion":"v1","spec":{"selector":{"app":"test"},"ports":[{"name":"http","port":80}]}}`)
		case "/api/v1/namespaces/default/pods":
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"kind":"PodList","apiVersion":"v1","items":[{"metadata":{"name":"test","uid":"test"},"status":{"phase":"Running"}}]}`)
		case "/api/v1/namespaces/default/pods/test/portforward":
			if strings.EqualFold(req.Header.Get("Upgrade"), "websocket") {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if _, err := httpstream.Handshake(req, w, []string{portforward.PortForwardProtocolV1Name}); err != nil {
				t.Error(err)
				return
			}
			connection := spdy.NewResponseUpgrader().UpgradeResponse(w, req, func(stream httpstream.Stream, replySent <-chan struct{}) error {
				go func() {
					<-replySent
					defer stream.Close()
					if stream.Headers().Get(corev1.StreamType) != corev1.StreamTypeData {
						return
					}
					forwarded, err := http.ReadRequest(bufio.NewReader(stream))
					if err != nil {
						return
					}
					defer forwarded.Body.Close()
					body := forwarded.Host
					if forwarded.Method == http.MethodHead {
						select {
						case probes <- forwarded.Host:
						default:
						}
						body = ""
					}
					fmt.Fprintf(stream, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
				}()
				return nil
			})
			if connection != nil {
				started <- connection
			}
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(server.Close)
	return server
}
