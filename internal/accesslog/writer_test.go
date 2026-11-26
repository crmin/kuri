package accesslog

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestLogRetentionBeforeAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	log, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	origin := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	now := origin
	log.now = func() time.Time { return now }
	write := func(name string) {
		t.Helper()
		if err := log.Write(Event{Event: name}); err != nil {
			t.Fatal(err)
		}
	}
	write("first")
	now = origin.Add(time.Hour)
	write("second")
	now = origin.Add(72 * time.Hour)
	write("third")
	if got := events(t, path); len(got) != 3 || got[0].Event != "first" {
		t.Fatalf("72-hour boundary: %+v", got)
	}
	now = now.Add(time.Second)
	write("last")
	got := events(t, path)
	if len(got) != 3 || got[0].Event != "second" || got[2].Event != "last" {
		t.Fatalf("invalid order or retention: %+v", got)
	}

	// Simulate a restart over an existing file, then make it exceed capacity
	// by exactly one byte after adding a new entry.
	log, err = Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	log.now = func() time.Time { return now }
	extra, _ := json.Marshal(Event{Time: now, Event: "appended"})
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	log.maxBytes = stat.Size() + int64(len(extra))
	write("appended")
	got = events(t, path)
	if len(got) != 3 || got[0].Event != "third" || got[2].Event != "appended" {
		t.Fatalf("capacity trimming did not remove only the earliest line: %+v", got)
	}
}

func TestConcurrentLogWritesMirrorInFileOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	var console bytes.Buffer
	relay := NewConsole(&console)
	relay.Start()
	log, err := Open(path, relay)
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for i := range 30 {
		workers.Go(func() {
			if err := log.Write(Event{Event: "startup", PID: i + 1}); err != nil {
				t.Error(err)
			}
		})
	}
	workers.Wait()
	if err := relay.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, console.Bytes()) || len(events(t, path)) != 30 {
		t.Fatalf("incomplete or disordered console fanout: %v", err)
	}
}

func TestAppendRepairsTruncatedPreviousLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	if err := os.WriteFile(path, []byte(`{"event":`), 0600); err != nil {
		t.Fatal(err)
	}
	log, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := log.Write(Event{Event: "startup"}); err != nil {
		t.Fatal(err)
	}
	if got := events(t, path); len(got) != 1 || got[0].Event != "startup" {
		t.Fatalf("existing partial JSON corrupted following entry: %+v", got)
	}
}

func events(t *testing.T, path string) []Event {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var result []Event
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var event Event
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatal(err)
		}
		result = append(result, event)
	}
	return result
}

type heldConsole struct {
	entered chan struct{}
	release chan struct{}
}

func (sink heldConsole) Write(line []byte) (int, error) {
	select {
	case sink.entered <- struct{}{}:
	default:
	}
	<-sink.release
	return len(line), nil
}

func TestBlockedConsoleLeavesFileLoggingAvailable(t *testing.T) {
	sink := heldConsole{entered: make(chan struct{}, 1), release: make(chan struct{})}
	defer close(sink.release)
	console := NewConsole(sink)
	console.Start()
	path := filepath.Join(t.TempDir(), "access.log")
	log, err := Open(path, console)
	if err != nil {
		t.Fatal(err)
	}

	if err := log.Write(Event{Event: "startup"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-sink.entered:
	case <-time.After(time.Second):
		t.Fatal("console did not receive startup")
	}

	written := make(chan error, 1)
	go func() {
		for range consoleQueueSize + 1 {
			if err := log.Write(Event{Event: "request"}); err != nil {
				written <- err
				return
			}
		}
		written <- log.Write(Event{Event: "stop"})
	}()
	select {
	case err := <-written:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("a stalled console blocked file appends")
	}
	flush, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := console.Close(flush); err == nil {
		t.Fatal("undelivered console logs were not reported")
	}
	got := events(t, path)
	if len(got) != consoleQueueSize+3 || got[len(got)-1].Event != "stop" {
		t.Fatal("file is missing lines or the stop event")
	}
}
