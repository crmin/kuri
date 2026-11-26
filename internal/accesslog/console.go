package accesslog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"time"
)

const consoleQueueSize = 128

// Console relays complete log lines in order. Disk writers only enqueue;
// a stalled foreground output must not hold the access log lock.
type Console struct {
	out     io.Writer
	lines   chan []byte
	done    chan struct{}
	dropped atomic.Uint64
	err     error // Published by closing done.
}

func NewConsole(out io.Writer) *Console {
	if out == nil {
		return nil
	}
	return &Console{out: out, lines: make(chan []byte, consoleQueueSize), done: make(chan struct{})}
}

// Start is called once, after any foreground readiness frame is sent.
func (c *Console) Start() {
	if c == nil {
		return
	}
	go func() {
		defer close(c.done)
		for line := range c.lines {
			n, err := c.out.Write(line)
			if err == nil && n != len(line) {
				err = io.ErrShortWrite
			}
			if err != nil {
				c.err = fmt.Errorf("console log output stopped: %w", err)
				return
			}
		}
	}()
}

// Enqueue takes ownership of line. Callers may continue writing the file
// when the console buffer is full; Close reports any omitted lines.
func (c *Console) Enqueue(line []byte) {
	if c == nil {
		return
	}
	select {
	case <-c.done:
		c.dropped.Add(1)
		return
	default:
	}
	select {
	case c.lines <- line:
	default:
		c.dropped.Add(1)
	}
}

// Close is called once, after all producers finish.
func (c *Console) Close(ctx context.Context) error {
	if c == nil {
		return nil
	}
	close(c.lines)
	select {
	case <-c.done:
		if count := c.dropped.Load(); count != 0 {
			return errors.Join(c.err, fmt.Errorf("%d access log lines were omitted from the console", count))
		}
		return c.err
	case <-ctx.Done():
		if sink, ok := c.out.(interface{ SetWriteDeadline(time.Time) error }); ok {
			_ = sink.SetWriteDeadline(time.Now())
		}
		return errors.New("console log output did not finish before shutdown; see access.log for file output")
	}
}
