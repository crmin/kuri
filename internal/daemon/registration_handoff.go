package daemon

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"time"
)

// HoldProxyForRegistration closes any background runtime and takes the same
// lock that both old and new daemons require before publishing a proxy.
func (app *App) HoldProxyForRegistration(ctx context.Context, validate func(State) error) (io.Closer, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	retry := time.NewTicker(20 * time.Millisecond)
	defer retry.Stop()
	stopped := false
	for {
		if err := ctx.Err(); err != nil {
			return nil, stopped, err
		}
		lock, err := lockRuntime(filepath.Join(app.home, ".kuri"))
		if err == nil {
			if err := validate(State{}); err != nil {
				lock.Close()
				return nil, stopped, err
			}
			return lock, stopped, nil
		}
		if !errors.Is(err, ErrAlreadyRunning) {
			return nil, stopped, err
		}
		state, err := app.State(ctx)
		if err != nil {
			return nil, stopped, err
		}
		if state.PID != 0 {
			if state.Mode != background {
				return nil, stopped, errors.New("Stop the foreground proxy from its terminal before updating the registration.")
			}
			if err := validate(state); err != nil {
				return nil, stopped, err
			}
			_, err = app.stopInstance(ctx, state, nil)
			if err != nil && !errors.Is(err, ErrNoBackground) {
				return nil, stopped, err
			}
			if err == nil {
				stopped = true
				continue
			}
		}
		select {
		case <-ctx.Done():
			return nil, stopped, ctx.Err()
		case <-retry.C:
		}
	}
}
