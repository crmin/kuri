package settings

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

const proxyPauseFile = "proxy-paused"

// SetProxyPaused serializes an intentional stop with passive proxy startup.
func (s Store) SetProxyPaused(ctx context.Context, paused bool) error {
	boot, err := bootSession()
	if err != nil || boot == "" {
		return err
	}
	return s.setProxyPaused(ctx, boot, paused)
}

func (s Store) setProxyPaused(ctx context.Context, boot string, paused bool) error {
	lock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer lock.Close()
	path := filepath.Join(s.Directory(), proxyPauseFile)
	if paused {
		return s.replaceFile(path, []byte(boot))
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// PassiveStartAllowedLocked checks the stop record while the caller holds the
// settings snapshot lock. Keep that lock through publication of the control socket.
func (s Store) PassiveStartAllowedLocked() (bool, error) {
	boot, err := bootSession()
	if err != nil {
		return false, err
	}
	if boot == "" {
		return true, nil
	}
	paused, err := s.proxyPaused(boot)
	return !paused && err == nil, err
}

func (s Store) proxyPaused(boot string) (bool, error) {
	path := filepath.Join(s.Directory(), proxyPauseFile)
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return false, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != uint32(os.Geteuid()) || st.Mode&0022 != 0 || st.Size < 1 || st.Size > 128 {
		return false, errors.New("Unsafe kuri proxy pause record.")
	}
	data, err := io.ReadAll(io.LimitReader(file, 129))
	return string(data) == boot, err
}
