package route

import (
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

func ChooseServicePort(ports []corev1.ServicePort, requested Selector) (corev1.ServicePort, error) {
	for _, port := range ports {
		if !IsTCPServicePort(port) {
			continue
		}
		switch {
		case requested.PortName != "" && port.Name == requested.PortName:
			return port, nil
		case requested.PortName == "" && requested.Port != 0 && port.Name == "" && port.Port == requested.Port:
			return port, nil
		case requested.IsDefault() && (len(ports) == 1 || port.Name == "http"):
			return port, nil
		}
	}
	if requested.PortName != "" {
		return corev1.ServicePort{}, fmt.Errorf("service has no TCP port named %q", requested.PortName)
	}
	if requested.Port != 0 {
		return corev1.ServicePort{}, fmt.Errorf("service has no unnamed TCP port %d", requested.Port)
	}
	return corev1.ServicePort{}, errors.New("service has no default TCP port")
}

func IsTCPServicePort(port corev1.ServicePort) bool {
	return port.Protocol == "" || port.Protocol == corev1.ProtocolTCP
}
