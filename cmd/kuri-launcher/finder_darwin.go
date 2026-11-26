//go:build cgo

package main

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

const startupLogLimit = 64 * 1024

type startupOutput struct {
	data []byte
}

func (out *startupOutput) Write(data []byte) (int, error) {
	// Keep draining stderr even after the log reaches its limit.
	keep := min(len(data), startupLogLimit-len(out.data))
	out.data = append(out.data, data[:keep]...)
	return len(data), nil
}

func writeStartupLog(output []byte) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, "Library", "Logs", "kuri")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0022 != 0 || int(info.Sys().(*syscall.Stat_t).Uid) != os.Geteuid() {
		return errors.New("the startup log directory must belong to the current user and be protected from writes by others")
	}
	file, err := os.CreateTemp(dir, ".startup-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(output[:min(len(output), startupLogLimit)])
	if err := errors.Join(err, file.Close()); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(dir, "startup.log"))
}
