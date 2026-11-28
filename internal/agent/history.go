package agent

import (
	"encoding/json"
	"strings"

	"github.com/chinmay-sawant/own-code/internal/agentstore"
)

const pendingToolResult = "Tool call has not completed; its outcome is not available."

// BuildMessages converts persisted history into provider messages. It is the
// only source of provider context: the loop never keeps a parallel in-memory
// transcript, so a restarted process rebuilds exactly what a live one sent.
//
// overrides maps a tool call ID to result text that replaces the stored one
// (used for rejection reasons that the store does not persist).
func BuildMessages(history []agentstore.HistoryMessage, overrides map[string]string) []Message {
	var out []Message
	for _, h := range history {
		var text strings.Builder
		var calls []agentstore.ToolCall
		for _, p := range h.Parts {
			switch p.Kind {
			case agentstore.PartText:
				text.WriteString(p.Text)
			case agentstore.PartToolCall:
				if p.ToolCall != nil {
					calls = append(calls, *p.ToolCall)
				}
			}
		}
		switch h.Role {
		case "user":
			if text.Len() > 0 {
				out = append(out, Message{Role: RoleUser, Content: text.String()})
			}
		case "assistant":
			if text.Len() == 0 && len(calls) == 0 {
				continue
			}
			msg := Message{Role: RoleAssistant, Content: text.String()}
			for _, c := range calls {
				args := json.RawMessage(c.Input)
				if !json.Valid(args) {
					args = json.RawMessage("{}")
				}
				msg.ToolCalls = append(msg.ToolCalls, ToolCall{ID: c.ID, Name: c.ToolName, Arguments: args})
			}
			out = append(out, msg)
			for _, c := range calls {
				result := c.Result
				if o, ok := overrides[c.ID]; ok {
					result = o
				} else if result == "" {
					result = pendingToolResult
				}
				out = append(out, Message{Role: RoleTool, ToolCallID: c.ID, Content: result})
			}
		}
	}
	return out
}
