package agent

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

// ToolResultMessage declares exactly upstream 0.99.1's ToolResultMessage fields
// (ai/src/types.ts:596-608, which adds nestedCalls). addedToolNames was a 0.84 field that 0.86 removed in
// favor of system messages with toolsAdded; PiG must not keep it. A field JSON skips is not a member.
func TestToolResultMessageFieldsMatchUpstream(t *testing.T) {
	var got []string
	typ := reflect.TypeFor[ToolResultMessage]()
	for field := range typ.Fields() {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "-" {
			// DetailsNull is the presence of details: null, not a member of its own.
			continue
		}
		got = append(got, name)
	}
	slices.Sort(got)
	want := []string{"content", "details", "isError", "nestedCalls", "role", "timestamp", "toolCallId", "toolName", "usage"}
	if !slices.Equal(got, want) {
		t.Fatalf("ToolResultMessage JSON fields = %v, want %v", got, want)
	}
}
