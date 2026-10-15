package codingagent

import (
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

type encodeCounted struct{ calls *atomic.Int64 }

func (c encodeCounted) MarshalJSON() ([]byte, error) {
	c.calls.Add(1)
	return []byte(`{"lines":2000}`), nil
}

// pi session-manager.ts appendMessage stringifies the entry once (JSON.stringify in _persist). Each message is encoded once, its record is what the session stores and counts.
func TestAppendMessageEncodesTheMessageOnce(t *testing.T) {
	var calls atomic.Int64
	session := NewSession("once", "/project")
	if _, err := session.AppendMessage(extension.CustomMessage{CustomType: "note", Content: "text", Display: true, Details: encodeCounted{&calls}}); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("the message's details were encoded %d times for one append", got)
	}
	entries := session.GetEntries()
	if len(entries) != 1 || !strings.Contains(string(entries[0].Raw()), `"details":{"lines":2000}`) {
		t.Fatalf("entry = %s", entries[0].Raw())
	}
}

// A message that cannot be encoded is the message's own error, and nothing is appended.
func TestAppendMessageReportsAnUnencodableMessageAsItsOwnErrorAndAppendsNothing(t *testing.T) {
	session := NewSession("bad", "/project")
	if _, err := session.AppendMessage(extension.CustomMessage{CustomType: "note", Content: "text", Details: make(chan int)}); err != nil {
		if !strings.HasPrefix(err.Error(), "session: AppendMessage: ") || !strings.Contains(err.Error(), "unsupported type: chan int") {
			t.Fatalf("error = %q", err)
		}
	} else {
		t.Fatal("an unencodable message was appended")
	}
	if len(session.GetEntries()) != 0 || session.GetLeafID() != nil {
		t.Fatal("a failed append changed the session")
	}
}
