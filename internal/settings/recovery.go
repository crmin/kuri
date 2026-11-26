package settings

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
)

var operationID = regexp.MustCompile(`^[A-Za-z0-9]{1,64}$`)

func (s Store) RetainRecovery(ctx context.Context, operation string, cfg Config) (func(), error) {
	path, err := s.recoveryPath(operation)
	if err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	lock, err := s.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	if err := s.writeAt(cfg, path); err != nil {
		return nil, err
	}
	return func() { _ = os.Remove(path) }, nil
}

func (s Store) Recovery(operation string) (*Config, error) {
	path, err := s.recoveryPath(operation)
	if err != nil {
		return nil, err
	}
	cfg, exists, err := s.readAt(path)
	if !exists || err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (s Store) recoveryPath(operation string) (string, error) {
	if !operationID.MatchString(operation) {
		return "", errors.New("Invalid kuri recovery operation.")
	}
	return filepath.Join(s.Directory(), ".recovery-"+operation+".yaml"), nil
}
