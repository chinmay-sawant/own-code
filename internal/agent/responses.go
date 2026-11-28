package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type responsesAdapter struct{}

func (responsesAdapter) encode(req Request, stream bool) ([]byte, error) {
	var input []map[string]any
	for _, m := range req.Messages {
		switch m.Role {
		case RoleTool:
			input = append(input, map[string]any{"type": "function_call_output", "call_id": m.ToolCallID, "output": m.Content})
		case RoleAssistant:
			if m.Content != "" {
				input = append(input, map[string]any{"role": "assistant", "content": m.Content})
			}
			for _, tc := range m.ToolCalls {
				input = append(input, map[string]any{"type": "function_call", "call_id": tc.ID, "name": tc.Name, "arguments": string(tc.Arguments)})
			}
		default:
			input = append(input, map[string]any{"role": string(m.Role), "content": m.Content})
		}
	}
	body := map[string]any{"model": req.Model.ModelID, "input": input, "stream": stream, "store": false}
	if req.MaxOutputTokens > 0 {
		body["max_output_tokens"] = req.MaxOutputTokens
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			params := t.Parameters
			if len(params) == 0 {
				params = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			tools = append(tools, map[string]any{"type": "function", "name": t.Name, "description": t.Description, "parameters": params})
		}
		body["tools"] = tools
	}
	return json.Marshal(body)
}

type respUsage struct {
	Input  int `json:"input_tokens"`
	Output int `json:"output_tokens"`
	Total  int `json:"total_tokens"`
}

func (u *respUsage) toUsage() *Usage {
	if u == nil {
		return nil
	}
	out := &Usage{InputTokens: u.Input, OutputTokens: u.Output, TotalTokens: u.Total}
	if out.TotalTokens == 0 {
		out.TotalTokens = out.InputTokens + out.OutputTokens
	}
	return out
}

type respItem struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Content   []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func (it respItem) toolCall() (ToolCall, error) {
	args, err := validArgs(it.Arguments)
	id := it.CallID
	if id == "" {
		id = it.ID
	}
	if err != nil || id == "" || it.Name == "" {
		return ToolCall{}, errors.New("incomplete tool call")
	}
	return ToolCall{ID: id, Name: it.Name, Arguments: args}, nil
}

func (responsesAdapter) decode(raw []byte) (Result, error) {
	var r struct {
		Status string     `json:"status"`
		Output []respItem `json:"output"`
		Usage  *respUsage `json:"usage"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return Result{}, err
	}
	if r.Error != nil || (r.Status != "" && r.Status != "completed") {
		return Result{}, &Error{Kind: ErrKindIncomplete, Message: "inference response status: " + r.Status}
	}
	res := Result{Usage: r.Usage.toUsage(), FinishReason: r.Status}
	var text strings.Builder
	for _, it := range r.Output {
		switch it.Type {
		case "message":
			for _, c := range it.Content {
				if c.Type == "output_text" {
					text.WriteString(c.Text)
				}
			}
		case "function_call":
			tc, err := it.toolCall()
			if err != nil {
				return Result{}, err
			}
			res.ToolCalls = append(res.ToolCalls, tc)
		}
	}
	res.Text = text.String()
	return res, nil
}

type respStream struct {
	emit      func(Event)
	text      strings.Builder
	calls     []ToolCall
	usage     *Usage
	completed bool
	status    string
}

func (responsesAdapter) newStream(emit func(Event)) streamState { return &respStream{emit: emit} }

func (s *respStream) handle(data []byte) (bool, error) {
	data = bytes.TrimSpace(data)
	if string(data) == "[DONE]" {
		return true, nil
	}
	var ev struct {
		Type     string   `json:"type"`
		Delta    string   `json:"delta"`
		Item     respItem `json:"item"`
		Message  string   `json:"message"`
		Response struct {
			Status string     `json:"status"`
			Usage  *respUsage `json:"usage"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		} `json:"response"`
	}
	if err := json.Unmarshal(data, &ev); err != nil {
		return false, fmt.Errorf("malformed event: %w", err)
	}
	switch ev.Type {
	case "response.output_text.delta":
		if ev.Delta != "" {
			s.text.WriteString(ev.Delta)
			s.emit(Event{Kind: EventTextDelta, Text: ev.Delta})
		}
	case "response.output_item.done":
		if ev.Item.Type == "function_call" {
			tc, err := ev.Item.toolCall()
			if err != nil {
				return false, &Error{Kind: ErrKindIncomplete, Message: "inference stream contained an incomplete tool call"}
			}
			s.calls = append(s.calls, tc)
		}
	case "response.completed":
		s.completed, s.status = true, "completed"
		s.usage = ev.Response.Usage.toUsage()
		return true, nil
	case "response.failed", "response.incomplete", "error":
		msg := ev.Message
		if ev.Response.Error != nil {
			msg = ev.Response.Error.Message
		}
		msg = strings.Join(strings.Fields(msg), " ")
		return false, &Error{Kind: ErrKindServer, Message: "inference stream reported " + ev.Type + ": " + msg}
	}
	return false, nil
}

func (s *respStream) partial() Result {
	return Result{Text: s.text.String(), Usage: s.usage}
}

func (s *respStream) finish() (Result, error) {
	if !s.completed {
		return s.partial(), &Error{Kind: ErrKindIncomplete, Message: "inference stream ended before completion"}
	}
	for i := range s.calls {
		tc := s.calls[i]
		s.emit(Event{Kind: EventToolCall, ToolCall: &tc})
	}
	if s.usage != nil {
		s.emit(Event{Kind: EventUsage, Usage: s.usage})
	}
	return Result{Text: s.text.String(), ToolCalls: s.calls, Usage: s.usage, FinishReason: s.status}, nil
}
