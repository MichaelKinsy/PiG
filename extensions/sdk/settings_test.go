package sdk

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Upstream types.ts:1708 and loader.ts:411-414: getSettings returns a copy of the effective settings object (global and project merged, with overrides). The host replicates it with each state.
func TestGetSettingsReturnsACopyOfTheReplicatedSettings(t *testing.T) {
	ext := New("settings")
	type read struct {
		settings Settings
		err      error
	}
	got := make(chan read, 4)
	ext.Command("read", "", func(ctx Context, _ string) error {
		settings, err := ctx.GetSettings()
		// The caller gets a private copy of what it read before it edits the object.
		var snapshot Settings
		if err == nil {
			err = json.Unmarshal([]byte(mustJSON(t, settings)), &snapshot)
		}
		got <- read{snapshot, err}
		if err == nil {
			// The object is the caller's: changing it must not change the settings.
			settings["defaultProvider"] = "mutated"
			if nested, ok := settings["nested"].(map[string]any); ok {
				nested["list"].([]any)[0] = "mutated"
			}
		}
		return nil
	})
	ready := &readyMsg{Cwd: "/tmp", Width: 80, State: json.RawMessage(`{"settings":{"defaultProvider":"anthropic","fullscreenWheelScrollLines":5,"nested":{"list":[1,2]}}}`)}
	host, _, done := surfaceHost(t, ext, ready)
	defer surfaceShutdown(t, host, done)
	step := func() read {
		t.Helper()
		calls, resp := runSurfaceCommand(t, host, "read", func(*callMsg) *callResultMsg { return &callResultMsg{} })
		if resp.Error != nil || len(calls) != 0 {
			t.Fatalf("calls = %+v, response = %+v (getSettings must not call the host)", calls, resp)
		}
		return recv(t, got)
	}
	want := Settings{"defaultProvider": "anthropic", "fullscreenWheelScrollLines": float64(5), "nested": map[string]any{"list": []any{float64(1), float64(2)}}}
	first := step()
	if first.err != nil || !reflect.DeepEqual(first.settings, want) {
		t.Fatalf("settings = %#v, err = %v, want %#v", first.settings, first.err, want)
	}
	if second := step(); second.err != nil || !reflect.DeepEqual(second.settings, want) {
		t.Fatalf("a caller's edit changed the replicated settings: %#v", second.settings)
	}
	// A state update with new settings replaces them; an update without settings keeps them.
	sendState(t, host, `{"settings":{"defaultProvider":"openai"}}`)
	if third := step(); third.err != nil || !reflect.DeepEqual(third.settings, Settings{"defaultProvider": "openai"}) {
		t.Fatalf("settings after update = %#v, err = %v", third.settings, third.err)
	}
	sendState(t, host, `{"hasUI":true}`)
	if fourth := step(); fourth.err != nil || !reflect.DeepEqual(fourth.settings, Settings{"defaultProvider": "openai"}) {
		t.Fatalf("settings after an update without them = %#v, err = %v", fourth.settings, fourth.err)
	}
}

// Upstream loader.ts:177: before the runner binds, getSettings is `notInitialized` and throws. A host that sent no settings is not answered with an empty object.
func TestGetSettingsFailsWhenTheHostSentNone(t *testing.T) {
	ext := New("settings")
	got := make(chan error, 1)
	ext.Command("read", "", func(ctx Context, _ string) error {
		settings, err := ctx.GetSettings()
		if err == nil {
			t.Errorf("settings = %#v without an error", settings)
		}
		got <- err
		return nil
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	runSurfaceCommand(t, host, "read", func(*callMsg) *callResultMsg { return &callResultMsg{} })
	if err := recv(t, got); err == nil {
		t.Fatal("no error")
	}
}
