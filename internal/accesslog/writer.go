package accesslog

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	maxLogAge   = 72 * time.Hour
	maxLogBytes = 100 * 1024 * 1024
)

type Connection struct {
	ConfigID   string `json:"config_id"`
	Alias      string `json:"alias"`
	Context    string `json:"context"`
	LocalAddr  string `json:"local_addr"`
	LocalPort  int    `json:"local_port"`
	Namespace  string `json:"namespace"`
	Protocol   string `json:"protocol"`
	RemotePort int32  `json:"remote_port"`
	Service    string `json:"service"`
}

type Event struct {
	Time    time.Time `json:"time"`
	Event   string    `json:"event"`
	PID     int       `json:"pid,omitempty"`
	Mode    string    `json:"mode,omitempty"`
	Domain  string    `json:"domain,omitempty"`
	Success *bool     `json:"success,omitempty"`
	Reason  string    `json:"reason,omitempty"`
	*Connection
}

type record struct {
	offset, size int64
	time         time.Time
}

// Writer owns the access log. The server holds its process lock for the entire
// lifetime of the Writer, including the initial scan and final shutdown entry.
type Writer struct {
	mu       sync.Mutex
	path     string
	console  *Console
	now      func() time.Time
	maxBytes int64

	records     []record
	oldest      time.Time
	size        int64
	needsRepair bool
}

func Open(path string, console *Console) (*Writer, error) {
	writer := &Writer{path: path, console: console, now: time.Now, maxBytes: maxLogBytes}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return writer, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("read access log: %w", err)
		}
		if len(line) > 0 {
			var entry struct {
				Time time.Time `json:"time"`
			}
			if err == nil && json.Unmarshal(line, &entry) == nil && !entry.Time.IsZero() {
				writer.add(record{writer.size, int64(len(line)), entry.Time})
			} else {
				writer.needsRepair = true
			}
			writer.size += int64(len(line))
		}
		if err != nil {
			break
		}
	}
	return writer, nil
}

func (w *Writer) add(entry record) {
	w.records = append(w.records, entry)
	if w.oldest.IsZero() || entry.time.Before(w.oldest) {
		w.oldest = entry.time
	}
}

func (w *Writer) Write(event Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	now := w.now().UTC()
	if event.Time.IsZero() {
		event.Time = now
	} else {
		event.Time = event.Time.UTC()
	}
	jsonLine, err := json.Marshal(event)
	if err != nil {
		return err
	}
	line := append(jsonLine, '\n')
	if int64(len(line)) > w.maxBytes {
		return errors.New("access log entry exceeds the file size limit")
	}

	cutoff := now.Add(-maxLogAge)
	newLine := line
	if event.Time.Before(cutoff) {
		newLine = nil
	}
	if w.needsRepair || (!w.oldest.IsZero() && w.oldest.Before(cutoff)) ||
		w.size+int64(len(newLine)) > w.maxBytes {
		err = w.compact(cutoff, event.Time, newLine)
	} else if len(newLine) > 0 {
		err = w.append(event.Time, newLine)
	}
	if err != nil {
		return fmt.Errorf("write access log: %w", err)
	}
	w.console.Enqueue(line)
	return nil
}

func (w *Writer) append(timestamp time.Time, line []byte) error {
	file, err := os.OpenFile(w.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	n, err := file.Write(line)
	if err != nil || n != len(line) {
		if err == nil {
			err = io.ErrShortWrite
		}
		return errors.Join(err, file.Truncate(w.size), file.Close())
	}
	w.add(record{w.size, int64(n), timestamp})
	w.size += int64(n)
	return file.Close()
}

func (w *Writer) compact(cutoff, timestamp time.Time, line []byte) error {
	keep := make([]record, 0, len(w.records)+1)
	total := int64(len(line))
	for _, entry := range w.records {
		if !entry.time.Before(cutoff) {
			keep = append(keep, entry)
			total += entry.size
		}
	}
	for total > w.maxBytes {
		total -= keep[0].size
		keep = keep[1:]
	}

	var original *os.File
	if len(keep) > 0 {
		var err error
		original, err = os.Open(w.path)
		if err != nil {
			return err
		}
		defer original.Close()
	}
	replacement, err := os.CreateTemp(filepath.Dir(w.path), ".access-*.tmp")
	if err != nil {
		return err
	}
	defer replacement.Close()
	defer os.Remove(replacement.Name())

	var offset int64
	for i := 0; i < len(keep); {
		start := keep[i].offset
		end := start + keep[i].size
		j := i + 1
		for j < len(keep) && keep[j].offset == end {
			end += keep[j].size
			j++
		}
		if _, err := io.CopyN(replacement, io.NewSectionReader(original, start, end-start), end-start); err != nil {
			return err
		}
		for ; i < j; i++ {
			keep[i].offset = offset
			offset += keep[i].size
		}
	}
	if len(line) > 0 {
		if _, err := replacement.Write(line); err != nil {
			return err
		}
		keep = append(keep, record{offset, int64(len(line)), timestamp})
	}
	if err := replacement.Sync(); err != nil {
		return err
	}
	if err := replacement.Close(); err != nil {
		return err
	}
	if err := os.Rename(replacement.Name(), w.path); err != nil {
		return err
	}

	w.oldest = time.Time{}
	w.records = keep[:0]
	for _, entry := range keep {
		w.add(entry)
	}
	w.size = total
	w.needsRepair = false
	return nil
}
