//go:build !darwin || !cgo

package launchd

import (
	"context"
	"errors"
	"io"
	"runtime"
)

func Supported() bool { return runtime.GOOS == "darwin" }

func Register(context.Context, string, string) error {
	return errors.New("build or run kuri with CGO_ENABLED=1 to register its macOS LaunchDaemon")
}

func InstallStartup(context.Context, io.Writer, io.Writer) error { return startupUnavailable() }

func UninstallStartup(context.Context, io.Writer, io.Writer) error { return startupUnavailable() }

type noopLock struct{}

func (noopLock) Close() error { return nil }

func LockLifecycle(context.Context, string) (io.Closer, error) { return noopLock{}, nil }
func ReadMenuPause() (MenuPause, error)                        { return MenuPause{}, nil }
func ChangeMenuRegistration(_ context.Context, action, _, _, _ string, _ io.Writer) error {
	if action == "pause" {
		return nil
	}
	return startupUnavailable()
}

func startupUnavailable() error {
	if runtime.GOOS == "darwin" {
		return errors.New("build kuri with CGO_ENABLED=1 to manage its macOS startup registration")
	}
	return errors.New("kuri startup installation is available on macOS")
}
