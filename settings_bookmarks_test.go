package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/crmin/kuri/internal/settings"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func bookmarkTestConfig(t *testing.T, home, server string) string {
	t.Helper()
	path := filepath.Join(home, "kubeconfig")
	config := fmt.Sprintf(`apiVersion: v1
kind: Config
current-context: dev
clusters:
- name: test
  cluster: {server: %q}
contexts:
- name: dev
  context: {cluster: test}
- name: prod
  context: {cluster: test}
`, server)
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	store := settings.NewStore(home)
	_, _, err := store.Update(t.Context(), func(cfg *settings.Config) error {
		cfg.Kubernetes.ConfigPath = path
		cfg.Kubernetes.MergeAliases(map[string]string{"prod": "live"})
		cfg.Server.Port = 18081
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAddBookmarkDuplicateTarget(t *testing.T) {
	home := t.TempDir()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/namespaces/default/services/api" {
			t.Errorf("unexpected Kubernetes request %s %s", r.Method, r.URL)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(corev1.Service{TypeMeta: metav1.TypeMeta{Kind: "Service", APIVersion: "v1"},
			Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: "http", Port: 80}}}})
	}))
	defer server.Close()
	path := bookmarkTestConfig(t, home, server.URL)
	run := func(req bookmarkRequest) bookmarkReply {
		return handleBookmarks(t.Context(), settings.NewStore(home), req)
	}
	contexts := run(bookmarkRequest{Operation: "contexts"})
	target := settings.BookmarkTarget{Context: "prod", Namespace: "default", Service: "api"}
	req := bookmarkRequest{Operation: "add", Name: "API", BookmarkTarget: target, KubeconfigFingerprint: contexts.KubeconfigFingerprint, KubeconfigPath: path}
	var wg sync.WaitGroup
	results := make(chan bookmarkReply, 4)
	for range cap(results) {
		wg.Go(func() { results <- run(req) })
	}
	wg.Wait()
	close(results)
	added := 0
	for reply := range results {
		if reply.Error != "" || (!reply.Changed && !reply.Duplicate) {
			t.Fatalf("add: %+v", reply)
		}
		if reply.Changed {
			added++
		}
	}
	if added != 1 {
		t.Fatalf("successful additions: %d", added)
	}
	req.PortName = "http"
	if reply := run(req); reply.Error != "" || !reply.Changed || len(reply.Items) != 2 {
		t.Fatalf("same name with explicit port: %+v", reply)
	}
	file := filepath.Join(home, ".kuri", "bookmarks.yaml")
	before, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	count := requests.Load()
	req.BookmarkTarget, req.Name, req.KubeconfigPath = target, "Renamed", "/another/config"
	if reply := run(req); !reply.Duplicate || reply.Changed || reply.Error != "" {
		t.Fatalf("duplicate: %+v", reply)
	}
	list := run(bookmarkRequest{Operation: "list"})
	if len(list.Items) != 2 || list.Items[0].Name != "API" || list.Items[0].PortName != "" || list.Items[1].PortName != "http" {
		t.Fatalf("stored items: %+v", list)
	}
	after, err := os.Stat(file)
	if err != nil || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) || requests.Load() != count {
		t.Fatalf("duplicate performed another lookup or write: %v", err)
	}
}

func TestBookmarkCatalogQueries(t *testing.T) {
	home := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method %s", r.Method)
		}
		switch r.URL.Path {
		case "/api/v1/namespaces":
			list := corev1.NamespaceList{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "NamespaceList"}}
			if r.URL.Query().Get("limit") != "200" {
				t.Errorf("missing page size: %s", r.URL)
			}
			switch r.URL.Query().Get("continue") {
			case "":
				list.Items = []corev1.Namespace{{ObjectMeta: metav1.ObjectMeta{Name: "z"}}}
				list.Continue = "next"
			case "next":
				list.Items = []corev1.Namespace{{ObjectMeta: metav1.ObjectMeta{Name: "a"}}}
			default:
				t.Errorf("unexpected namespace page: %s", r.URL)
			}
			_ = json.NewEncoder(w).Encode(list)
		case "/api/v1/namespaces/a/services":
			_ = json.NewEncoder(w).Encode(corev1.ServiceList{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ServiceList"},
				Items: []corev1.Service{{ObjectMeta: metav1.ObjectMeta{Name: "api"}}}})
		case "/api/v1/namespaces/a/services/api":
			_ = json.NewEncoder(w).Encode(corev1.Service{TypeMeta: metav1.TypeMeta{Kind: "Service", APIVersion: "v1"},
				Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: "web", Port: 80}, {Name: "admin", Port: 81},
					{Name: "dns", Port: 53, Protocol: corev1.ProtocolUDP}}}})
		case "/api/v1/namespaces/locked/services":
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(metav1.Status{TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"}, Reason: metav1.StatusReasonForbidden})
		default:
			t.Errorf("unexpected request %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	path := bookmarkTestConfig(t, home, server.URL)
	store := settings.NewStore(home)
	contexts := handleBookmarks(t.Context(), store, bookmarkRequest{Operation: "contexts"})
	if contexts.Error != "" || !slices.Equal(contexts.Options, []string{"dev", "prod"}) {
		t.Fatalf("contexts: %+v", contexts)
	}
	req := bookmarkRequest{KubeconfigPath: path, KubeconfigFingerprint: contexts.KubeconfigFingerprint,
		BookmarkTarget: settings.BookmarkTarget{Context: "prod", Namespace: "a", Service: "api"}}
	for _, tc := range []struct {
		operation string
		want      []string
	}{{"namespaces", []string{"a", "z"}}, {"services", []string{"api"}}, {"ports", []string{"admin", "web"}}} {
		req.Operation = tc.operation
		reply := handleBookmarks(t.Context(), store, req)
		if reply.Error != "" || !slices.Equal(reply.Options, tc.want) {
			t.Fatalf("%s: %+v", tc.operation, reply)
		}
	}
	req.Operation, req.Namespace = "services", "locked"
	reply := handleBookmarks(t.Context(), store, req)
	if reply.Error != "list services: Forbidden" {
		t.Fatalf("forbidden: %+v", reply)
	}
}

func TestBookmarkCatalogDeadline(t *testing.T) {
	serverCanceled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		close(serverCanceled)
	}))
	defer server.Close()
	home := t.TempDir()
	path := bookmarkTestConfig(t, home, server.URL)
	store := settings.NewStore(home)
	contexts := handleBookmarks(t.Context(), store, bookmarkRequest{Operation: "contexts"})
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	reply := handleBookmarks(ctx, store, bookmarkRequest{
		Operation: "namespaces", BookmarkTarget: settings.BookmarkTarget{Context: "prod"},
		KubeconfigPath: path, KubeconfigFingerprint: contexts.KubeconfigFingerprint,
	})
	if reply.Error != context.DeadlineExceeded.Error() || len(reply.Options) != 0 {
		t.Fatalf("lookup did not return a deadline error: %+v", reply)
	}
	select {
	case <-serverCanceled:
	case <-time.After(time.Second):
		t.Fatal("server did not observe the canceled lookup")
	}
}

func TestBookmarkDeadlineStopsCredentialGroup(t *testing.T) {
	home := t.TempDir()
	binary := filepath.Join(home, "kuri")
	if out, err := exec.CommandContext(t.Context(), "go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build kuri: %v\n%s", err, out)
	}
	path := bookmarkTestConfig(t, home, "https://127.0.0.1:1")
	pidsPath := filepath.Join(home, "credential.pids")
	config, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	kubeconfig := strings.ReplaceAll(string(config), "context: {cluster: test}", "context: {cluster: test, user: slow}")
	kubeconfig += fmt.Sprintf(`users:
- name: slow
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1
      interactiveMode: Never
      command: /bin/sh
      args:
      - -c
      - 'sleep 30 & printf "%%s %%s\n" "$$" "$!" > "$1"; wait'
      - plugin
      - %q
`, pidsPath)
	if err := os.WriteFile(path, []byte(kubeconfig), 0600); err != nil {
		t.Fatal(err)
	}
	store := settings.NewStore(home)
	contexts := handleBookmarks(t.Context(), store, bookmarkRequest{Operation: "contexts"})
	if contexts.Error != "" {
		t.Fatal(contexts.Error)
	}
	payload, err := json.Marshal(bookmarkRequest{
		Operation: "namespaces", BookmarkTarget: settings.BookmarkTarget{Context: "prod"},
		KubeconfigPath: path, KubeconfigFingerprint: contexts.KubeconfigFingerprint,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "__bookmarks")
	cmd.WaitDelay = time.Second
	cmd.Env = append(os.Environ(), "HOME="+home, "TMPDIR="+home)
	cmd.Stdin = bytes.NewReader(payload)
	out, runErr := cmd.Output()

	ids, err := os.ReadFile(pidsPath)
	if err != nil {
		t.Fatalf("credential plugin did not start: %v (CLI: %v, %s)", err, runErr, out)
	}
	pids := strings.Fields(string(ids))
	if len(pids) != 2 {
		t.Fatalf("expected the plugin and its child: %q", ids)
	}
	for _, id := range pids {
		pid, err := strconv.Atoi(id)
		if err != nil || pid <= 1 {
			t.Fatalf("invalid fixture PID %q", id)
		}
		status, _ := exec.Command("/bin/ps", "-p", id, "-o", "stat=").Output()
		if state := strings.TrimSpace(string(status)); state != "" && !strings.HasPrefix(state, "Z") {
			if proc, err := os.FindProcess(pid); err == nil {
				_ = proc.Kill()
			}
			t.Errorf("credential process %d survived: %s", pid, state)
		}
	}
	var reply bookmarkReply
	if runErr != nil || json.Unmarshal(out, &reply) != nil || reply.Error != context.DeadlineExceeded.Error() {
		t.Fatalf("expected a bounded CLI deadline reply: %v, %s", runErr, out)
	}
	matches, err := filepath.Glob(filepath.Join(home, ".credential-*"))
	if err != nil || len(matches) > 0 {
		t.Fatalf("credential session was not removed: %v %v", matches, err)
	}
}

func TestAddBookmarkValidatesServicePort(t *testing.T) {
	for _, tc := range []struct {
		name, selection, wantError string
		ports                      []corev1.ServicePort
	}{
		{name: "unnamed single TCP", ports: []corev1.ServicePort{{Port: 80}}},
		{name: "http default", ports: []corev1.ServicePort{{Name: "web", Port: 81}, {Name: "http", Port: 80}}},
		{name: "explicit name", selection: "web", ports: []corev1.ServicePort{{Name: "web", Port: 81}, {Name: "admin", Port: 80}}},
		{name: "multiple ports", wantError: "This service has multiple ports. Select a Port Name.",
			ports: []corev1.ServicePort{{Name: "web", Port: 81}, {Name: "admin", Port: 80}}},
		{name: "UDP only", wantError: "This service has no TCP ports. Choose another service.",
			ports: []corev1.ServicePort{{Name: "http", Port: 80, Protocol: corev1.ProtocolUDP}}},
		{name: "removed name", selection: "web", wantError: "The selected TCP port is unavailable. Choose another Port Name.",
			ports: []corev1.ServicePort{{Name: "admin", Port: 80}}},
		{name: "no ports", wantError: "This service has no TCP ports. Choose another service."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/namespaces/default/services/api" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(corev1.Service{TypeMeta: metav1.TypeMeta{Kind: "Service", APIVersion: "v1"},
					Spec: corev1.ServiceSpec{Ports: tc.ports}})
			}))
			defer server.Close()
			home := t.TempDir()
			path := bookmarkTestConfig(t, home, server.URL)
			store := settings.NewStore(home)
			contexts := handleBookmarks(t.Context(), store, bookmarkRequest{Operation: "contexts"})
			req := bookmarkRequest{Operation: "add", KubeconfigPath: path, KubeconfigFingerprint: "stale",
				BookmarkTarget: settings.BookmarkTarget{Context: "prod", Namespace: "default", Service: "api", PortName: tc.selection}}
			if reply := handleBookmarks(t.Context(), store, req); !reply.Refresh || reply.Changed {
				t.Fatalf("stale config: %+v", reply)
			}
			req.KubeconfigFingerprint = contexts.KubeconfigFingerprint
			reply := handleBookmarks(t.Context(), store, req)
			if reply.Error != tc.wantError || reply.Changed != (tc.wantError == "") {
				t.Fatalf("add: %+v", reply)
			}
			items, err := store.ReadBookmarks()
			if err != nil || (len(items) == 1) != reply.Changed {
				t.Fatalf("stored items: %+v %v", items, err)
			}
		})
	}
}

func TestBookmarkMenuWithoutDaemon(t *testing.T) {
	home := t.TempDir()
	path := bookmarkTestConfig(t, home, "http://127.0.0.1:1")
	store := settings.NewStore(home)
	item := settings.Bookmark{KubeconfigPath: path, Name: "API",
		BookmarkTarget: settings.BookmarkTarget{Context: "prod", Namespace: "default", Service: "api", PortName: "web"}}
	if _, changed, err := store.AddBookmark(t.Context(), item, nil); err != nil || !changed {
		t.Fatalf("seed: changed=%v err=%v", changed, err)
	}
	reply := handleBookmarks(t.Context(), store, bookmarkRequest{Operation: "menu"})
	if reply.Error != "" || len(reply.Rows) != 1 || reply.Rows[0].URL != "http://web.api.default.live.kuri.localhost:18081" {
		t.Fatalf("menu: %+v", reply)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	reply = handleBookmarks(t.Context(), store, bookmarkRequest{Operation: "menu"})
	if len(reply.Rows) != 1 || reply.Rows[0].URL != "" || reply.Rows[0].Unavailable == "" {
		t.Fatalf("missing kubeconfig: %+v", reply)
	}
}
