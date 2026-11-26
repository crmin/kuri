//go:build !darwin || !cgo

package launchd

import (
	"context"
	"errors"
	"runtime"
)

func Supported() bool { return runtime.GOOS == "darwin" }

func Register(context.Context, string, string) error {
	return errors.New("build or run kuri with CGO_ENABLED=1 to register its macOS LaunchDaemon")
}
