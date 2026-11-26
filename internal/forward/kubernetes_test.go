package forward

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

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
