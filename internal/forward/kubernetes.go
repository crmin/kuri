package forward

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"

	"github.com/crmin/kuri/internal/credential"
	"github.com/crmin/kuri/internal/route"
	"github.com/crmin/kuri/internal/settings"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientconfig "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
	streamhttp "k8s.io/streaming/pkg/httpstream"
)

var errRouteUnavailable = errors.New("the Kubernetes service port is unavailable")

type Kubernetes struct {
	config      *clientconfig.Config
	loader      *clientcmd.ClientConfigLoadingRules
	credentials *credential.Runner

	mu      sync.Mutex
	clients map[string]contextClient
}

type contextClient struct {
	client *coreclient.CoreV1Client
	config *rest.Config
}

func LoadKubernetes(filename string) (*Kubernetes, route.Contexts, error) {
	loader := &clientcmd.ClientConfigLoadingRules{ExplicitPath: filename}
	config, err := loader.Load()
	if err != nil {
		return nil, route.Contexts{}, fmt.Errorf("load kubeconfig: %w", err)
	}
	k := &Kubernetes{config: config, loader: loader}
	return k, k.Routes(settings.Defaults("").Server, nil), nil
}

func (k *Kubernetes) ContextNames() []string {
	names := make([]string, 0, len(k.config.Contexts))
	for name := range k.config.Contexts {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func (k *Kubernetes) Routes(server settings.Server, excluded []string) route.Contexts {
	names := slices.DeleteFunc(k.ContextNames(), func(name string) bool {
		return slices.Contains(excluded, name)
	})
	return route.NewConfiguredContexts(k.config.CurrentContext, names, server)
}

// StartCredentials is called before serving requests. Non-exec kubeconfigs
// do not need a helper session or an executable copy.
func (k *Kubernetes) StartCredentials(runtimeDir string, retainExecutable bool) error {
	for _, info := range k.config.AuthInfos {
		if info != nil && info.Exec != nil {
			runner, err := credential.New(runtimeDir, retainExecutable)
			if err != nil {
				return fmt.Errorf("prepare credential helper: %w", err)
			}
			k.credentials = runner
			break
		}
	}
	return nil
}

func (k *Kubernetes) StopCredentials() error {
	if k.credentials != nil {
		return k.credentials.Close()
	}
	return nil
}

func (k *Kubernetes) client(name string) (*coreclient.CoreV1Client, *rest.Config, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if existing, ok := k.clients[name]; ok {
		return existing.client, existing.config, nil
	}
	if k.config.Contexts[name] == nil {
		return nil, nil, errors.New("unknown Kubernetes context")
	}
	config, err := clientcmd.NewNonInteractiveClientConfig(
		*k.config.DeepCopy(), name, &clientcmd.ConfigOverrides{}, k.loader,
	).ClientConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("load Kubernetes settings for context %q: %w", name, err)
	}
	if config.ExecProvider != nil && k.credentials != nil {
		if err := k.credentials.Wrap(config.ExecProvider); err != nil {
			return nil, nil, err
		}
	}
	client, err := coreclient.NewForConfig(config)
	if err != nil {
		return nil, nil, fmt.Errorf("create Kubernetes client: %w", err)
	}
	if k.clients == nil {
		k.clients = make(map[string]contextClient)
	}
	k.clients[name] = contextClient{client: client, config: config}
	return client, config, nil
}

func (k *Kubernetes) Resolve(ctx context.Context, target route.Target, failedPod string) ([]Destination, error) {
	client, _, err := k.client(target.Context)
	if err != nil {
		return nil, err
	}
	service, err := client.Services(target.Namespace).Get(ctx, target.Service, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("Kubernetes service %q not found", target.Service)
		}
		return nil, kubernetesError("get Kubernetes service", err)
	}

	port, err := route.ChooseServicePort(service.Spec.Ports, target.Port)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errRouteUnavailable, err)
	}
	if len(service.Spec.Selector) == 0 {
		return nil, fmt.Errorf("%w: service has no pod selector", errRouteUnavailable)
	}
	defaultPort, _ := route.ChooseServicePort(service.Spec.Ports, 0)
	target.Port = port.Port
	pods, err := client.Pods(target.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labels.Set(service.Spec.Selector).String(),
	})
	if err != nil {
		return nil, kubernetesError("list Kubernetes pods", err)
	}
	sort.SliceStable(pods.Items, func(i, j int) bool {
		left, right := &pods.Items[i], &pods.Items[j]
		if a, b := podPriority(left, failedPod), podPriority(right, failedPod); a != b {
			return a > b
		}
		return left.Name < right.Name
	})

	var destinations []Destination
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Status.Phase != corev1.PodRunning || pod.DeletionTimestamp != nil {
			continue
		}
		podPort, err := podTargetPort(port, pod, service.Spec.ClusterIP == corev1.ClusterIPNone)
		if err == nil {
			destinations = append(destinations, Destination{
				Target: target, PortName: port.Name, DefaultPort: defaultPort.Port,
				Pod: pod.Name, PodUID: string(pod.UID), TargetPort: podPort,
			})
		}
	}
	if len(destinations) == 0 {
		return nil, errors.New("no running pod matches the service selector and target port")
	}
	return destinations, nil
}

func kubernetesError(operation string, err error) error {
	if status := apierrors.ReasonForError(err); status != metav1.StatusReasonUnknown {
		return fmt.Errorf("%s: %s", operation, status)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return fmt.Errorf("%s: Kubernetes API request failed", operation)
}

func podPriority(pod *corev1.Pod, failedPod string) int {
	priority := 0
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			priority += 2
			break
		}
	}
	if string(pod.UID) != failedPod {
		priority++
	}
	return priority
}

func podTargetPort(port corev1.ServicePort, pod *corev1.Pod, headless bool) (int32, error) {
	if headless || port.TargetPort == (intstr.IntOrString{}) {
		return port.Port, nil
	}
	if port.TargetPort.Type == intstr.Int && port.TargetPort.IntVal > 0 {
		return port.TargetPort.IntVal, nil
	}
	namedPort := func(container corev1.Container) int32 {
		for _, candidate := range container.Ports {
			if candidate.Name == port.TargetPort.StrVal &&
				(candidate.Protocol == corev1.ProtocolTCP || candidate.Protocol == "") {
				return candidate.ContainerPort
			}
		}
		return 0
	}
	for _, container := range pod.Spec.Containers {
		if value := namedPort(container); value > 0 {
			return value, nil
		}
	}
	for _, container := range pod.Spec.InitContainers {
		if container.RestartPolicy != nil && *container.RestartPolicy == corev1.ContainerRestartPolicyAlways {
			if value := namedPort(container); value > 0 {
				return value, nil
			}
		}
	}
	return 0, fmt.Errorf("pod has no TCP target port named %q", port.TargetPort.StrVal)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

// Classify errors before ForwardPorts flattens them into strings. Never
// include the upstream response body, credentials, or full request URL.
func forwardFailure(response *http.Response, err error) error {
	if response != nil {
		switch response.StatusCode {
		case http.StatusSwitchingProtocols:
			return nil
		case http.StatusForbidden:
			return errors.New("Kubernetes pod port forwarding was forbidden (HTTP 403)")
		case http.StatusUnauthorized:
			return errors.New("Kubernetes pod port forwarding authentication failed (HTTP 401)")
		default:
			return fmt.Errorf("Kubernetes port forward protocol upgrade failed (HTTP %d)", response.StatusCode)
		}
	}
	var upgrade *streamhttp.UpgradeFailureError
	if errors.As(err, &upgrade) {
		if apierrors.IsForbidden(upgrade.Cause) {
			return errors.New("Kubernetes pod port forwarding was forbidden (HTTP 403)")
		}
		if apierrors.IsUnauthorized(upgrade.Cause) {
			return errors.New("Kubernetes pod port forwarding authentication failed (HTTP 401)")
		}
		return errors.New("Kubernetes port forward protocol upgrade failed")
	}
	var network net.Error
	if errors.As(err, &network) || errors.Is(err, io.EOF) {
		return errors.New("Kubernetes API connection for port forwarding failed")
	}
	return nil
}

func (k *Kubernetes) Open(ctx context.Context, dest Destination, port int) (Tunnel, error) {
	client, baseConfig, err := k.client(dest.Context)
	if err != nil {
		return Tunnel{}, err
	}
	config := *baseConfig
	url := client.RESTClient().Post().Namespace(dest.Namespace).Resource("pods").Name(dest.Pod).SubResource("portforward").URL()

	// The readiness context bounds the handshake. Once ready, the tunnel has
	// its own lifetime so a successful upgrade survives that context.
	lifetime, stop := context.WithCancel(logr.NewContext(context.WithoutCancel(ctx), logr.Discard()))
	var failure error
	config.Wrap(func(next http.RoundTripper) http.RoundTripper {
		return roundTripFunc(func(request *http.Request) (*http.Response, error) {
			response, err := next.RoundTrip(request.WithContext(lifetime))
			failure = forwardFailure(response, err)
			return response, err
		})
	})
	rt, upgrader, err := spdy.RoundTripperFor(&config)
	if err != nil {
		stop()
		return Tunnel{}, fmt.Errorf("configure SPDY port forward: %w", err)
	}
	websocket, err := portforward.NewSPDYOverWebsocketDialerForStreamingWithLogger(logr.Discard(), url, &config)
	if err != nil {
		stop()
		return Tunnel{}, fmt.Errorf("configure WebSocket port forward: %w", err)
	}
	dialer := portforward.NewFallbackDialerForStreaming(websocket,
		spdy.NewDialerForStreaming(upgrader, &http.Client{Transport: rt}, http.MethodPost, url),
		func(err error) bool { return streamhttp.IsUpgradeFailure(err) || streamhttp.IsHTTPSProxyError(err) },
	)
	var listenErrors bytes.Buffer
	ready := make(chan struct{})
	forward, err := portforward.NewOnAddressesForStreamingWithContext(lifetime, dialer,
		[]string{"127.0.0.1"}, []string{fmt.Sprintf("%d:%d", port, dest.TargetPort)}, ready, io.Discard, &listenErrors)
	if err != nil {
		stop()
		return Tunnel{}, err
	}

	done := make(chan struct{})
	var forwardErr error
	go func() {
		defer close(done)
		forwardErr = forward.ForwardPorts()
	}()
	select {
	case <-ctx.Done():
		stop()
		<-done
		return Tunnel{}, ctx.Err()
	case <-done:
		stop()
		if strings.Contains(listenErrors.String(), syscall.EADDRINUSE.Error()) {
			return Tunnel{}, ErrPortInUse
		}
		if forwardErr != nil && listenErrors.Len() > 0 {
			return Tunnel{}, fmt.Errorf("bind local forwarding port %d: %s", port, strings.TrimSpace(listenErrors.String()))
		}
		if failure != nil {
			return Tunnel{}, failure
		}
		return Tunnel{}, fmt.Errorf("Kubernetes port forward to pod %q closed before readiness", dest.Pod)
	case <-ready:
		return Tunnel{
			Done: done,
			Close: func() {
				stop()
				<-done
			},
		}, nil
	}
}
