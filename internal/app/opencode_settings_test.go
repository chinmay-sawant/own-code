package app

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/chinmay-sawant/ownframe"
)

func TestNormalizeOpenCodeServerURL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr error
	}{
		{name: "loopback HTTP", input: " http://127.0.0.1:4096/ ", want: "http://127.0.0.1:4096"},
		{name: "localhost HTTP", input: "http://localhost:4096", want: "http://localhost:4096"},
		{name: "remote HTTPS", input: "https://opencode.example", want: "https://opencode.example"},
		{name: "remote HTTP", input: "http://opencode.example", wantErr: errOpenCodeRemoteNeedsHTTPS},
		{name: "embedded password", input: "https://opencode:secret@example.test", wantErr: errInvalidOpenCodeServerURL},
		{name: "path prefix", input: "https://example.test/opencode", wantErr: errInvalidOpenCodeServerURL},
		{name: "unsupported scheme", input: "ftp://127.0.0.1:4096", wantErr: errInvalidOpenCodeServerURL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeOpenCodeServerURL(tt.input)
			if tt.wantErr != nil {
				if err != tt.wantErr {
					t.Fatalf("normalizeOpenCodeServerURL() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeOpenCodeServerURL() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("normalizeOpenCodeServerURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOpenCodeSettingsSanitize(t *testing.T) {
	got := (&opencodeSettings{
		Mode:          "unexpected",
		ServerURL:     "http://remote.example:4096",
		IntegrationID: "  my.provider-1  ",
	}).sanitize()
	if got == nil {
		t.Fatal("sanitize() returned nil")
	}
	if got.Mode != openCodeModeAttach {
		t.Errorf("Mode = %q, want %q", got.Mode, openCodeModeAttach)
	}
	if got.ServerURL != openCodeDefaultURL {
		t.Errorf("ServerURL = %q, want %q", got.ServerURL, openCodeDefaultURL)
	}
	if got.IntegrationID != "my.provider-1" {
		t.Errorf("IntegrationID = %q, want trimmed value", got.IntegrationID)
	}
	if got := sanitizeOpenCodeIntegrationID("bad/id"); got != "" {
		t.Errorf("sanitizeOpenCodeIntegrationID(bad path) = %q, want empty", got)
	}
}

func TestOpenCodeSettingsSerializationContainsMetadataOnly(t *testing.T) {
	data, err := json.Marshal(globalSettings{OpenCode: &opencodeSettings{
		Mode:          openCodeModeAttach,
		ServerURL:     "https://opencode.example",
		IntegrationID: "anthropic",
	}})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `"serverUrl":"https://opencode.example"`) || !strings.Contains(text, `"integrationId":"anthropic"`) {
		t.Fatalf("settings JSON is missing OpenCode metadata: %s", text)
	}
	for _, forbidden := range []string{"serverPassword", "apiKey", "authorization", "secret"} {
		if strings.Contains(strings.ToLower(text), strings.ToLower(forbidden)) {
			t.Errorf("settings JSON contains forbidden credential field marker %q: %s", forbidden, text)
		}
	}
}

func TestOpenCodeSettingsTabRendersCredentialInputsOutsideView(t *testing.T) {
	t.Parallel()

	a := newTestApp(t)
	ctx := context.Background()
	if err := a.onClick(ctx, ownframe.Box{Action: "settings-open"}); err != nil {
		t.Fatal(err)
	}
	if err := a.onClick(ctx, ownframe.Box{Action: "settings-tab:opencode"}); err != nil {
		t.Fatal(err)
	}
	if err := a.page.Redraw(ctx); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{
		"settings-tab-opencode",
		"opencode-server-url",
		"opencode-server-password",
		"opencode-integration-id",
		"opencode-api-key",
		"opencode-save",
	} {
		if _, ok := boxByID(a.page.Boxes(), id); !ok {
			t.Errorf("OpenCode settings control %q is missing", id)
		}
	}
	if _, ok := reflect.TypeOf(a.View()).FieldByName("OpenCodeServerPassword"); ok {
		t.Error("server password must not be copied into View")
	}
	if _, ok := reflect.TypeOf(a.View()).FieldByName("OpenCodeAPIKey"); ok {
		t.Error("provider API key must not be copied into View")
	}
}
