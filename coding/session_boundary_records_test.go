package coding

import (
	"bytes"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// A boundary projection hands out, for a message entry the session holds without its record, the record an earlier projection made for the
// same message object: the same bytes boundarywire keys its kept encoding by. Another object under the same id gets its own record, and a
// projection forgets the records it did not use.
func TestBoundaryRecordsHandOutTheRecordOfTheSameObject(t *testing.T) {
	parent := "0a1b2c3d"
	entry := func(text string) icodingagent.MessageEntry {
		return icodingagent.MessageEntry{SessionEntryBase: icodingagent.SessionEntryBase{Type: "message", ID: "4e5f6a7b", ParentID: &parent,
			Timestamp: "2026-10-13T08:00:00.000Z"}, Message: agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser,
			Content: ai.UserText(text), Timestamp: 1}}}
	}
	var records boundaryRecords
	first := entry("first")
	pass := records.begin()
	a := records.record(first, pass)
	b := records.record(first, pass)
	records.forget(pass)
	if !bytes.Equal(a, first.Raw()) || &a[0] != &b[0] {
		t.Fatalf("the same object got %s and %s, want its record once", a, b)
	}
	pass = records.begin()
	if c := records.record(first, pass); &c[0] != &a[0] {
		t.Fatal("the next projection encoded the record of a kept object again")
	}
	records.forget(pass)
	other := entry("other")
	pass = records.begin()
	if d := records.record(other, pass); !bytes.Equal(d, other.Raw()) {
		t.Fatalf("another object under the same id got %s, want %s", d, other.Raw())
	}
	records.forget(pass)
	pass = records.begin()
	if e := records.record(first, pass); &e[0] == &a[0] {
		t.Fatal("a record the previous projection did not use was kept")
	}
	records.forget(pass)
}
