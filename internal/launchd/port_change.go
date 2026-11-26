package launchd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type PortChange struct {
	ID   string
	Port int
}

func (p PortChange) acknowledgement() string {
	return fmt.Sprintf("%s\n%d\n", p.ID, p.Port)
}

func (p PortChange) Pending(home string) (bool, error) {
	if p.ID == "" {
		return false, nil
	}
	data, err := os.ReadFile(filepath.Join(home, ".kuri", "port-change.applied"))
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	return string(data) != p.acknowledgement(), err
}

func (p PortChange) Finish(home string) error {
	dir := filepath.Join(home, ".kuri")
	f, err := os.CreateTemp(dir, ".port-change-applied-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.WriteString(p.acknowledgement())
	if err := errors.Join(err, f.Close()); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, "port-change.applied"))
}
