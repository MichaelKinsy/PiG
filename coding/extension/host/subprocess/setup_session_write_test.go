package subprocess

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

type recordingWriter struct{ calls []string }

func (w *recordingWriter) record(call string) (string, error) {
	w.calls = append(w.calls, call)
	return "id-" + string(rune('0'+len(w.calls))), nil
}

func (w *recordingWriter) AppendMessage(m extension.SessionMessage) (string, error) {
	return w.record("appendMessage")
}
func (w *recordingWriter) AppendCustomEntry(customType string, data any) (string, error) {
	return w.record("appendCustomEntry:" + customType + ":" + jsonText(data))
}
func (w *recordingWriter) AppendCustomMessageEntry(customType string, content any, display bool, details any) (string, error) {
	return w.record("appendCustomMessageEntry:" + customType + ":" + jsonText(content) + ":" + map[bool]string{true: "shown", false: "hidden"}[display] + ":" + jsonText(details))
}
func (w *recordingWriter) AppendSessionInfo(name string) (string, error) {
	return w.record("appendSessionInfo:" + name)
}
func (w *recordingWriter) AppendModelChange(provider, modelID string) (string, error) {
	return w.record("appendModelChange:" + provider + "/" + modelID)
}
func (w *recordingWriter) AppendThinkingLevelChange(level string) (string, error) {
	return w.record("appendThinkingLevelChange:" + level)
}
func (w *recordingWriter) AppendLabelChange(targetID string, label *string) (string, error) {
	if label == nil {
		return w.record("appendLabelChange:" + targetID + ":nil")
	}
	return w.record("appendLabelChange:" + targetID + ":" + *label)
}

func jsonText(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}

// session-manager.ts:218-480: each append of a setup request reaches the replacement Session's manager with its arguments and answers the new entry id.
func TestApplySessionWriteReachesEachPiAppend(t *testing.T) {
	user := `{"role":"user","content":[{"type":"text","text":"seed"}],"timestamp":1}`
	cases := []struct{ name, args, want string }{
		{"appendMessage", `{"message":` + user + `}`, "appendMessage"},
		{"appendCustomEntry", `{"customType":"note","data":{"a":1}}`, `appendCustomEntry:note:{"a":1}`},
		{"appendCustomMessageEntry", `{"customType":"c","content":"hi","display":true,"details":{"d":2}}`, `appendCustomMessageEntry:c:"hi":shown:{"d":2}`},
		{"appendSessionInfo", `{"name":"seeded"}`, "appendSessionInfo:seeded"},
		{"appendModelChange", `{"provider":"p","modelId":"m"}`, "appendModelChange:p/m"},
		{"appendThinkingLevelChange", `{"thinkingLevel":"high"}`, "appendThinkingLevelChange:high"},
		{"appendLabelChange", `{"targetId":"e1","label":"keep"}`, "appendLabelChange:e1:keep"},
		{"appendLabelChange", `{"targetId":"e1","label":null}`, "appendLabelChange:e1:nil"},
	}
	for _, c := range cases {
		w := &recordingWriter{}
		id, err := applySessionWrite(w, json.RawMessage(`{"method":"`+c.name+`","args":`+c.args+`}`))
		if err != nil || id != "id-1" || len(w.calls) != 1 || w.calls[0] != c.want {
			t.Errorf("%s: id=%q err=%v calls=%v, want %q", c.name, id, err, w.calls, c.want)
		}
	}
	w := &recordingWriter{}
	if _, err := applySessionWrite(w, json.RawMessage(`{"method":"deleteEverything","args":{}}`)); err == nil || !strings.Contains(err.Error(), "unknown sessionWrite method") || len(w.calls) != 0 {
		t.Fatalf("unknown method: %v calls=%v", err, w.calls)
	}
	if _, err := applySessionWrite(w, json.RawMessage(`{"method":"appendSessionInfo","args":5}`)); err == nil || len(w.calls) != 0 {
		t.Fatalf("bad args: %v calls=%v", err, w.calls)
	}
}
