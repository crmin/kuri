package forward

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"syscall"

	"github.com/crmin/kuri/internal/route"
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
	config *clientconfig.Config
	loader *clientcmd.ClientConfigLoadingRules
}

func LoadKubernetes(filename string) (*Kubernetes, route.Contexts, error) {
	loader := &clientcmd.ClientConfigLoadingRules{ExplicitPath: filename}
	config, err := loader.Load()
	if err != nil {
		return nil, route.Contexts{}, fmt.Errorf("load kubeconfig: %w", err)
	}
	names := make([]string, 0, len(config.Contexts))
	for name := range config.Contexts {
		names = append(names, name)
	}
	return &Kubernetes{config, loader}, route.NewContexts(config.CurrentContext, names), nil
}

func (k *Kubernetes) client(name string) (*coreclient.CoreV1Client, *rest.Config, error) {
	if k.config.Contexts[name] == nil {
		return nil, nil, errors.New("unknown Kubernetes context")
	}
	config, err := clientcmd.NewNonInteractiveClientConfig(
		*k.config, name, &clientcmd.ConfigOverrides{}, k.loader,
	).ClientConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("load Kubernetes settings for context %q: %w", name, err)
	}
	client, err := coreclient.NewForConfig(config)
	if err != nil {
		return nil, nil, fmt.Errorf("create Kubernetes client: %w", err)
	}
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

func (k *Kubernetes) Open(ctx context.Context, dest Destination, port int) (Tunnel, error) {
	client, config, err := k.client(dest.Context)
	if err != nil {
		return Tunnel{}, err
	}
	url := client.RESTClient().Post().Namespace(dest.Namespace).Resource("pods").Name(dest.Pod).SubResource("portforward").URL()

	// The readiness context bounds the handshake. Once ready, the tunnel has
	// its own lifetime so a successful upgrade survives that context.
	lifetime, stop := context.WithCancel(logr.NewContext(context.WithoutCancel(ctx), logr.Discard()))
	config.Wrap(func(next http.RoundTripper) http.RoundTripper {
		return roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return next.RoundTrip(request.WithContext(lifetime))
		})
	})
	rt, upgrader, err := spdy.RoundTripperFor(config)
	if err != nil {
		stop()
		return Tunnel{}, fmt.Errorf("configure SPDY port forward: %w", err)
	}
	websocket, err := portforward.NewSPDYOverWebsocketDialerForStreamingWithLogger(logr.Discard(), url, config)
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
