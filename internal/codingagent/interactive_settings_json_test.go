package codingagent

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
	"unsafe"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Interactive mode binds getSettingsJSON, so a state push before every tool, command and event dispatch reuses the settings encoding made once per settings generation instead of encoding getSettings again. The encoding holds the same members as getSettings before and after each settings write, in Pi's member order (the settings files' order, not json.Marshal's sorted one), so the test compares the decoded values.
func TestInteractiveBindsSettingsEncodingOncePerGeneration(t *testing.T) {
	bridge := &captureUIBridge{}
	sm := NewInMemorySettingsManager(settingsWithEveryKind(t))
	m := &InteractiveMode{opts: InteractiveModeOptions{SubprocessUIBridge: bridge, SettingsManager: sm}}
	detach := m.wireSubprocessHostCallbacks()
	defer detach()
	getJSON, ok := bridge.actions["getSettingsJSON"].(func() json.RawMessage)
	if !ok {
		t.Fatalf("getSettingsJSON action = %T, want func() json.RawMessage", bridge.actions["getSettingsJSON"])
	}
	get := bridge.actions["getSettings"].(func() extension.Settings)
	check := func(step string) json.RawMessage {
		t.Helper()
		first := getJSON()
		want, err := json.Marshal(get())
		if err != nil {
			t.Fatal(err)
		}
		var gotValue, wantValue any
		if err := json.Unmarshal(first, &gotValue); err != nil {
			t.Fatalf("%s: getSettingsJSON is not JSON: %v\n%s", step, err, first)
		}
		if err := json.Unmarshal(want, &wantValue); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(gotValue, wantValue) {
			t.Fatalf("%s: getSettingsJSON\n got %s\nwant the members of %s", step, first, want)
		}
		for range 5 {
			if again := getJSON(); unsafe.SliceData(again) != unsafe.SliceData(first) { //nolint:gosec // G103: compares backing-array addresses to prove the encoding is reused; the pointers are never dereferenced
				t.Fatalf("%s: unchanged settings were encoded again", step)
			}
		}
		return first
	}
	before := check("initial")
	if err := sm.SetDefaultModel("next-model"); err != nil {
		t.Fatal(err)
	}
	after := check("after write")
	if bytes.Equal(before, after) || !bytes.Contains(after, []byte(`"next-model"`)) {
		t.Fatalf("the write is missing from the encoding: %s", after)
	}
}

// Without a settings manager the encoding is the empty object getSettings reports.
func TestInteractiveSettingsEncodingWithoutManager(t *testing.T) {
	bridge := &captureUIBridge{}
	m := &InteractiveMode{opts: InteractiveModeOptions{SubprocessUIBridge: bridge}}
	detach := m.wireSubprocessHostCallbacks()
	defer detach()
	getJSON, ok := bridge.actions["getSettingsJSON"].(func() json.RawMessage)
	if !ok {
		t.Fatalf("getSettingsJSON action = %T", bridge.actions["getSettingsJSON"])
	}
	want, _ := json.Marshal(bridge.actions["getSettings"].(func() extension.Settings)())
	if got := getJSON(); !bytes.Equal(got, want) {
		t.Fatalf("getSettingsJSON = %s, want %s", got, want)
	}
}
