package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

type chatAdapter struct{}

type chatMsg struct {
	Role       string         `json:"role"`
	Content    *string        `json:"content"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type chatToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func (chatAdapter) encode(req Request, stream bool) ([]byte, error) {
	msgs := make([]chatMsg, 0, len(req.Messages))
	for _, m := range req.Messages {
		content := m.Content
		cm := chatMsg{Role: string(m.Role), ToolCallID: m.ToolCallID}
		if m.Role == RoleAssistant && len(m.ToolCalls) > 0 && content == "" {
			cm.Content = nil
		} else {
			cm.Content = &content
		}
		for _, tc := range m.ToolCalls {
			c := chatToolCall{ID: tc.ID, Type: "function"}
			c.Function.Name, c.Function.Arguments = tc.Name, string(tc.Arguments)
			cm.ToolCalls = append(cm.ToolCalls, c)
		}
		msgs = append(msgs, cm)
	}
	body := map[string]any{"model": req.Model.ModelID, "messages": msgs, "stream": stream}
	if stream {
		body["stream_options"] = map[string]any{"include_usage": true}
	}
	if req.MaxOutputTokens > 0 {
		body["max_tokens"] = req.MaxOutputTokens
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			params := t.Parameters
			if len(params) == 0 {
				params = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			tools = append(tools, map[string]any{"type": "function", "function": map[string]any{
				"name": t.Name, "description": t.Description, "parameters": params}})
		}
		body["tools"] = tools
	}
	return json.Marshal(body)
}

type chatUsage struct {
	Prompt     int `json:"prompt_tokens"`
	Completion int `json:"completion_tokens"`
	Total      int `json:"total_tokens"`
}

func (u *chatUsage) toUsage() *Usage {
	if u == nil {
		return nil
	}
	out := &Usage{InputTokens: u.Prompt, OutputTokens: u.Completion, TotalTokens: u.Total}
	if out.TotalTokens == 0 {
		out.TotalTokens = out.InputTokens + out.OutputTokens
	}
	return out
}

func validArgs(s string) (json.RawMessage, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		s = "{}"
	}
	if !json.Valid([]byte(s)) {
		return nil, errors.New("tool call arguments are not complete JSON")
	}
	return json.RawMessage(s), nil
}

func (chatAdapter) decode(raw []byte) (Result, error) {
	var r struct {
		Choices []struct {
			Message struct {
				Content   *string        `json:"content"`
				ToolCalls []chatToolCall `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage *chatUsage `json:"usage"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return Result{}, err
	}
	if len(r.Choices) == 0 {
		return Result{}, errors.New("response has no choices")
	}
	ch := r.Choices[0]
	res := Result{FinishReason: ch.FinishReason, Usage: r.Usage.toUsage()}
	if ch.Message.Content != nil {
		res.Text = *ch.Message.Content
	}
	for _, tc := range ch.Message.ToolCalls {
		args, err := validArgs(tc.Function.Arguments)
		if err != nil || tc.ID == "" || tc.Function.Name == "" {
			return Result{}, errors.New("incomplete tool call in response")
		}
		res.ToolCalls = append(res.ToolCalls, ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: args})
	}
	return res, nil
}

type chatStream struct {
	emit     func(Event)
	text     strings.Builder
	calls    map[int]*chatToolCall
	reason   string
	usage    *Usage
	finished bool
}

func (chatAdapter) newStream(emit func(Event)) streamState {
	return &chatStream{emit: emit, calls: map[int]*chatToolCall{}}
}

func (s *chatStream) handle(data []byte) (bool, error) {
	data = bytes.TrimSpace(data)
	if string(data) == "[DONE]" {
		return true, nil
	}
	var chunk struct {
		Error   json.RawMessage `json:"error"`
		Choices []struct {
			Delta struct {
				Content   string         `json:"content"`
				ToolCalls []chatToolCall `json:"tool_calls"`
			} `json:"delta"`
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
		Usage *chatUsage `json:"usage"`
	}
	if err := json.Unmarshal(data, &chunk); err != nil {
		return false, fmt.Errorf("malformed event: %w", err)
	}
	if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
		return false, &Error{Kind: ErrKindServer, Message: "inference stream reported an error: " + extractErrorMessage(data)}
	}
	for _, ch := range chunk.Choices {
		if ch.Delta.Content != "" {
			s.text.WriteString(ch.Delta.Content)
			s.emit(Event{Kind: EventTextDelta, Text: ch.Delta.Content})
		}
		for _, tc := range ch.Delta.ToolCalls {
			cur := s.calls[tc.Index]
			if cur == nil {
				cur = &chatToolCall{Index: tc.Index}
				s.calls[tc.Index] = cur
			}
			if tc.ID != "" {
				cur.ID = tc.ID
			}
			if tc.Function.Name != "" {
				cur.Function.Name = tc.Function.Name
			}
			cur.Function.Arguments += tc.Function.Arguments
		}
		if ch.FinishReason != nil && *ch.FinishReason != "" {
			s.reason, s.finished = *ch.FinishReason, true
		}
	}
	if chunk.Usage != nil {
		s.usage = chunk.Usage.toUsage()
	}
	return false, nil
}

func (s *chatStream) partial() Result {
	return Result{Text: s.text.String(), Usage: s.usage}
}

func (s *chatStream) finish() (Result, error) {
	if !s.finished {
		return s.partial(), &Error{Kind: ErrKindIncomplete, Message: "inference stream ended before completion"}
	}
	idx := make([]int, 0, len(s.calls))
	for i := range s.calls {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	res := Result{Text: s.text.String(), Usage: s.usage, FinishReason: s.reason}
	for _, i := range idx {
		c := s.calls[i]
		args, err := validArgs(c.Function.Arguments)
		if err != nil || c.ID == "" || c.Function.Name == "" {
			return s.partial(), &Error{Kind: ErrKindIncomplete, Message: "inference stream contained an incomplete tool call"}
		}
		res.ToolCalls = append(res.ToolCalls, ToolCall{ID: c.ID, Name: c.Function.Name, Arguments: args})
	}
	for i := range res.ToolCalls {
		tc := res.ToolCalls[i]
		s.emit(Event{Kind: EventToolCall, ToolCall: &tc})
	}
	if s.usage != nil {
		s.emit(Event{Kind: EventUsage, Usage: s.usage})
	}
	return res, nil
}
