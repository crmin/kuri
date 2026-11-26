package launchd

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestActivatedListeners(t *testing.T) {
	ip4, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer ip4.Close()
	ip6, err := net.ListenTCP("tcp6", &net.TCPAddr{IP: net.IPv6loopback, Port: ip4.Addr().(*net.TCPAddr).Port})
	if err != nil {
		t.Fatal(err)
	}
	defer ip6.Close()
	dir, err := os.MkdirTemp("/tmp", "kuri-fd-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	ctrl, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(dir, "ctl"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer ctrl.Close()

	dup := func(listener interface{ File() (*os.File, error) }) int {
		t.Helper()
		file, err := listener.File()
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		fd, err := unix.Dup(int(file.Fd()))
		if err != nil {
			t.Fatal(err)
		}
		return fd
	}

	v4, v6, control := dup(ip4), dup(ip6), dup(ctrl)
	inherited, err := acquire(func(name string) ([]int, error) {
		if name == "HTTP" {
			return []int{v4, v6}, nil
		}
		if name == "Control" {
			return []int{control}, nil
		}
		return nil, errors.New("wrong socket name")
	})
	if err != nil {
		t.Fatal(err)
	}
	defer inherited.Close()
	if len(inherited.HTTP) != 2 || inherited.Control.Addr().String() != ctrl.Addr().String() {
		t.Fatalf("unexpected socket addresses: %+v", inherited)
	}
	inherited.Close()
	for _, fd := range []int{v4, v6, control} {
		if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
			t.Fatalf("descriptor %d was not released: %v", fd, err)
		}
	}
}

func TestControlSocketFailureClosesHTTP(t *testing.T) {
	ip4, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer ip4.Close()
	file, err := ip4.File()
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	fd, err := unix.Dup(int(file.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	_, err = acquire(func(name string) ([]int, error) {
		if name == "HTTP" {
			return []int{fd}, nil
		}
		return nil, errors.New("not registered")
	})
	if err == nil {
		t.Fatal("expected missing socket error")
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
		t.Fatalf("descriptor %d was not released: %v", fd, err)
	}
}
