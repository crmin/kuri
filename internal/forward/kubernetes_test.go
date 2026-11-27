package forward

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/crmin/kuri/internal/route"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/clientcmd"
	clientconfig "k8s.io/client-go/tools/clientcmd/api"
)

func TestResolveByServicePortName(t *testing.T) {
	service := corev1.Service{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
		ObjectMeta: metav1.ObjectMeta{Name: "our-product", Namespace: "service"},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "our-product"},
			Ports: []corev1.ServicePort{
				{Name: "web", Protocol: corev1.ProtocolTCP, Port: 3000, TargetPort: intstr.FromInt32(3100)},
				{Name: "admin", Protocol: corev1.ProtocolTCP, Port: 8000, TargetPort: intstr.FromString("admin-pod")},
				{Name: "dns", Protocol: corev1.ProtocolUDP, Port: 53},
			},
		},
	}
	pods := corev1.PodList{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PodList"},
		Items: []corev1.Pod{{
			ObjectMeta: metav1.ObjectMeta{Name: "product-pod", UID: "product-uid"},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Ports: []corev1.ContainerPort{
				{Name: "admin-pod", ContainerPort: 8100, Protocol: corev1.ProtocolTCP},
			}}}},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		}},
	}
	var podLists atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/namespaces/service/services/our-product":
			_ = json.NewEncoder(w).Encode(service)
		case "/api/v1/namespaces/service/pods":
			podLists.Add(1)
			if got := r.URL.Query().Get("labelSelector"); got != "app=our-product" {
				t.Errorf("unexpected Pod selector %q", got)
			}
			_ = json.NewEncoder(w).Encode(pods)
		default:
			t.Errorf("unexpected Kubernetes API request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	kube := &Kubernetes{
		config: &clientconfig.Config{
			Clusters: map[string]*clientconfig.Cluster{"fixture": {Server: server.URL}},
			Contexts: map[string]*clientconfig.Context{"prod": {Cluster: "fixture"}},
		},
		loader: &clientcmd.ClientConfigLoadingRules{},
	}
	contexts := route.NewContexts("prod", []string{"prod"})
	for _, tc := range []struct {
		name      string
		port, pod int32
	}{{"web", 3000, 3100}, {"admin", 8000, 8100}, {"dns", 0, 0}, {"missing", 0, 0}, {"3000", 0, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := contexts.Parse(tc.name + ".our-product.service.prod.kuri.localhost")
			if err != nil {
				t.Fatal(err)
			}
			before := podLists.Load()
			destinations, err := kube.Resolve(t.Context(), req.Selector, "")
			if tc.port == 0 {
				if !errors.Is(err, errRouteUnavailable) || len(destinations) != 0 || podLists.Load() != before {
					t.Fatalf("unexpected resolution: %+v, %v", destinations, err)
				}
				return
			}
			if err != nil || len(destinations) != 1 {
				t.Fatalf("unexpected resolution: %+v, %v", destinations, err)
			}
			dest := destinations[0]
			if dest.Port != tc.port || dest.TargetPort != tc.pod || dest.PortName != tc.name || dest.Pod != "product-pod" || !dest.DefaultPort.IsDefault() {
				t.Fatalf("unexpected destination: %+v", dest)
			}
		})
	}
}

func TestResolvePodTargetPort(t *testing.T) {
	pod := corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{{Ports: []corev1.ContainerPort{
		{Name: "web", ContainerPort: 9090, Protocol: corev1.ProtocolTCP},
	}}}}}
	for _, tc := range []struct {
		name     string
		target   intstr.IntOrString
		headless bool
		want     int32
	}{
		{"number", intstr.FromInt32(8081), false, 8081},
		{"name", intstr.FromString("web"), false, 9090},
		{"default", intstr.IntOrString{}, false, 80},
		{"headless", intstr.FromString("web"), true, 80},
		{"missing", intstr.FromString("missing"), false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := podTargetPort(corev1.ServicePort{Port: 80, TargetPort: tc.target}, &pod, tc.headless)
			if (err != nil) != (tc.want == 0) || got != tc.want {
				t.Fatalf("got %d, %v; want %d", got, err, tc.want)
			}
		})
	}
}

func TestPodPriorityAvoidsFailedReadyPod(t *testing.T) {
	ready := func() corev1.Pod {
		return corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}}}
	}
	failed, different := ready(), ready()
	failed.UID = "failed"
	different.UID = "different"
	if podPriority(&failed, "failed") >= podPriority(&different, "failed") {
		t.Fatal("a failing Ready Pod should rank behind an alternative Ready Pod")
	}
}

func TestExecConfigIsIsolatedAcrossContexts(t *testing.T) {
	makeCluster := func(audience string) *clientconfig.Cluster {
		return &clientconfig.Cluster{
			Server: "https://127.0.0.1:6443",
			Extensions: map[string]runtime.Object{
				"client.authentication.k8s.io/exec": &runtime.Unknown{Raw: []byte(audience)},
			},
		}
	}
	execConfig := &clientconfig.ExecConfig{
		Command: "/bin/false", APIVersion: "client.authentication.k8s.io/v1",
		InteractiveMode: clientconfig.NeverExecInteractiveMode,
	}
	kube := Kubernetes{
		config: &clientconfig.Config{
			Clusters: map[string]*clientconfig.Cluster{
				"a": makeCluster(`{"audience":"a"}`),
				"b": makeCluster(`{"audience":"b"}`),
			},
			Contexts: map[string]*clientconfig.Context{
				"a": {Cluster: "a", AuthInfo: "shared"},
				"b": {Cluster: "b", AuthInfo: "shared"},
			},
			AuthInfos: map[string]*clientconfig.AuthInfo{"shared": {Exec: execConfig}},
		},
		loader: &clientcmd.ClientConfigLoadingRules{},
	}
	first, _, err := kube.client("a")
	if err != nil {
		t.Fatal(err)
	}
	again, _, err := kube.client("a")
	if err != nil || first != again {
		t.Fatalf("context did not reuse its lookup client: %v", err)
	}
	var requests sync.WaitGroup
	for range 8 {
		for _, name := range []string{"a", "b"} {
			requests.Go(func() {
				_, config, err := kube.client(name)
				if err != nil {
					t.Error(err)
					return
				}
				want := `{"audience":"` + name + `"}`
				if got := string(config.ExecProvider.Config.(*runtime.Unknown).Raw); got != want {
					t.Errorf("context %s got %s, want %s", name, got, want)
				}
			})
		}
	}
	requests.Wait()
	if execConfig.Config != nil {
		t.Fatal("client construction modified the shared kubeconfig")
	}
}

func TestPortForwardFailureUsesSafeAPIReasons(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		want   string
	}{
		{"forbidden", http.StatusForbidden, "forbidden (HTTP 403)"},
		{"unauthorized", http.StatusUnauthorized, "authentication failed (HTTP 401)"},
		{"no upgrade", http.StatusOK, "protocol upgrade failed (HTTP 200)"},
		{"API connection", 0, "API connection for port forwarding failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte("sensitive response"))
			}))
			defer server.Close()
			if tc.status == 0 {
				server.Close()
			}
			kube := &Kubernetes{
				config: &clientconfig.Config{
					Clusters: map[string]*clientconfig.Cluster{"test": {Server: server.URL}},
					Contexts: map[string]*clientconfig.Context{"dev": {Cluster: "test"}},
				},
				loader: &clientcmd.ClientConfigLoadingRules{},
			}
			dest := stubDestination(request("test-service").Selector, "test-pod")
			_, err := kube.Open(t.Context(), dest, 30001)
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "sensitive response") {
				t.Fatalf("unexpected failure: %v", err)
			}
		})
	}
}
