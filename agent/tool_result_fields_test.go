package agent

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

// ToolResultMessage declares exactly Pi 0.87.1's ToolResultMessage fields
// (ai/src/types.ts). addedToolNames was a 0.84 field that 0.86 removed in
// favor of system messages with toolsAdded; PiG must not keep it.
func TestToolResultMessageFieldsMatchUpstream(t *testing.T) {
	var got []string
	typ := reflect.TypeFor[ToolResultMessage]()
	for field := range typ.Fields() {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		got = append(got, name)
	}
	slices.Sort(got)
	want := []string{"content", "details", "isError", "role", "timestamp", "toolCallId", "toolName", "usage"}
	if !slices.Equal(got, want) {
		t.Fatalf("ToolResultMessage JSON fields = %v, want %v", got, want)
	}
}
