package launchd

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
)

const Directory = "/Library/Application Support/kuri"

func ControlPath(uid int) string {
	return filepath.Join(Directory, fmt.Sprintf("activate-%d.sock", uid))
}

type Sockets struct {
	HTTP    []net.Listener
	Control *net.UnixListener
}

func (s Sockets) Close() {
	for _, listener := range s.HTTP {
		_ = listener.Close()
	}
	if s.Control != nil {
		_ = s.Control.Close()
	}
}

func Acquire() (Sockets, error) {
	return acquire(activateDescriptors)
}

func acquire(descriptors func(string) ([]int, error)) (Sockets, error) {
	http, err := takeListeners(descriptors, "HTTP")
	if err != nil {
		return Sockets{}, err
	}
	control, err := takeListeners(descriptors, "Control")
	if err != nil {
		Sockets{HTTP: http}.Close()
		return Sockets{}, err
	}
	if len(control) != 1 {
		Sockets{HTTP: append(http, control...)}.Close()
		return Sockets{}, errors.New("launchd must supply one Unix control listener")
	}
	unix, ok := control[0].(*net.UnixListener)
	if !ok {
		Sockets{HTTP: append(http, control...)}.Close()
		return Sockets{}, errors.New("launchd control listener is not a Unix socket")
	}
	s := Sockets{HTTP: http, Control: unix}
	if err := validateHTTP(http); err != nil {
		s.Close()
		return Sockets{}, err
	}
	return s, nil
}

func takeListeners(descriptors func(string) ([]int, error), name string) ([]net.Listener, error) {
	descs, err := descriptors(name)
	if err != nil {
		return nil, fmt.Errorf("activate launchd socket %s: %w", name, err)
	}
	files := make([]*os.File, len(descs))
	for i, fd := range descs {
		files[i] = os.NewFile(uintptr(fd), name)
	}
	defer func() {
		for _, file := range files {
			if file != nil {
				_ = file.Close()
			}
		}
	}()

	var listeners []net.Listener
	for _, file := range files {
		if file == nil {
			Sockets{HTTP: listeners}.Close()
			return nil, fmt.Errorf("invalid launchd descriptor for %s", name)
		}
		listener, err := net.FileListener(file)
		if err != nil {
			Sockets{HTTP: listeners}.Close()
			return nil, fmt.Errorf("convert launchd descriptor for %s: %w", name, err)
		}
		listeners = append(listeners, listener)
	}
	return listeners, nil
}

func validateHTTP(listeners []net.Listener) error {
	var v4, v6 bool
	var port int
	for _, listener := range listeners {
		addr, ok := listener.Addr().(*net.TCPAddr)
		if !ok || (port != 0 && addr.Port != port) {
			return errors.New("launchd HTTP listeners must share a TCP port")
		}
		port = addr.Port
		v4 = v4 || addr.IP.Equal(net.IPv4(127, 0, 0, 1))
		v6 = v6 || addr.IP.Equal(net.IPv6loopback)
	}
	if len(listeners) != 2 || !v4 || !v6 {
		return errors.New("launchd must supply IPv4 and IPv6 loopback HTTP listeners")
	}
	return nil
}
