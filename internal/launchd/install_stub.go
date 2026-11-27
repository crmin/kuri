//go:build !darwin || !cgo

package launchd

import (
	"context"
	"errors"
	"io"
	"runtime"
)

func Supported() bool { return runtime.GOOS == "darwin" }

func Register(context.Context, string, string, ...string) error {
	return errors.New("build or run kuri with CGO_ENABLED=1 to register its macOS LaunchDaemon")
}

func InstallStartup(context.Context, io.Writer, io.Writer) error { return startupUnavailable() }

func InstallStartupAt(context.Context, string, io.Writer, io.Writer) error {
	return startupUnavailable()
}

func UninstallStartup(context.Context, io.Writer, io.Writer) error { return startupUnavailable() }

func RemoveSocketRegistration(context.Context, io.Writer) error { return startupUnavailable() }

func Registered(context.Context, string) (Registration, error) { return Registration{}, nil }

func (r Registration) SupportsSettings(context.Context) bool { return !r.Present() }

func (r Registration) SupportsSettingsSchema(context.Context, int) bool { return !r.Present() }

func CheckSettingsSchema(context.Context, string, int, int, func(context.Context) bool) error {
	return nil
}

func (r Registration) SupportsProxyPause(context.Context) bool { return !r.Present() }

func CheckProxyPauseSupport(context.Context, string, int, func(context.Context) bool) error {
	return nil
}

func EnsurePolicySupport(context.Context, string, io.Writer) error { return nil }

func UpdateProxyRegistration(context.Context, string, string, Registration, io.Writer, func(context.Context) error) error {
	return startupUnavailable()
}

func ReadPortChange() (PortChange, error) { return PortChange{}, nil }

func ChangePort(context.Context, string, PortChange, io.Writer) (bool, error) {
	return false, startupUnavailable()
}

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
