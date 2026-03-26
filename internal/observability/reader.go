package observability

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// EventFilter controls which events are returned by ReadEvents.
type EventFilter struct {
	Type    EventType // empty means all types
	AgentID string    // empty means all agents
	TaskID  string    // empty means all tasks
}

// ReadEvents reads JSONL events from the given file path, applying the filter.
func ReadEvents(path string, filter EventFilter) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open event log: %w", err)
	}
	defer f.Close()

	return ReadEventsFrom(f, filter)
}

// ReadEventsFrom reads JSONL events from a reader, applying the filter.
func ReadEventsFrom(r io.Reader, filter EventFilter) ([]Event, error) {
	var events []Event
	scanner := bufio.NewScanner(r)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var event Event
		if err := json.Unmarshal(line, &event); err != nil {
			continue // skip malformed lines
		}

		if filter.Type != "" && event.Type != filter.Type {
			continue
		}
		if filter.AgentID != "" && event.AgentID != filter.AgentID {
			continue
		}
		if filter.TaskID != "" && event.TaskID != filter.TaskID {
			continue
		}

		events = append(events, event)
	}

	if err := scanner.Err(); err != nil {
		return events, fmt.Errorf("read event log: %w", err)
	}

	return events, nil
}
