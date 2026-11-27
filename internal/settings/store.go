package settings

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"go.yaml.in/yaml/v3"
	"golang.org/x/sys/unix"
)

const maxSettingsSize = 1 << 20

type Store struct {
	home string
}

func NewStore(home string) Store { return Store{home: home} }

func (s Store) Directory() string { return filepath.Join(s.home, ".kuri") }

func (s Store) Path() string { return filepath.Join(s.Directory(), "settings.yaml") }

// Read returns defaults for a missing file. Ensure materializes them.
func (s Store) Read() (Config, error) {
	cfg, _, err := s.read()
	return cfg, err
}

func (s Store) Ensure(ctx context.Context) (Config, error) {
	_, cfg, err := s.update(ctx, nil, false)
	return cfg, err
}

// AutoStart serializes the admission decision with committed settings updates.
func (s Store) AutoStart(ctx context.Context) (bool, error) {
	var allowed bool
	err := s.WithSnapshot(ctx, func(cfg Config) error {
		allowed = cfg.General.AutoStart
		return nil
	})
	return allowed, err
}

// WithSnapshot serializes a short publication step with settings writes.
// The callback must not wait for a daemon or prepare external resources.
func (s Store) WithSnapshot(ctx context.Context, publish func(Config) error) error {
	lock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer lock.Close()
	cfg, err := s.Read()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return publish(cfg)
}

func (s Store) Update(ctx context.Context, change func(*Config) error) (before, after Config, err error) {
	return s.update(ctx, change, false)
}

func (s Store) Reset(ctx context.Context) (before, after Config, err error) {
	return s.update(ctx, func(cfg *Config) error { *cfg = Defaults(s.home); return nil }, true)
}

func (s Store) update(ctx context.Context, change func(*Config) error, repair bool) (Config, Config, error) {
	lock, err := s.lock(ctx)
	if err != nil {
		return Config{}, Config{}, err
	}
	defer lock.Close()
	before, exists, err := s.read()
	if err != nil {
		if !repair {
			return Config{}, Config{}, err
		}
		before = Defaults(s.home)
	}
	after := before
	after.Kubernetes.ExcludedContexts = append([]string{}, before.Kubernetes.ExcludedContexts...)
	if change != nil {
		if err := change(&after); err != nil {
			return before, before, err
		}
	}
	if err := after.Validate(); err != nil {
		return before, before, err
	}
	if !exists || repair || before.Fingerprint() != after.Fingerprint() {
		if err := s.write(after); err != nil {
			return before, before, fmt.Errorf("save kuri settings: %w", err)
		}
	}
	return before, after, nil
}

func (s Store) read() (Config, bool, error) {
	return s.readAt(s.Path())
}

func (s Store) readAt(path string) (Config, bool, error) {
	cfg := Defaults(s.home)
	file, err := openSettingsFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, false, nil
	}
	if err != nil {
		return Config{}, false, fmt.Errorf("open kuri settings: %w", err)
	}
	defer file.Close()
	decoder := yaml.NewDecoder(io.LimitReader(file, maxSettingsSize+1))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, true, fmt.Errorf("read kuri settings: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Config{}, true, errors.New("The kuri settings file must contain one YAML document.")
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, true, fmt.Errorf("read kuri settings: %w", err)
	}
	return cfg, true, nil
}

func openSettingsFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		file.Close()
		return nil, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != uint32(os.Geteuid()) || st.Mode&0022 != 0 || st.Size > maxSettingsSize {
		file.Close()
		return nil, errors.New("Unsafe kuri settings file.")
	}
	return file, nil
}

func (s Store) write(cfg Config) error {
	return s.writeAt(cfg, s.Path())
}

func (s Store) writeAt(cfg Config, path string) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	if len(data) > maxSettingsSize {
		return errors.New("The kuri settings file exceeds 1 MiB.")
	}
	return s.replaceFile(path, data)
}

func (s Store) replaceFile(path string, data []byte) error {
	return replaceSettingsFile(s.Directory(), path, data)
}

func replaceSettingsFile(directory, path string, data []byte) error {
	file, err := os.CreateTemp(directory, ".settings-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	if err := errors.Join(err, file.Close()); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func (s Store) lock(ctx context.Context) (*os.File, error) {
	dir := s.Directory()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	var st unix.Stat_t
	if err := unix.Lstat(dir, &st); err != nil {
		return nil, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR || st.Uid != uint32(os.Geteuid()) || st.Mode&0022 != 0 {
		return nil, errors.New("Unsafe kuri settings directory.")
	}
	path := filepath.Join(dir, "settings.lock")
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, fmt.Errorf("open kuri settings lock: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	if err := unix.Fstat(fd, &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFREG ||
		st.Uid != uint32(os.Geteuid()) || st.Mode&0022 != 0 {
		file.Close()
		return nil, errors.Join(errors.New("Unsafe kuri settings lock."), err)
	}
	retry := time.NewTicker(20 * time.Millisecond)
	defer retry.Stop()
	for ctx.Err() == nil {
		err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return file, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			file.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
		case <-retry.C:
		}
	}
	file.Close()
	return nil, ctx.Err()
}
