package codingagent

import (
	"encoding/json"
	"reflect"
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
		{name: "missing assistant metadata replaces prior", records: []string{`{"type":"model_change","provider":"old","modelId":"old"}`, `{"type":"message","message":{"role":"assistant","content":[]}}`}, thinking: "off", model: &SessionContextModel{}},
		{name: "malformed latest fields leave earlier state", records: []string{`{"type":"thinking_level_change","thinkingLevel":"high"}`, `{"type":"model_change","provider":"old","modelId":"old"}`, `{"type":"thinking_level_change","thinkingLevel":7}`, `{"type":"message","message":{"role":"assistant","provider":7}}`}, thinking: "high", model: &SessionContextModel{Provider: "old", ModelID: "old"}},
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
