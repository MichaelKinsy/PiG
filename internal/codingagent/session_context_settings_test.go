package codingagent

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// Pi session-manager.ts:getSessionContextSettings uses the latest applicable raw-path state, including assistant metadata that survives context edits or compaction.
func TestGetSessionContextSettingsBoundaries(t *testing.T) {
	cases := []struct {
		name     string
		records  []string
		thinking string
		model    *SessionContextModel
	}{
		{name: "empty", thinking: "off"},
		{name: "latest assistant", records: []string{`{"type":"model_change","provider":"old","modelId":"old"}`, `{"type":"message","message":{"role":"assistant","provider":"new","model":"new","content":[]}}`}, thinking: "off", model: &SessionContextModel{Provider: "new", ModelID: "new"}},
		{name: "latest of two assistants without a thinking change", records: []string{`{"type":"message","message":{"role":"assistant","provider":"old","model":"old","content":[]}}`, `{"type":"message","message":{"role":"assistant","provider":"new","model":"new","content":[]}}`}, thinking: "off", model: &SessionContextModel{Provider: "new", ModelID: "new"}},
		{name: "missing assistant metadata replaces prior", records: []string{`{"type":"model_change","provider":"old","modelId":"old"}`, `{"type":"message","message":{"role":"assistant","content":[]}}`}, thinking: "off", model: &SessionContextModel{}},
		{name: "malformed latest thinking level leaves earlier state", records: []string{`{"type":"thinking_level_change","thinkingLevel":"high"}`, `{"type":"model_change","provider":"old","modelId":"old"}`, `{"type":"thinking_level_change","thinkingLevel":7}`}, thinking: "high", model: &SessionContextModel{Provider: "old", ModelID: "old"}},
		// Pi takes the latest assistant's provider and model whatever their type (session-manager.ts:428), so no registered model matches it; a member of another type reads as "".
		{name: "latest assistant with a wrong-typed provider replaces the model", records: []string{`{"type":"model_change","provider":"old","modelId":"old"}`, `{"type":"message","message":{"role":"assistant","provider":7}}`}, thinking: "off", model: &SessionContextModel{}},
		{name: "empty settings stay explicit", records: []string{`{"type":"thinking_level_change","thinkingLevel":"high"}`, `{"type":"thinking_level_change","thinkingLevel":""}`, `{"type":"model_change"}`}, thinking: "", model: &SessionContextModel{}},
		{name: "state-only and poisoned content", records: []string{`{"type":"message","message":{"role":"assistant","provider":"kept","model":"kept","content":[{"type":"unknown"}]}}`, `{"type":"compaction","summary":"summary"}`, `{"type":"context_edit","targetId":"missing","replacement":null}`, `{"type":"message","message":{"role":"user","content":"latest user"}}`}, thinking: "off", model: &SessionContextModel{Provider: "kept", ModelID: "kept"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var entries []SessionEntry
			for _, raw := range tc.records {
				var base SessionEntryBase
				if err := json.Unmarshal([]byte(raw), &base); err != nil {
					t.Fatal(err)
				}
				entries = append(entries, sessionentry.DecodeSessionEntry(json.RawMessage(raw)))
			}
			thinking, model := GetSessionContextSettings(entries)
			if thinking != tc.thinking || !reflect.DeepEqual(model, tc.model) {
				t.Fatalf("got %q/%+v want %q/%+v", thinking, model, tc.thinking, tc.model)
			}
		})
	}
}

// Pi's getSessionContextSettings reads the parsed entries and never touches message bodies (session-manager.ts getSessionContextSettings). A path of large tool results between the leaf and the latest assistant must not be decoded again.
func TestGetSessionContextSettingsDoesNotScanMessageBodies(t *testing.T) {
	big := strings.Repeat("line of file text\\n", 4000)
	records := []string{`{"type":"message","id":"a","message":{"role":"assistant","provider":"p","model":"m","content":[],"stopReason":"stop","timestamp":1}}`}
	for i := range 20 {
		records = append(records, fmt.Sprintf(`{"type":"message","id":"t%d","message":{"role":"toolResult","toolCallId":"c%d","toolName":"read","content":[{"type":"text","text":%q}],"isError":false,"timestamp":2}}`, i, i, big))
	}
	var entries []SessionEntry
	for _, raw := range records {
		entry := sessionentry.DecodeSessionEntry(json.RawMessage(raw))
		if _, ok := entry.(MessageEntry); !ok {
			t.Fatalf("fixture entry did not decode as a message: %T", entry)
		}
		entries = append(entries, entry)
	}
	_, model := GetSessionContextSettings(entries)
	if model == nil || model.Provider != "p" || model.ModelID != "m" {
		t.Fatalf("model = %+v", model)
	}
	if allocs := testing.AllocsPerRun(20, func() { GetSessionContextSettings(entries) }); allocs > 3 {
		t.Fatalf("%.0f allocations for 20 tool results: message bodies were decoded again", allocs)
	}
}
