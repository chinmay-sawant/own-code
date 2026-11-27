package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// maxMessageSize bounds a single framed message.
const maxMessageSize = 64 << 20

// Message is a JSON-RPC 2.0 envelope (request, response or notification).
type Message struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *ResponseError   `json:"error,omitempty"`
}

// ResponseError is a JSON-RPC error object.
type ResponseError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *ResponseError) Error() string {
	return fmt.Sprintf("lsp error %d: %s", e.Code, e.Message)
}

// IsRequest reports whether m is a request (method and id).
func (m *Message) IsRequest() bool { return m.Method != "" && m.ID != nil }

// IsNotification reports whether m is a notification (method, no id).
func (m *Message) IsNotification() bool { return m.Method != "" && m.ID == nil }

// IsResponse reports whether m is a response.
func (m *Message) IsResponse() bool { return m.Method == "" && m.ID != nil }

// Conn reads and writes Content-Length framed JSON-RPC messages.
type Conn struct {
	r  *bufio.Reader
	mu sync.Mutex // guards writes
	w  io.Writer
}

// NewConn wraps a reader/writer pair.
func NewConn(r io.Reader, w io.Writer) *Conn {
	return &Conn{r: bufio.NewReader(r), w: w}
}

// Write marshals and sends one message.
func (c *Conn) Write(m *Message) error {
	if m.JSONRPC == "" {
		m.JSONRPC = "2.0"
	}
	body, err := json.Marshal(m)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "Content-Length: %d\r\n\r\n", len(body))
	buf.Write(body)
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err = c.w.Write(buf.Bytes())
	return err
}

// Read blocks for the next message. It returns io.EOF on clean end of stream.
func (c *Conn) Read() (*Message, error) {
	length := -1
	sawHeader := false
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			if err == io.EOF && !sawHeader && line == "" {
				return nil, io.EOF
			}
			if err == io.EOF {
				return nil, io.ErrUnexpectedEOF
			}
			return nil, err
		}
		sawHeader = true
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, val, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("lsp: malformed header %q", line)
		}
		if strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(val))
			if err != nil || n < 0 {
				return nil, fmt.Errorf("lsp: bad Content-Length %q", val)
			}
			length = n
		}
	}
	if length < 0 {
		return nil, errors.New("lsp: missing Content-Length")
	}
	if length > maxMessageSize {
		return nil, fmt.Errorf("lsp: message too large (%d bytes)", length)
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(c.r, body); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	var m Message
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("lsp: bad JSON: %w", err)
	}
	return &m, nil
}
