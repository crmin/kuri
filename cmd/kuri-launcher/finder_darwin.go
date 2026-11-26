package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
)

const startupLogLimit = 64 * 1024

func stderrIsDiscarded() bool {
	stderr, err := os.Stderr.Stat()
	if err != nil {
		return false
	}
	discard, err := os.Stat(os.DevNull)
	return err == nil && os.SameFile(stderr, discard)
}

func startWithFailureLog(binary string, env []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	var output startupOutput
	cmd := exec.CommandContext(ctx, binary, "start")
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, &output
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	err := cmd.Run()
	if err == nil {
		return nil
	}
	if len(output.data) == 0 {
		fmt.Fprintf(&output, "Could not start kuri: %v\n", err)
	}
	if logErr := writeStartupLog(output.data); logErr != nil {
		return errors.Join(err, fmt.Errorf("write startup log: %w", logErr))
	}
	return err
}

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
	_, err = file.Write(output)
	if err := errors.Join(err, file.Close()); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(dir, "startup.log"))
}
