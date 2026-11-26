//go:build darwin && cgo

package launchd

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

func InstallSettingsStartup(ctx context.Context, home, source string, stdout, stderr io.Writer, handoff func(context.Context) error) error {
	if handoff == nil {
		return changeStartup(ctx, "install", source, stdout, stderr, "--keep-paused")
	}
	return runStartupHandoff(ctx, home, handoff, func(ctx context.Context, dir string) error {
		return changeStartup(ctx, "install", source, stdout, stderr, "--keep-paused", dir)
	})
}

func UninstallSettingsStartup(ctx context.Context, home string, stderr io.Writer, save func(context.Context) error) error {
	return runStartupHandoff(ctx, home, save, func(ctx context.Context, dir string) error {
		return changeStartup(ctx, "uninstall", "", io.Discard, stderr, "80", "", dir)
	})
}

func runStartupHandoff(ctx context.Context, home string, handoff func(context.Context) error, installer func(context.Context, string) error) error {
	dir, err := os.MkdirTemp(filepath.Join(home, ".kuri"), ".startup-handoff-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- installer(ctx, dir) }()
	check := time.NewTicker(50 * time.Millisecond)
	defer check.Stop()
	for {
		select {
		case err := <-done:
			return err
		case <-ctx.Done():
			return errors.Join(ctx.Err(), <-done)
		case <-check.C:
		}
		_, err := os.Stat(filepath.Join(dir, "ready"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err == nil {
			err = handoff(ctx)
		}
		if err == nil {
			err = os.WriteFile(filepath.Join(dir, "continue"), nil, 0600)
		}
		if err != nil {
			if signalErr := os.WriteFile(filepath.Join(dir, "abort"), nil, 0600); signalErr != nil {
				cancel()
				err = errors.Join(err, signalErr)
			}
		}
		return errors.Join(err, <-done)
	}
}
