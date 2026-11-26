package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/crmin/kuri/internal/daemon"
	"github.com/crmin/kuri/internal/launchd"
	"github.com/crmin/kuri/internal/settings"
)

func stageStartupBinary(home string) (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	source, err := os.Open(executable)
	if err != nil {
		return "", err
	}
	defer source.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, source); err != nil {
		return "", err
	}
	store := settings.NewStore(home)
	dir := filepath.Join(store.Directory(), "installed", hex.EncodeToString(hash.Sum(nil)))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	binary := filepath.Join(dir, "kuri")
	f, err := os.CreateTemp(dir, ".kuri-")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	_, err = source.Seek(0, io.SeekStart)
	if err == nil {
		_, err = io.Copy(f, source)
	}
	if err == nil {
		err = f.Chmod(0700)
	}
	if err := errors.Join(err, f.Close()); err != nil {
		return "", err
	}
	return binary, os.Rename(f.Name(), binary)
}

func changeStartupSetting(ctx context.Context, app settingsServer, home string, state daemon.State, register bool) error {
	reg, err := app.Registration(ctx)
	if err != nil || reg.AtBoot == register {
		return err
	}
	if state.Mode == "foreground" && (register || state.Activated) {
		return errors.New("Stop the foreground proxy from its terminal before changing the startup registration.")
	}
	var changed error
	if register {
		source, err := stageStartupBinary(home)
		if err != nil {
			return err
		}
		var handoff func(context.Context) error
		if state.PID != 0 && !state.Activated {
			handoff = func(ctx context.Context) error {
				_, err := app.Stop(ctx)
				return err
			}
		}
		changed = launchd.InstallSettingsStartup(ctx, home, source, io.Discard, os.Stderr, handoff)
	} else {
		changed = launchd.UninstallStartup(ctx, io.Discard, os.Stderr)
	}
	if state.PID != 0 && (register || state.Activated) {
		// If the previous job exited, continue a running proxy even when
		// automatic startup is disabled.
		_, err := app.Start(ctx)
		if err != nil && !errors.Is(err, daemon.ErrAlreadyRunning) {
			changed = errors.Join(changed, err)
		}
	}
	return changed
}
