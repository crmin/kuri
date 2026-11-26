package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"time"
)

const handoffEvent = "kuri_port_restart"

var errHandoffRolledBack = errors.New("The switch from direct serving to launchd was rolled back.")

type handoffFrame struct {
	Event     string `json:"event"`
	Operation string `json:"operation"`
}

type foregroundHandoff struct {
	conn      *net.UnixConn
	owner     int
	mu        sync.Mutex
	operation string
	ack       chan struct{}
}

func (h *foregroundHandoff) prepare(ctx context.Context, operation string) error {
	if operation == "" || len(operation) > 64 {
		return errors.New("Invalid reverse proxy restart operation.")
	}
	h.mu.Lock()
	if h.ack != nil {
		h.mu.Unlock()
		return errors.New("The foreground reverse proxy already has a restart pending.")
	}
	ack := make(chan struct{})
	h.ack, h.operation = ack, operation
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.ack, h.operation = nil, ""
		h.mu.Unlock()
	}()
	_ = h.conn.SetWriteDeadline(time.Now().Add(controlTimeout))
	err := json.NewEncoder(h.conn).Encode(handoffFrame{Event: handoffEvent, Operation: operation})
	_ = h.conn.SetWriteDeadline(time.Time{})
	if err != nil {
		return err
	}
	timer := time.NewTimer(controlTimeout)
	defer timer.Stop()
	select {
	case <-ack:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return errors.New("The foreground terminal did not confirm the port change.")
	}
}

func (h *foregroundHandoff) read(cancel context.CancelFunc) {
	defer cancel()
	reader := json.NewDecoder(h.conn)
	for {
		var message handoffFrame
		if err := reader.Decode(&message); err != nil {
			return
		}
		h.mu.Lock()
		if message.Event == handoffEvent && h.ack != nil && h.operation == message.Operation {
			select {
			case <-h.ack:
			default:
				close(h.ack)
			}
		}
		h.mu.Unlock()
	}
}

func (app *App) PreparePortRestart(ctx context.Context, state State, operation string) error {
	if state.ForegroundOwner <= 0 {
		return errors.New("Update the foreground terminal to a kuri version that supports reconnecting.")
	}
	_, err := app.sendRuntimeCommand(ctx, state, request{Action: "prepare_port_restart", PortChange: operation})
	return err
}

func (app *App) HandoffDirectForeground(ctx context.Context, state State, operation string) error {
	_, err := app.sendRuntimeCommand(ctx, state, request{Action: "handoff_direct_foreground", PortChange: operation})
	return err
}

func (app *App) resumeForeground(ctx context.Context, operation string) (int, string, error) {
	return app.resumeForegroundMode(ctx, operation, false)
}

func (app *App) resumeForegroundMode(ctx context.Context, operation string, directFallback bool) (int, string, error) {
	deadline := time.NewTimer(2 * startTimeout)
	defer deadline.Stop()
	retry := time.NewTicker(100 * time.Millisecond)
	defer retry.Stop()
	for {
		change, err := app.portChange()
		if err == nil && change.ID == "" && directFallback && ctx.Err() == nil {
			return 0, "", errHandoffRolledBack
		}
		if err == nil && change.ID == operation {
			var pid int
			var next string
			pid, next, err = app.activateSession(ctx, foreground, false, operation)
			if pid != 0 || ctx.Err() != nil {
				return pid, next, err
			}
		}
		select {
		case <-ctx.Done():
			return 0, "", nil
		case <-deadline.C:
			return 0, "", errors.Join(errors.New("Could not reconnect to the foreground reverse proxy."), err)
		case <-retry.C:
		}
	}
}
