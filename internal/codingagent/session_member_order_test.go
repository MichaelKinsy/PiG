package codingagent

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// A custom entry's `data` and a custom message's `details` are the JavaScript objects an extension wrote (session-manager.ts appendCustomEntry, appendCustomMessageEntry); the file holds JSON.stringify of them and a session that is loaded again hands them on as JSON.parse read them, in the order written. Neither the file nor the context built from it may sort their members.
func TestSessionKeepsExtensionObjectsInWrittenMemberOrder(t *testing.T) {
	const value = `{"zeta":1,"alpha":{"yy":2,"bb":3},"mid":[{"qq":1,"aa":2}]}`
	sm := tempSessionMgr(t)
	session, err := sm.Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendCustomEntry("probe-entry", json.RawMessage(value)); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendCustomMessage("probe-msg", "note", true, json.RawMessage(value)); err != nil {
		t.Fatal(err)
	}
	flushSession(t, session)
	data, err := os.ReadFile(session.Path())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"customType":"probe-entry","data":` + value, `"customType":"probe-msg","content":"note","display":true,"details":` + value} {
		if !strings.Contains(string(data), want) {
			t.Errorf("session file lacks %s\n%s", want, data)
		}
	}

	reloaded, err := sm.Load(session.Path())
	if err != nil {
		t.Fatal(err)
	}
	var sawEntry, sawMessage bool
	for _, entry := range reloaded.Entries() {
		switch entry.Base.Type {
		case "custom":
			var custom CustomEntry
			if err := json.Unmarshal(entry.Raw(), &custom); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(custom.Data)
			sawEntry = true
			if err != nil || string(encoded) != value {
				t.Errorf("reloaded custom entry data = %s, %v, want %s", encoded, err, value)
			}
		case "custom_message":
			var custom CustomMessageEntry
			if err := json.Unmarshal(entry.Raw(), &custom); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(custom.Details)
			sawMessage = true
			if err != nil || string(encoded) != value {
				t.Errorf("reloaded custom message details = %s, %v, want %s", encoded, err, value)
			}
		}
	}
	if !sawEntry || !sawMessage {
		t.Fatalf("reloaded entries lack the custom entry (%v) or the custom message (%v)", sawEntry, sawMessage)
	}
	var custom string
	for _, message := range reloaded.BuildContext(nil) {
		if message.Custom != nil {
			encoded, err := json.Marshal(message)
			if err != nil {
				t.Fatal(err)
			}
			custom = string(encoded)
		}
	}
	if !strings.HasPrefix(custom, `{"role":"custom","customType":"probe-msg","content":"note","display":true,"details":`+value+`,"timestamp":`) {
		t.Errorf("context message = %s", custom)
	}
}
