package route

import (
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

func ChooseServicePort(ports []corev1.ServicePort, requested int32) (corev1.ServicePort, error) {
	for _, port := range ports {
		if port.Protocol != "" && port.Protocol != corev1.ProtocolTCP {
			continue
		}
		if requested != 0 {
			if port.Port == requested {
				return port, nil
			}
			continue
		}
		if len(ports) == 1 || port.Name == "http" {
			return port, nil
		}
	}
	if requested != 0 {
		return corev1.ServicePort{}, fmt.Errorf("service has no TCP port %d", requested)
	}
	return corev1.ServicePort{}, errors.New("service has no default TCP port")
}
