package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/crmin/kuri/internal/launchd"
)

func (app *App) ConfirmPortChange(ctx context.Context, change launchd.PortChange) error {
	ctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		info, err := app.activationInfo(ctx)
		if err == nil {
			if info.PortChange != change.ID || info.State.Port != change.Port {
				return errors.New("The registered proxy did not confirm its new port.")
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for the registered proxy to confirm port %d: %w", change.Port, err)
		case <-ticker.C:
		}
	}
}

func (app *App) activationInfo(ctx context.Context) (reply, error) {
	ctx, cancel := context.WithTimeout(ctx, controlTimeout)
	defer cancel()
	socket, err := (&net.Dialer{}).DialContext(ctx, "unix", app.activation)
	if err != nil {
		return reply{}, err
	}
	defer socket.Close()
	deadline, _ := ctx.Deadline()
	_ = socket.SetDeadline(deadline)
	if err := sendRequest(socket.(*net.UnixConn), "activation_info"); err != nil {
		return reply{}, err
	}
	return decodeControlReply(json.NewDecoder(socket))
}

func ProbePort(port int) error {
	listeners, err := bindHTTP(port)
	if err != nil {
		return bindError(err, port)
	}
	closeListeners(listeners)
	return nil
}

func RequiresRegistration(err error) bool {
	return errors.Is(err, errBindPermission)
}

func (app *App) Registration(ctx context.Context) (launchd.Registration, error) {
	registration, err := launchd.Registered(ctx, app.home)
	if errors.Is(err, launchd.ErrOtherRegistration) {
		return launchd.Registration{}, nil
	}
	return registration, err
}

func (app *App) RegisterForPort(ctx context.Context, operation string) error {
	if app.register == nil {
		return errors.New("This proxy port requires macOS socket activation.")
	}
	if operation != "" {
		return launchd.Register(ctx, app.home, app.activation, operation)
	}
	return app.register(ctx)
}

func (app *App) EnsurePolicySupport(ctx context.Context) error {
	return launchd.EnsurePolicySupport(ctx, app.home, app.stderr)
}

func (app *App) UninstallStartupForReset(ctx context.Context, save func(context.Context) error) error {
	return launchd.UninstallSettingsStartup(ctx, app.home, app.stderr, save)
}

// OwnsRegisteredPort confirms the live listener independently of the plist.
// Inspecting the activation socket does not request a proxy startup.
func (app *App) OwnsRegisteredPort(ctx context.Context, port int) (bool, error) {
	if !app.hasActivationSocket() {
		return false, nil
	}
	info, err := app.activationInfo(ctx)
	if errors.Is(err, os.ErrNotExist) || connectionRefused(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.State.Port == port, nil
}
