package observability

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
)

// Emitter writes events to a JSONL file.
type Emitter struct {
	mu     sync.Mutex
	writer io.Writer
	file   *os.File
}

// NewEmitter creates an emitter that writes JSONL to the given file path.
// The file is created if it doesn't exist, and appended to if it does.
func NewEmitter(path string) (*Emitter, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("open event log: %w", err)
	}
	return &Emitter{writer: f, file: f}, nil
}

// NewEmitterWriter creates an emitter that writes to an arbitrary writer.
// Useful for testing.
func NewEmitterWriter(w io.Writer) *Emitter {
	return &Emitter{writer: w}
}

// Emit writes a single event as a JSON line.
func (e *Emitter) Emit(event Event) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	data = append(data, '\n')

	_, err = e.writer.Write(data)
	if err != nil {
		return fmt.Errorf("write event: %w", err)
	}
	return nil
}

// Close closes the underlying file if one was opened.
func (e *Emitter) Close() error {
	if e.file != nil {
		return e.file.Close()
	}
	return nil
}
