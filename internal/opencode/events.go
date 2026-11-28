package opencode

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

const maxEventLineSize = 16 << 20

// Event represents one event from GET /api/event. Data remains raw because
// OpenCode event payloads are a large, evolving union.
type Event struct {
	ID   string          `json:"id,omitempty"`
	Type string          `json:"event"`
	Data json.RawMessage `json:"data"`
}

// EventStream reads events from a SubscribeEvents response. Call Close when
// the consumer stops before the supplied context is cancelled.
type EventStream struct {
	body         io.ReadCloser
	scanner      *bufio.Scanner
	lastEventID  string
	pendingEvent string
	pendingData  []string
	closed       bool
	terminalErr  error
}

func newEventStream(body io.ReadCloser) *EventStream {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 4096), maxEventLineSize)
	return &EventStream{body: body, scanner: scanner}
}

// Next reads the next complete server-sent event. It returns io.EOF when the
// stream closes without a final event.
func (s *EventStream) Next() (Event, error) {
	if s.closed {
		return Event{}, io.EOF
	}
	if s.terminalErr != nil {
		return Event{}, s.terminalErr
	}
	for s.scanner.Scan() {
		line := s.scanner.Text()
		if line == "" {
			if event, ok := s.dispatch(); ok {
				return event, nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, found := strings.Cut(line, ":")
		if !found {
			field, value = line, ""
		}
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "id":
			if !strings.ContainsRune(value, '\x00') {
				s.lastEventID = value
			}
		case "event":
			s.pendingEvent = value
		case "data":
			s.pendingData = append(s.pendingData, value)
		}
	}
	if err := s.scanner.Err(); err != nil {
		s.terminalErr = err
		return Event{}, err
	}
	if event, ok := s.dispatch(); ok {
		return event, nil
	}
	return Event{}, io.EOF
}

func (s *EventStream) dispatch() (Event, bool) {
	if len(s.pendingData) == 0 {
		s.pendingEvent = ""
		return Event{}, false
	}
	event := Event{
		ID:   s.lastEventID,
		Type: s.pendingEvent,
		Data: json.RawMessage(strings.Join(s.pendingData, "\n")),
	}
	s.pendingEvent = ""
	s.pendingData = s.pendingData[:0]
	return event, true
}

// Close closes the underlying HTTP response body.
func (s *EventStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	if s.body == nil {
		return errors.New("OpenCode event stream has no response body")
	}
	return s.body.Close()
}
