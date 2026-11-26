//go:build cgo

package main

/*
#cgo LDFLAGS: -framework AppKit
#include <stdlib.h>
#include "statusitem_darwin.h"
*/
import "C"

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"
	"unsafe"

	"github.com/crmin/kuri/internal/menuview"
)

func init() { runtime.LockOSThread() }

const (
	startAction = 1
	stopAction  = 2
	quitAction  = 3
)

type menuPresentation struct {
	menuview.State
	Known    bool   `json:"known"`
	Busy     bool   `json:"busy"`
	Quitting bool   `json:"quitting"`
	Error    string `json:"error"`
}

type menuRunner interface {
	run(context.Context, time.Duration, time.Duration, ...string) commandResult
	state(context.Context) (menuview.State, error)
}

type menuController struct {
	mu                                                      sync.Mutex
	runner                                                  menuRunner
	render                                                  func(menuPresentation)
	terminate                                               func()
	logFailure                                              func([]byte)
	state                                                   menuview.State
	known, busy, querying, visible, quitPending, terminated bool
	generation                                              uint64
	actionError, stateError                                 string
	lastServer                                              menuview.State
	startUncertain                                          bool
	queryCancel                                             context.CancelFunc
	queryDone                                               chan struct{}
	pollCancel                                              context.CancelFunc
}

var menu *menuController

func main() {
	launcher, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		_ = writeStartupLog([]byte(err.Error() + "\n"))
		return
	}
	menu = &menuController{runner: newCommandRunner(launcher), render: renderNative, terminate: func() { C.kuriTerminate() },
		logFailure: func(data []byte) { _ = writeStartupLog(data) }}
	C.kuriRun()
}

func renderNative(view menuPresentation) {
	data, err := json.Marshal(view)
	if err != nil {
		return
	}
	value := C.CString(string(data))
	defer C.free(unsafe.Pointer(value))
	C.kuriRender(value)
}

//export kuriGoReady
func kuriGoReady() { menu.act(startAction, true) }

//export kuriGoVisible
func kuriGoVisible(visible C.int) { menu.setVisible(visible != 0) }

//export kuriGoAction
func kuriGoAction(action C.int) { menu.act(int(action), false) }

func (m *menuController) emitLocked() {
	detail := m.actionError
	if detail == "" {
		detail = m.stateError
	}
	m.render(menuPresentation{State: m.state, Known: m.known, Busy: m.busy, Quitting: m.quitPending, Error: detail})
}

func (m *menuController) setVisible(visible bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.visible == visible || m.terminated {
		return
	}
	m.visible = visible
	if !visible {
		m.pollCancel()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.pollCancel = cancel
	go func() {
		timer := time.NewTicker(time.Second)
		defer timer.Stop()
		for {
			select {
			case <-timer.C:
				m.refresh()
			case <-ctx.Done():
				return
			}
		}
	}()
	m.emitLocked()
	m.refreshLocked()
}

func (m *menuController) refresh() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refreshLocked()
}

func (m *menuController) refreshLocked() {
	if !m.visible || m.busy || m.querying || m.terminated {
		return
	}
	m.querying = true
	generation := m.generation
	ctx, cancel := context.WithCancel(context.Background())
	m.queryCancel = cancel
	done := make(chan struct{})
	m.queryDone = done
	go func() {
		defer close(done)
		defer cancel()
		state, err := m.runner.state(ctx)
		m.mu.Lock()
		defer m.mu.Unlock()
		m.querying = false
		m.queryCancel = nil
		m.queryDone = nil
		if generation != m.generation || m.terminated {
			return
		}
		m.setStateLocked(state, err)
		m.emitLocked()
	}()
}

func (m *menuController) setStateLocked(state menuview.State, err error) {
	m.known = err == nil
	if err == nil {
		m.state = state
		if state.PID > 0 {
			m.lastServer = state
		}
		m.stateError = ""
	} else {
		m.stateError = err.Error()
	}
}

func (m *menuController) act(action int, initial bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.terminated || m.quitPending {
		return
	}
	if action == quitAction {
		if m.startUncertain && !m.busy && (!m.known || m.state.PID == 0) {
			m.actionError = "The previous start did not finish. Confirm server state or try Start before quitting."
			m.emitLocked()
			return
		}
		m.quitPending = true
		if m.busy {
			m.emitLocked()
			return
		}
	} else if m.busy || (!initial && (!m.known ||
		(action == startAction && m.state.Mode != "stopped") ||
		(action == stopAction && m.state.Mode != "background"))) {
		return
	}
	m.beginLocked(action)
}

func (m *menuController) beginLocked(action int) {
	m.busy = true
	m.generation++
	if m.queryCancel != nil {
		m.queryCancel()
	}
	m.actionError = ""
	m.emitLocked()
	known := m.lastServer
	queryDone := m.queryDone
	go func() {
		if queryDone != nil {
			<-queryDone
		}
		m.runAction(action, known)
	}()
}

func (m *menuController) runAction(action int, known menuview.State) {
	ctx := context.Background()
	var result commandResult
	switch action {
	case startAction:
		result = m.runner.run(ctx, 220*time.Second, 20*time.Second, "start")
	case stopAction:
		result = m.runner.run(ctx, 50*time.Second, 20*time.Second, "stop")
	case quitAction:
		args := []string{"__menu_quit"}
		if known.PID > 0 && known.Started != nil {
			args = append(args, strconv.Itoa(known.PID), known.Started.Format(time.RFC3339Nano))
		}
		result = m.runner.run(ctx, 4*time.Minute, 20*time.Second, args...)
	}
	if action == quitAction && result.err == nil {
		m.mu.Lock()
		m.terminated = true
		m.mu.Unlock()
		m.terminate()
		return
	}
	state, err := m.runner.state(ctx)
	// Failure to start an already running server is normal when the app is opened.
	if action == startAction && result.diagnostic == "kuri is already running." && err == nil && state.PID > 0 {
		result.err = nil
	}
	if action == startAction && result.err != nil && m.logFailure != nil {
		m.logFailure([]byte(result.diagnostic + "\n"))
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.busy = false
	if action == stopAction && result.err == nil {
		m.lastServer = menuview.State{}
	}
	m.setStateLocked(state, err)
	if action == startAction {
		m.startUncertain = result.timedOut || (result.err != nil && err != nil)
	}
	if result.err != nil {
		m.actionError = result.diagnostic
	}
	if action == quitAction || m.startUncertain {
		m.quitPending = false
	}
	if m.quitPending {
		m.beginLocked(quitAction)
		return
	}
	m.emitLocked()
}
