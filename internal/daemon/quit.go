package daemon

import (
	"context"
	"errors"
	"fmt"
	"syscall"
	"time"
)

func (app *App) QuitBackground(ctx context.Context, knownPID int, knownStarted time.Time) error {
	query, cancel := context.WithTimeout(ctx, controlTimeout)
	defer cancel()
	conn, pid, err := app.connectRuntime(query)
	if errors.Is(err, errNoServer) {
		if knownPID > 0 && !errors.Is(syscall.Kill(knownPID, 0), syscall.ESRCH) {
			return fmt.Errorf("could not confirm shutdown of kuri process %d", knownPID)
		}
		return nil
	}
	if err != nil {
		return err
	}
	exited, release, err := subscribeExit(pid)
	if err != nil {
		conn.Close()
		return fmt.Errorf("observe shutdown of kuri process %d: %w", pid, err)
	}
	defer release()
	state, err := readState(query, conn, pid)
	conn.Close()
	if err != nil {
		return err
	}
	if state.Mode != background {
		return errors.New("Stop the foreground kuri process in its terminal before quitting.")
	}
	if knownPID > 0 && ((pid == knownPID && !state.Started.Equal(knownStarted)) ||
		(pid != knownPID && !errors.Is(syscall.Kill(knownPID, 0), syscall.ESRCH))) {
		return errors.New("Could not confirm the identity or shutdown of the previous kuri process.")
	}
	_, err = app.stopInstance(ctx, state, exited)
	if errors.Is(err, ErrNoBackground) {
		wait, stop := context.WithTimeout(ctx, startTimeout)
		defer stop()
		return exited(wait)
	}
	return err
}

func WaitForLaunchdExit(ctx context.Context, pid int) error {
	if pid == 0 || errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		return nil
	}
	exited, release, err := subscribeExit(pid)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	if err != nil {
		return err
	}
	defer release()
	wait, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	return exited(wait)
}
