// Package conformance holds the runner-independent Storage and SessionRepo
// conformance suites every session backend must pass.
package conformance

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

var background = context.Background()

const messageTimestamp = 1_650_000_000_000

// assertJSONEqual compares values by their JSON meaning so backends that
// decode stored values into different Go representations compare equal.
func assertJSONEqual(t *testing.T, got, want any) {
	t.Helper()
	if !jsonEqual(t, got, want) {
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(want)
		t.Fatalf("mismatch\n got: %s\nwant: %s", gotJSON, wantJSON)
	}
}

func jsonEqual(t *testing.T, left, right any) bool {
	t.Helper()
	return reflect.DeepEqual(normalize(t, left), normalize(t, right))
}

func normalize(t *testing.T, value any) any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %T: %v", value, err)
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

// must returns value, failing the test on err: must(call())(t).
func must[T any](value T, err error) func(t *testing.T) T {
	return func(t *testing.T) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
}

func mustErr(t *testing.T, err error, description string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s to fail", description)
	}
}

func equal[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func usage(input, output int, cacheWrite1h, reasoning *int) ai.Usage {
	return ai.Usage{
		Input: input, Output: output, CacheRead: input + 1, CacheWrite: output + 1,
		CacheWrite1h: cacheWrite1h, Reasoning: reasoning, TotalTokens: input + output,
		Cost: ai.UsageCost{
			Input: float64(input) / 100, Output: float64(output) / 100, CacheRead: float64(input+1) / 100,
			CacheWrite: float64(output+1) / 100, Total: float64(input+output+2) / 100,
		},
	}
}

func zeroStats() session.SessionStats { return session.SessionStats{} }

func userMessage(content string, timestamp int64) agent.AgentMessage {
	return agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: []ai.UserContentBlock{ai.TextContent{Text: content}}, Timestamp: timestamp}}
}

func userEntry(id string, parentID *string, content string) session.Entry {
	return session.Entry{ID: id, ParentID: parentID, Type: session.EntryTypeMessage, Message: userMessage(content, messageTimestamp)}
}

func rootUser(id string) session.Entry { return userEntry(id, nil, id) }

func customEntry(id string, parentID *string, customType string, data any) session.Entry {
	return session.Entry{ID: id, ParentID: parentID, Type: session.EntryTypeCustom, CustomType: customType, Data: new(data)}
}

func note(id string, parentID *string) session.Entry {
	return customEntry(id, parentID, "note", map[string]any{"id": id})
}

func bareCustom(id string, parentID *string, customType string) session.Entry {
	return session.Entry{ID: id, ParentID: parentID, Type: session.EntryTypeCustom, CustomType: customType}
}

func compactionEntry(id string, parentID *string) session.Entry {
	return session.Entry{ID: id, ParentID: parentID, Type: session.EntryTypeCompaction, Summary: "summary:" + id, RetainedTail: []agent.AgentMessage{}, TokensBefore: 10}
}

func stamped(entry session.Entry, seq, timestamp int64) session.Entry {
	return session.MaterializeCommittedEntry(entry, seq, timestamp)
}

func entryIDs(entries []session.Entry) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.ID)
	}
	return out
}

func structureIDs(entries []session.EntryStructure) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.ID)
	}
	return out
}

func usageIDs(rows []session.UsageRow) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.ID)
	}
	return out
}

func listValues(elements []session.ListElement[any]) []any {
	out := make([]any, 0, len(elements))
	for _, element := range elements {
		out = append(out, element.Value)
	}
	return out
}

// waitBlocked requires that nothing arrives on channel for a short interval.
func waitBlocked[T any](t *testing.T, channel <-chan T) {
	t.Helper()
	select {
	case <-channel:
		t.Fatal("operation completed before its barrier was released")
	case <-time.After(20 * time.Millisecond):
	}
}
