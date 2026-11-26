package route

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestHostForms(t *testing.T) {
	contexts := NewContexts("dev", []string{"dev", "prod"})
	for _, tc := range []struct {
		host, context, service, namespace, normal string
		port                                      int32
	}{
		{"API.Default.Prod.KURI.LOCALHOST.:80", "prod", "api", "default", "api.default.prod.kuri.localhost", 0},
		{"8080.api.default.prod.kuri.localhost", "prod", "api", "default", "8080.api.default.prod.kuri.localhost", 8080},
		{"api.default.kuri.localhost", "dev", "api", "default", "api.default.kuri.localhost", 0},
		{"api.default.unknown.kuri.localhost", "", "", "", "", 0},
		{"api.bad_ns.kuri.localhost", "", "", "", "", 0},
		{"api.default.kuri.localhost:81", "", "", "", "", 0},
		{"http://api.default.kuri.localhost", "", "", "", "", 0},
		{"+8.api.default.dev.kuri.localhost", "", "", "", "", 0},
		{"65536.api.default.dev.kuri.localhost", "", "", "", "", 0},
		{"api.default.kuri.localhost..", "", "", "", "", 0},
		{"api.default.kuri.localhost:", "", "", "", "", 0},
	} {
		t.Run(tc.host, func(t *testing.T) {
			r, err := contexts.Parse(tc.host)
			if tc.context == "" {
				if err == nil {
					t.Fatalf("accepted invalid host: %+v", r)
				}
				return
			}
			want := Request{Target: Target{tc.context, tc.namespace, tc.service, tc.port}, Host: tc.normal}
			if err != nil || r != want {
				t.Fatalf("got %+v, %v; want %+v", r, err, want)
			}
		})
	}
	if _, err := NewContexts("", []string{"dev"}).Parse("api.default.kuri.localhost"); err == nil {
		t.Fatal("accepted current-context routing without current-context")
	}
}

func TestContextAliases(t *testing.T) {
	name := "arn:aws:eks:prod"
	alias := Alias(name)
	request, err := NewContexts(name, []string{name}).Parse("api.default." + alias + ".kuri.localhost")
	if err != nil || request.Context != name {
		t.Fatalf("alias %q: %+v, %v", alias, request, err)
	}
	contexts := NewContexts(name, []string{name, alias})
	if _, err := contexts.Parse("api.default." + alias + ".kuri.localhost"); err == nil {
		t.Fatal("accepted an ambiguous alias")
	}
	if _, err := contexts.Parse("api.default.kuri.localhost"); err != nil {
		t.Fatalf("ambiguous explicit alias prevented current-context routing: %v", err)
	}
}

func TestChooseServicePort(t *testing.T) {
	httpPort := corev1.ServicePort{Name: "http", Port: 80, Protocol: corev1.ProtocolTCP}
	other := corev1.ServicePort{Name: "app", Port: 8080}
	udp := corev1.ServicePort{Name: "dns", Port: 8080, Protocol: corev1.ProtocolUDP}
	for _, tc := range []struct {
		name          string
		ports         []corev1.ServicePort
		request, want int32
	}{
		{"single", []corev1.ServicePort{other}, 0, 8080},
		{"named http", []corev1.ServicePort{other, httpPort}, 0, 80},
		{"missing http", []corev1.ServicePort{other, udp}, 0, 0},
		{"explicit", []corev1.ServicePort{udp, other, httpPort}, 8080, 8080},
		{"missing explicit", []corev1.ServicePort{other, httpPort}, 123, 0},
		{"UDP", []corev1.ServicePort{udp}, 0, 0},
		{"no ports", nil, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			port, err := ChooseServicePort(tc.ports, tc.request)
			if (err != nil) != (tc.want == 0) || port.Port != tc.want {
				t.Fatalf("got %+v, %v; want %d", port, err, tc.want)
			}
		})
	}
}
