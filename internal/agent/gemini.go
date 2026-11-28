package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// geminiEndpoint builds <base>/models/<model>:generateContent, or
// :streamGenerateContent?alt=sse when streaming. A base_url that already
// includes the /models/<model> path or the method is accepted.
func (t ModelTarget) geminiEndpoint(u *url.URL, stream bool) (string, error) {
	method := ":generateContent"
	if stream {
		method = ":streamGenerateContent"
	}
	p := strings.TrimRight(u.Path, "/")
	for _, m := range []string{":streamGenerateContent", ":generateContent"} {
		p = strings.TrimSuffix(p, m)
	}
	if !strings.Contains(p, "/models/") {
		if strings.TrimSpace(t.ModelID) == "" || strings.ContainsAny(t.ModelID, "/?#:") {
			return "", errors.New("model ID is not valid for a Gemini route")
		}
		p += "/models/" + t.ModelID
	}
	u.Path, u.RawPath = p+method, ""
	if stream {
		u.RawQuery = "alt=sse"
	}
	return u.String(), nil
}

type geminiAdapter struct{}

type gemPart map[string]any

type gemContent struct {
	Role  string    `json:"role"`
	Parts []gemPart `json:"parts"`
}

func (geminiAdapter) encode(req Request, _ bool) ([]byte, error) {
	var system []string
	var contents []gemContent
	names := map[string]string{} // local call ID -> function name
	push := func(role string, parts ...gemPart) {
		if n := len(contents); n > 0 && contents[n-1].Role == role {
			contents[n-1].Parts = append(contents[n-1].Parts, parts...)
			return
		}
		contents = append(contents, gemContent{Role: role, Parts: parts})
	}
	for _, m := range req.Messages {
		switch m.Role {
		case RoleSystem:
			if m.Content != "" {
				system = append(system, m.Content)
			}
		case RoleTool:
			name := names[m.ToolCallID]
			if name == "" {
				return nil, fmt.Errorf("tool result %q has no matching function call", m.ToolCallID)
			}
			push("user", gemPart{"functionResponse": map[string]any{"name": name, "response": map[string]any{"result": m.Content}}})
		case RoleAssistant:
			var parts []gemPart
			if m.Content != "" {
				parts = append(parts, gemPart{"text": m.Content})
			}
			for _, tc := range m.ToolCalls {
				args := tc.Arguments
				if len(args) == 0 {
					args = json.RawMessage(`{}`)
				}
				names[tc.ID] = tc.Name
				parts = append(parts, gemPart{"functionCall": map[string]any{"name": tc.Name, "args": args}})
			}
			if len(parts) > 0 {
				push("model", parts...)
			}
		default:
			push("user", gemPart{"text": m.Content})
		}
	}
	body := map[string]any{"contents": contents}
	if len(system) > 0 {
		body["systemInstruction"] = map[string]any{"parts": []gemPart{{"text": strings.Join(system, "\n\n")}}}
	}
	if req.MaxOutputTokens > 0 {
		body["generationConfig"] = map[string]any{"maxOutputTokens": req.MaxOutputTokens}
	}
	if len(req.Tools) > 0 {
		decls := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			d := map[string]any{"name": t.Name, "description": t.Description}
			if len(t.Parameters) > 0 {
				d["parametersJsonSchema"] = t.Parameters
			}
			decls = append(decls, d)
		}
		body["tools"] = []map[string]any{{"functionDeclarations": decls}}
	}
	return json.Marshal(body)
}

type gemChunk struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text         string `json:"text"`
				Thought      bool   `json:"thought"`
				FunctionCall *struct {
					ID   string          `json:"id"`
					Name string          `json:"name"`
					Args json.RawMessage `json:"args"`
				} `json:"functionCall"`
			} `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	Usage *struct {
		Prompt     int `json:"promptTokenCount"`
		Candidates int `json:"candidatesTokenCount"`
		Thoughts   int `json:"thoughtsTokenCount"`
		Total      int `json:"totalTokenCount"`
	} `json:"usageMetadata"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// gemAcc accumulates chunks for both the one-shot and the streaming path.
type gemAcc struct {
	emit   func(Event)
	text   strings.Builder
	calls  []ToolCall
	reason string
	usage  *Usage
}

// localCallID is stable for a given response position, name and arguments.
func localCallID(index int, name string, args json.RawMessage) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00%s", index, name, args)))
	return "gemini_call_" + hex.EncodeToString(h[:6])
}

func (a *gemAcc) add(raw []byte) error {
	var c gemChunk
	if err := json.Unmarshal(raw, &c); err != nil {
		return fmt.Errorf("malformed event: %w", err)
	}
	if c.Error != nil {
		return &Error{Kind: ErrKindServer, Message: "inference response reported an error: " + strings.Join(strings.Fields(c.Error.Message), " ")}
	}
	if c.PromptFeedback != nil && c.PromptFeedback.BlockReason != "" {
		return &Error{Kind: ErrKindInvalid, Message: "prompt was blocked by the provider: " + c.PromptFeedback.BlockReason}
	}
	if u := c.Usage; u != nil {
		out := u.Candidates + u.Thoughts
		total := u.Total
		if total == 0 {
			total = u.Prompt + out
		}
		a.usage = &Usage{InputTokens: u.Prompt, OutputTokens: out, TotalTokens: total}
	}
	for _, cand := range c.Candidates[:min(1, len(c.Candidates))] {
		for _, p := range cand.Content.Parts {
			switch {
			case p.FunctionCall != nil:
				fc := p.FunctionCall
				args, err := anthInput(fc.Args)
				if err != nil || fc.Name == "" {
					return &Error{Kind: ErrKindIncomplete, Message: "incomplete tool call in response"}
				}
				id := fc.ID
				if id == "" {
					id = localCallID(len(a.calls), fc.Name, args)
				}
				a.calls = append(a.calls, ToolCall{ID: id, Name: fc.Name, Arguments: args})
			case p.Text != "" && !p.Thought:
				a.text.WriteString(p.Text)
				if a.emit != nil {
					a.emit(Event{Kind: EventTextDelta, Text: p.Text})
				}
			}
		}
		if cand.FinishReason != "" {
			a.reason = cand.FinishReason
		}
	}
	return nil
}

func (geminiAdapter) decode(raw []byte) (Result, error) {
	a := &gemAcc{}
	if err := a.add(raw); err != nil {
		return Result{}, err
	}
	return Result{Text: a.text.String(), ToolCalls: a.calls, Usage: a.usage, FinishReason: a.reason}, nil
}

type gemStream struct{ gemAcc }

func (geminiAdapter) newStream(emit func(Event)) streamState {
	return &gemStream{gemAcc{emit: emit}}
}

func (s *gemStream) handle(data []byte) (bool, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return false, nil
	}
	return false, s.add(data)
}

func (s *gemStream) partial() Result { return Result{Text: s.text.String(), Usage: s.usage} }

// finish requires a finishReason: Gemini has no terminator event, so a stream
// that ends without one is truncated. Tool calls are reported only here.
func (s *gemStream) finish() (Result, error) {
	if s.reason == "" {
		return s.partial(), &Error{Kind: ErrKindIncomplete, Message: "inference stream ended before completion"}
	}
	for i := range s.calls {
		tc := s.calls[i]
		s.emit(Event{Kind: EventToolCall, ToolCall: &tc})
	}
	if s.usage != nil {
		s.emit(Event{Kind: EventUsage, Usage: s.usage})
	}
	return Result{Text: s.text.String(), ToolCalls: s.calls, Usage: s.usage, FinishReason: s.reason}, nil
}
