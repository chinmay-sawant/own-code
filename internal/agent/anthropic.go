package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// anthropicVersion is the Anthropic Messages API version header value.
const anthropicVersion = "2023-06-01"

// anthropicDefaultMaxTokens is used because Messages requires max_tokens.
const anthropicDefaultMaxTokens = 8192

type anthropicAdapter struct{}

type anthBlock map[string]any

type anthMsg struct {
	Role    string      `json:"role"`
	Content []anthBlock `json:"content"`
}

func (anthropicAdapter) encode(req Request, stream bool) ([]byte, error) {
	var system []string
	var msgs []anthMsg
	push := func(role string, blocks ...anthBlock) {
		if n := len(msgs); n > 0 && msgs[n-1].Role == role {
			msgs[n-1].Content = append(msgs[n-1].Content, blocks...)
			return
		}
		msgs = append(msgs, anthMsg{Role: role, Content: blocks})
	}
	for _, m := range req.Messages {
		switch m.Role {
		case RoleSystem:
			if m.Content != "" {
				system = append(system, m.Content)
			}
		case RoleTool:
			push("user", anthBlock{"type": "tool_result", "tool_use_id": m.ToolCallID, "content": m.Content})
		case RoleAssistant:
			var blocks []anthBlock
			if m.Content != "" {
				blocks = append(blocks, anthBlock{"type": "text", "text": m.Content})
			}
			for _, tc := range m.ToolCalls {
				input := tc.Arguments
				if len(input) == 0 {
					input = json.RawMessage(`{}`)
				}
				blocks = append(blocks, anthBlock{"type": "tool_use", "id": tc.ID, "name": tc.Name, "input": input})
			}
			if len(blocks) > 0 {
				push("assistant", blocks...)
			}
		default:
			push("user", anthBlock{"type": "text", "text": m.Content})
		}
	}
	max := req.MaxOutputTokens
	if max <= 0 {
		max = anthropicDefaultMaxTokens
	}
	body := map[string]any{"model": req.Model.ModelID, "messages": msgs, "stream": stream, "max_tokens": max}
	if len(system) > 0 {
		body["system"] = strings.Join(system, "\n\n")
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			params := t.Parameters
			if len(params) == 0 {
				params = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			tools = append(tools, map[string]any{"name": t.Name, "description": t.Description, "input_schema": params})
		}
		body["tools"] = tools
	}
	return json.Marshal(body)
}

type anthUsage struct {
	Input  int `json:"input_tokens"`
	Output int `json:"output_tokens"`
}

func anthToUsage(in, out int) *Usage {
	return &Usage{InputTokens: in, OutputTokens: out, TotalTokens: in + out}
}

func anthInput(raw json.RawMessage) (json.RawMessage, error) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		s = "{}"
	}
	return validArgs(s)
}

func (anthropicAdapter) decode(raw []byte) (Result, error) {
	var r struct {
		Type    string `json:"type"`
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		StopReason string     `json:"stop_reason"`
		Usage      *anthUsage `json:"usage"`
		Error      *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return Result{}, err
	}
	if r.Type == "error" || r.Error != nil {
		return Result{}, &Error{Kind: ErrKindServer, Message: "inference response reported an error: " + extractErrorMessage(raw)}
	}
	res := Result{FinishReason: r.StopReason}
	if r.Usage != nil {
		res.Usage = anthToUsage(r.Usage.Input, r.Usage.Output)
	}
	var text strings.Builder
	for _, b := range r.Content {
		switch b.Type {
		case "text":
			text.WriteString(b.Text)
		case "tool_use":
			args, err := anthInput(b.Input)
			if err != nil || b.ID == "" || b.Name == "" {
				return Result{}, errors.New("incomplete tool call in response")
			}
			res.ToolCalls = append(res.ToolCalls, ToolCall{ID: b.ID, Name: b.Name, Arguments: args})
		}
	}
	res.Text = text.String()
	return res, nil
}

type anthPending struct {
	id, name string
	args     strings.Builder
}

type anthStream struct {
	emit    func(Event)
	text    strings.Builder
	blocks  map[int]*anthPending
	calls   []ToolCall
	in, out int
	hasUse  bool
	reason  string
	stopped bool
}

func (anthropicAdapter) newStream(emit func(Event)) streamState {
	return &anthStream{emit: emit, blocks: map[int]*anthPending{}}
}

func (s *anthStream) handle(data []byte) (bool, error) {
	data = bytes.TrimSpace(data)
	if string(data) == "[DONE]" {
		return true, nil
	}
	var ev struct {
		Type    string `json:"type"`
		Index   int    `json:"index"`
		Message struct {
			Usage *anthUsage `json:"usage"`
		} `json:"message"`
		ContentBlock struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"content_block"`
		Delta struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			PartialJSON string `json:"partial_json"`
			StopReason  string `json:"stop_reason"`
		} `json:"delta"`
		Usage *anthUsage `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &ev); err != nil {
		return false, fmt.Errorf("malformed event: %w", err)
	}
	switch ev.Type {
	case "message_start":
		if u := ev.Message.Usage; u != nil {
			s.in, s.out, s.hasUse = u.Input, u.Output, true
		}
	case "content_block_start":
		if ev.ContentBlock.Type == "tool_use" {
			s.blocks[ev.Index] = &anthPending{id: ev.ContentBlock.ID, name: ev.ContentBlock.Name}
		}
	case "content_block_delta":
		switch ev.Delta.Type {
		case "text_delta":
			if ev.Delta.Text != "" {
				s.text.WriteString(ev.Delta.Text)
				s.emit(Event{Kind: EventTextDelta, Text: ev.Delta.Text})
			}
		case "input_json_delta":
			if b := s.blocks[ev.Index]; b != nil {
				b.args.WriteString(ev.Delta.PartialJSON)
			}
		}
	case "content_block_stop":
		if b := s.blocks[ev.Index]; b != nil {
			delete(s.blocks, ev.Index)
			args, err := validArgs(b.args.String())
			if err != nil || b.id == "" || b.name == "" {
				return false, &Error{Kind: ErrKindIncomplete, Message: "inference stream contained an incomplete tool call"}
			}
			s.calls = append(s.calls, ToolCall{ID: b.id, Name: b.name, Arguments: args})
		}
	case "message_delta":
		if ev.Delta.StopReason != "" {
			s.reason = ev.Delta.StopReason
		}
		if ev.Usage != nil {
			s.out, s.hasUse = ev.Usage.Output, true
			if ev.Usage.Input > 0 {
				s.in = ev.Usage.Input
			}
		}
	case "message_stop":
		s.stopped = true
		return true, nil
	case "error":
		msg := ""
		if ev.Error != nil {
			msg = strings.Join(strings.Fields(ev.Error.Message), " ")
		}
		return false, &Error{Kind: ErrKindServer, Message: "inference stream reported an error: " + msg}
	}
	return false, nil
}

func (s *anthStream) usage() *Usage {
	if !s.hasUse {
		return nil
	}
	return anthToUsage(s.in, s.out)
}

func (s *anthStream) partial() Result { return Result{Text: s.text.String(), Usage: s.usage()} }

func (s *anthStream) finish() (Result, error) {
	if !s.stopped {
		return s.partial(), &Error{Kind: ErrKindIncomplete, Message: "inference stream ended before completion"}
	}
	if len(s.blocks) > 0 {
		return s.partial(), &Error{Kind: ErrKindIncomplete, Message: "inference stream contained an incomplete tool call"}
	}
	u := s.usage()
	for i := range s.calls {
		tc := s.calls[i]
		s.emit(Event{Kind: EventToolCall, ToolCall: &tc})
	}
	if u != nil {
		s.emit(Event{Kind: EventUsage, Usage: u})
	}
	return Result{Text: s.text.String(), ToolCalls: s.calls, Usage: u, FinishReason: s.reason}, nil
}
