package opencode

import "testing"

func TestConsoleModelSupportsAgentRuntime(t *testing.T) {
	tests := []struct {
		name  string
		model ConsoleModel
		want  bool
	}{
		{
			name: "chat completions",
			model: ConsoleModel{
				Type: "chat", ToolsKnown: true, ToolsSupported: true,
				APIs: map[string]ConsoleModelAPI{
					"chat-completions": {BaseURL: "https://opencode.ai/inference/openai/v1"},
				},
			},
			want: true,
		},
		{
			name: "responses",
			model: ConsoleModel{
				Type: "chat", ToolsKnown: true, ToolsSupported: true,
				APIs: map[string]ConsoleModelAPI{
					"responses": {BaseURL: "https://opencode.ai/inference/openai/v1"},
				},
			},
			want: true,
		},
		{
			name: "tool support unknown",
			model: ConsoleModel{
				Type: "chat",
				APIs: map[string]ConsoleModelAPI{
					"chat-completions": {BaseURL: "https://opencode.ai/inference/openai/v1"},
				},
			},
		},
		{
			name: "tools unsupported",
			model: ConsoleModel{
				Type: "chat", ToolsKnown: true,
				APIs: map[string]ConsoleModelAPI{
					"chat-completions": {BaseURL: "https://opencode.ai/inference/openai/v1"},
				},
			},
		},
		{
			name: "unknown protocol",
			model: ConsoleModel{
				Type: "chat", ToolsKnown: true, ToolsSupported: true,
				APIs: map[string]ConsoleModelAPI{
					"messages": {BaseURL: "https://opencode.ai/inference/anthropic/v1"},
				},
			},
		},
		{
			name: "empty base URL",
			model: ConsoleModel{
				Type: "chat", ToolsKnown: true, ToolsSupported: true,
				APIs: map[string]ConsoleModelAPI{"responses": {}},
			},
		},
		{
			name: "missing model type",
			model: ConsoleModel{
				ToolsKnown: true, ToolsSupported: true,
				APIs: map[string]ConsoleModelAPI{
					"chat-completions": {BaseURL: "https://opencode.ai/inference/openai/v1"},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.model.SupportsAgentRuntime(); got != test.want {
				t.Fatalf("SupportsAgentRuntime() = %t, want %t", got, test.want)
			}
		})
	}
}
