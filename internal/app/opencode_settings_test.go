package app

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/chinmay-sawant/ownframe"
)

func TestOpenCodeSettingsSanitize(t *testing.T) {
	got := (&opencodeSettings{
		ProviderID: "  opencode-go  ",
		ModelID:    "  model-id  ",
	}).sanitize()
	if got == nil {
		t.Fatal("sanitize() returned nil")
	}
	if got.ProviderID != "opencode-go" || got.ModelID != "model-id" {
		t.Errorf("selected model = %q/%q, want trimmed values", got.ProviderID, got.ModelID)
	}
}

func TestOpenCodeSettingsSerializationContainsModelOnly(t *testing.T) {
	data, err := json.Marshal(globalSettings{OpenCode: &opencodeSettings{
		ProviderID: "opencode-go",
		ModelID:    "example-model",
	}})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `"providerId":"opencode-go"`) || !strings.Contains(text, `"modelId":"example-model"`) {
		t.Fatalf("settings JSON is missing the selected OpenCode model: %s", text)
	}
	for _, forbidden := range []string{"serverUrl", "integrationId", "serverPassword", "apiKey", "authorization", "secret"} {
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
		"opencode-api-key",
		"opencode-connect",
		"opencode-model",
		"opencode-model-save",
		"opencode-key-clear",
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
