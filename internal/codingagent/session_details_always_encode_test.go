package codingagent

import (
	"math"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

type selfHolding struct {
	Name string
	Next *selfHolding
}

type embedsDetails struct {
	tools.ReadDetails
	Count int `json:"count,omitempty"`
}

type quotedNumber struct {
	Count int `json:"count,string"`
}

type namedText string

// alwaysEncodes holds only for a type whose every value the encoder writes without an error: each value below of such a type encodes, and
// the types that can hold a rejected value (a cycle, a channel, a marshaler, a dynamic type) are not such types.
func TestAlwaysEncodesHoldsOnlyForTypesWhoseValuesAllEncode(t *testing.T) {
	text := "package main\n\tfunc main() {\"<&>\"}\n\xff\xed\xa0\x80"
	truncation := &tools.TruncationResult{Content: text, Truncated: true, TruncatedBy: "lines", TotalLines: 9, OutputLines: 2, MaxLines: 2}
	cycle := &selfHolding{Name: "a"}
	cycle.Next = cycle
	cases := []struct {
		name   string
		value  any
		always bool
	}{
		{"read details", &tools.ReadDetails{Truncation: truncation}, true},
		{"read details without truncation", &tools.ReadDetails{}, true},
		{"read details value", tools.ReadDetails{Truncation: truncation}, true},
		{"bash details", &tools.BashDetails{Truncation: truncation, FullOutputPath: "/tmp/out.txt"}, true},
		{"grep details", &tools.GrepDetails{Truncation: truncation, MatchLimitReached: math.NaN()}, true},
		{"find details", &tools.FindDetails{Truncation: truncation}, true},
		{"ls details", &tools.LsDetails{Truncation: truncation, EntryLimitReached: math.Inf(-1)}, true},
		{"embedded details", embedsDetails{Truncation: truncation, Count: 2}, true},
		{"typed slice", []string{"a", text}, true},
		{"array", [2]int{1, 2}, true},
		{"bytes", []byte{1, 2, 3}, true},
		{"empty struct", struct{}{}, true},
		{"pointer to pointer", &[]*tools.ReadDetails{{}}[0], true},
		{"map", map[string]any{"a": 1.0}, false},
		{"typed map", map[string]int{"a": 1}, false},
		{"any slice", []any{1.0, "x"}, false},
		{"float", math.NaN(), true},
		{"struct with a float", struct{ Ratio float64 }{math.Inf(1)}, true},
		{"struct with an interface", struct{ Value any }{make(chan int)}, false},
		{"self-holding type", cycle, false},
		{"channel", make(chan int), false},
		{"function", func() {}, false},
		{"named string", namedText("x"), false},
		{"quoted number option", quotedNumber{Count: 1}, false},
		{"time", time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC), false},
		{"marshaler", nullDetails{Note: "n"}, false},
		{"pointer text marshaler", &textDetails{Note: "n"}, false},
		{"nested typed details", nestedDetails{Lines: []string{"a"}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			always := alwaysEncodes(reflect.TypeOf(c.value))
			if always != c.always {
				t.Fatalf("alwaysEncodes = %v, want %v", always, c.always)
			}
			if _, err := jsstring.MarshalJSON(c.value); always && err != nil {
				t.Fatalf("a value of a type that always encodes did not: %v", err)
			}
		})
	}
	if alwaysEncodes(nil) {
		t.Fatal("a nil type always encodes")
	}
}

// A session with no file holds a built-in tool's result without encoding its details: the encoding was made only to learn that it
// succeeds, and for these types it always does. Pi stringifies an entry only to persist it (session-manager.ts _persist).
func TestASessionWithoutAFileDoesNotEncodeDetailsThatAlwaysEncode(t *testing.T) {
	text := strings.Repeat("a line of the file the tool read\n", 2000)
	result := agent.ToolResultMessage{Role: agent.RoleToolResult, ToolCallID: "call_1", ToolName: "read", Timestamp: 1760000000000,
		Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text + "\n\n[notice]"}},
		Details: &tools.ReadDetails{Truncation: &tools.TruncationResult{Content: text, Truncated: true, TruncatedBy: "bytes"}}}
	parent := "0a1b2c3d"
	entry := MessageEntry{Type: "message", ID: "4e5f6a7b", ParentID: &parent, Timestamp: "2026-10-13T08:00:00.000Z", Message: agent.AgentMessage{ToolResult: &result}}
	session := &Session{}
	if !session.holdsWithoutRecord(entry, proveHeldEntry(entry)) {
		t.Fatal("the session does not hold a read result as appended")
	}
	const runs = 20
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range runs {
		if !session.holdsWithoutRecord(entry, proveHeldEntry(entry)) {
			t.Fatal("the session does not hold a read result as appended")
		}
	}
	runtime.ReadMemStats(&after)
	if perRun := (after.TotalAlloc - before.TotalAlloc) / runs; perRun >= uint64(len(text)) {
		t.Fatalf("holding the result allocated %d bytes a time, as much as its %d bytes of details: they were encoded", perRun, len(text))
	}
}
