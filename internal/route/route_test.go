package route

import (
	"testing"

	"github.com/crmin/kuri/internal/settings"
	corev1 "k8s.io/api/core/v1"
)

func TestContextAliasPrecedence(t *testing.T) {
	server := settings.Defaults("").Server
	names := []string{"dev-1", "dev", "aws-prod"}
	aliases := map[string]string{"dev-1": "dev", "aws-prod": "prod"}
	for _, excluded := range [][]string{nil, {"dev-1"}} {
		contexts := NewContextRoutes("dev", names, server, excluded, aliases)
		target, err := contexts.Parse("a.default.dev.kuri.localhost")
		if len(excluded) == 0 && (err != nil || target.Context != "dev-1") {
			t.Fatalf("custom alias did not win: %+v %v", target, err)
		}
		if len(excluded) != 0 && err == nil {
			t.Fatalf("disabled alias fell through: %+v", target)
		}
		if _, err := contexts.Parse("a.default.aws-prod.kuri.localhost"); err == nil {
			t.Fatal("accepted original address for a renamed context")
		}
		if _, host := contexts.ForwardAddress(Target{Context: "aws-prod", Namespace: "default", Service: "a"}, "https", false); host != "https.a.default.prod.kuri.localhost" {
			t.Fatalf("copy address: %s", host)
		}
	}
}

func TestForwardHostMatchesExplicitContext(t *testing.T) {
	target := Target{Context: "cloud.prod", Namespace: "ns", Service: "api", Port: 8080}
	alias := Alias(target.Context)
	contexts := NewContexts(target.Context, []string{target.Context})
	if _, got := contexts.ForwardAddress(target, "web", false); got != "web.api.ns."+alias+".kuri.localhost" {
		t.Fatal(got)
	}
	if _, got := contexts.ForwardAddress(target, "web", true); got != "api.ns."+alias+".kuri.localhost" {
		t.Fatal(got)
	}
	ambiguous := NewContexts(target.Context, []string{target.Context, alias})
	if _, err := ambiguous.Parse("api.ns.kuri.localhost"); err != nil {
		t.Fatal(err)
	}
	if _, got := ambiguous.ForwardAddress(target, "web", true); got != "api.ns.kuri.localhost" {
		t.Fatal(got)
	}
	if label, got := ambiguous.ForwardAddress(target, "web", false); label != "web.api.ns."+alias || got != "" {
		t.Fatalf("ambiguous named address: %q, %q", label, got)
	}
}

func TestHostForms(t *testing.T) {
	contexts := NewContexts("dev", []string{"dev", "prod"})
	for _, tc := range []struct {
		host, context, service, namespace, normal, portName string
	}{
		{"API.Default.Prod.KURI.LOCALHOST.:80", "prod", "api", "default", "api.default.prod.kuri.localhost", ""},
		{"WEB.our-product.service.prod.kuri.localhost:8081", "prod", "our-product", "service", "web.our-product.service.prod.kuri.localhost", "web"},
		{"admin.our-product.service.prod.kuri.localhost", "prod", "our-product", "service", "admin.our-product.service.prod.kuri.localhost", "admin"},
		{"65536.api.default.dev.kuri.localhost", "dev", "api", "default", "65536.api.default.dev.kuri.localhost", "65536"},
		{"api.default.kuri.localhost", "dev", "api", "default", "api.default.kuri.localhost", ""},
		{"api.default.unknown.kuri.localhost", "", "", "", "", ""},
		{"api.bad_ns.kuri.localhost", "", "", "", "", ""},
		{"api.default.kuri.localhost:81", "dev", "api", "default", "api.default.kuri.localhost", ""},
		{"http://api.default.kuri.localhost", "", "", "", "", ""},
		{"+8.api.default.dev.kuri.localhost", "", "", "", "", ""},
		{"bad_name.api.default.dev.kuri.localhost", "", "", "", "", ""},
		{"api.default.kuri.localhost..", "", "", "", "", ""},
		{"api.default.kuri.localhost:", "", "", "", "", ""},
	} {
		t.Run(tc.host, func(t *testing.T) {
			r, err := contexts.Parse(tc.host)
			if tc.context == "" {
				if err == nil {
					t.Fatalf("accepted invalid host: %+v", r)
				}
				return
			}
			want := Request{Selector: Selector{Target: Target{tc.context, tc.namespace, tc.service, 0}, PortName: tc.portName}, Host: tc.normal}
			if err != nil || r != want {
				t.Fatalf("got %+v, %v; want %+v", r, err, want)
			}
		})
	}
	if _, err := NewContexts("", []string{"dev"}).Parse("api.default.kuri.localhost"); err == nil {
		t.Fatal("accepted current-context routing without current-context")
	}
}

func TestConfiguredDomainStylesAndCopyURL(t *testing.T) {
	server := settings.Server{Domain: "example.com", Port: 8081,
		Styles: settings.DomainStyles{CurrentContext: true}}
	target := Target{Context: "dev", Namespace: "default", Service: "api", Port: 80}
	contexts := NewConfiguredContexts("dev", []string{"dev"}, server)
	if _, got := contexts.ForwardAddress(target, "http", true); got != "api.default.example.com:8081" {
		t.Fatalf("copy address %q", got)
	}
	for _, host := range []string{"api.default.dev.example.com", "http.api.default.dev.example.com", "api.default.kuri.localhost"} {
		if _, err := contexts.Parse(host); err == nil {
			t.Fatalf("accepted disabled host %q", host)
		}
	}
	if _, err := NewConfiguredContexts("dev", []string{"prod"}, server).Parse("api.default.example.com"); err == nil {
		t.Fatal("implicit host reached an excluded current context")
	}
	server.Styles = settings.DomainStyles{ExplicitPort: true}
	contexts = NewConfiguredContexts("dev", []string{"dev"}, server)
	if _, got := contexts.ForwardAddress(target, "http", true); got != "http.api.default.dev.example.com:8081" {
		t.Fatalf("copy fallback %q", got)
	}
	if label, got := contexts.ForwardAddress(target, "", true); label != "api.default.dev" || got != "" {
		t.Fatalf("unnamed port should show a label without a copy address: %q, %q", label, got)
	}
	if _, got := NewConfiguredContexts("dev", []string{"prod"}, server).ForwardAddress(target, "http", true); got != "" {
		t.Fatalf("copy address for excluded context %q", got)
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
	sctp := corev1.ServicePort{Name: "control", Port: 9000, Protocol: corev1.ProtocolSCTP}
	unnamed := corev1.ServicePort{Port: 8080}
	numericName := corev1.ServicePort{Name: "8080", Port: 81}
	for _, tc := range []struct {
		name      string
		ports     []corev1.ServicePort
		requested Selector
		want      int32
	}{
		{"single", []corev1.ServicePort{other}, Selector{}, 8080},
		{"single unnamed", []corev1.ServicePort{unnamed}, Selector{}, 8080},
		{"named http", []corev1.ServicePort{other, httpPort}, Selector{}, 80},
		{"missing http", []corev1.ServicePort{other, udp}, Selector{}, 0},
		{"explicit name", []corev1.ServicePort{udp, other, httpPort}, Selector{PortName: "app"}, 8080},
		{"literal numeric name", []corev1.ServicePort{other, numericName}, Selector{PortName: "8080"}, 81},
		{"old numeric address", []corev1.ServicePort{other}, Selector{PortName: "8080"}, 0},
		{"missing name on single port", []corev1.ServicePort{unnamed}, Selector{PortName: "app"}, 0},
		{"UDP", []corev1.ServicePort{udp}, Selector{PortName: "dns"}, 0},
		{"SCTP", []corev1.ServicePort{sctp}, Selector{PortName: "control"}, 0},
		{"recover unnamed", []corev1.ServicePort{unnamed}, Selector{Target: Target{Port: 8080}}, 8080},
		{"unnamed port acquired a name", []corev1.ServicePort{other}, Selector{Target: Target{Port: 8080}}, 0},
		{"no ports", nil, Selector{}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			port, err := ChooseServicePort(tc.ports, tc.requested)
			if (err != nil) != (tc.want == 0) || port.Port != tc.want {
				t.Fatalf("got %+v, %v; want %d", port, err, tc.want)
			}
		})
	}
}

func TestBookmarkAddress(t *testing.T) {
	server := settings.Defaults("").Server
	target := Target{Context: "cloud.prod", Namespace: "default", Service: "api"}
	ctx := NewContextRoutes("dev", []string{"dev", target.Context}, server, nil, nil)
	if _, got := ctx.BookmarkAddress(target, ""); got != "api.default."+Alias(target.Context)+".kuri.localhost" {
		t.Fatal(got)
	}
	server.Port, server.Domain = 8080, "test.localhost"
	aliases := map[string]string{target.Context: "live"}
	ctx = NewContextRoutes("dev", []string{"dev", target.Context}, server, nil, aliases)
	if _, got := ctx.BookmarkAddress(target, "http"); got != "http.api.default.live.test.localhost:8080" {
		t.Fatal(got)
	}
	server.Styles.ExplicitContext = false
	ctx = NewContextRoutes(target.Context, []string{target.Context}, server, nil, aliases)
	if _, got := ctx.BookmarkAddress(target, ""); got != "" {
		t.Fatalf("fell back to a different domain form: %s", got)
	}
	ctx = NewContextRoutes("dev", []string{"dev", target.Context}, server, []string{target.Context}, aliases)
	if _, got := ctx.BookmarkAddress(target, "http"); got != "" {
		t.Fatalf("allowed excluded context: %s", got)
	}
}
