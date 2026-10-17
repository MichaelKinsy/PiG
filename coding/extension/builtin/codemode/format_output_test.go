package codemode

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	sandbox "github.com/MichaelKinsy/PiG/codemode"
)

// execute.ts formatOutput and joinAdjacentText (v1.1.0): the unit-level contract that the session tests in
// coding/codemode_session_upstream_test.go reach through the tool.
func TestFormatOutputSeparatesTextItemsAndGroupsConsoleLines(t *testing.T) {
	text := func(s string) sandbox.OutputItem { return sandbox.OutputItem{Type: sandbox.OutputItemText, Text: s} }
	console := func(s string) sandbox.OutputItem {
		return sandbox.OutputItem{Type: sandbox.OutputItemText, Text: s, Console: true}
	}
	image := sandbox.OutputItem{Type: sandbox.OutputItemImage, Data: "AAAA", MimeType: "image/png"}
	for _, c := range []struct {
		name   string
		output []sandbox.OutputItem
		want   []ai.ToolResultMessageContent
	}{
		{"no output", nil, []ai.ToolResultMessageContent{}},
		{"one text item has no header", []sandbox.OutputItem{text("a")}, []ai.ToolResultMessageContent{ai.TextContent{Text: "a"}}},
		{"console lines do not count toward the headers", []sandbox.OutputItem{console("x"), text("a")}, []ai.ToolResultMessageContent{ai.TextContent{Text: "a"}, ai.TextContent{Text: "<console_output>\nx\n</console_output>"}}},
		{"several text items are numbered, images keep their place, console is last", []sandbox.OutputItem{text("a"), console("c1"), image, text("b"), console("c2")}, []ai.ToolResultMessageContent{
			ai.TextContent{Text: "==> text 1/2 <==\na"}, ai.ImageContent{Data: "AAAA", MimeType: "image/png"}, ai.TextContent{Text: "==> text 2/2 <==\nb"}, ai.TextContent{Text: "<console_output>\nc1\nc2\n</console_output>"},
		}},
	} {
		if got := formatOutput(c.output); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %#v, want %#v", c.name, got, c.want)
		}
	}
}

func TestJoinAdjacentTextStartsEachPartOnItsOwnLine(t *testing.T) {
	text := func(s string) ai.ToolResultMessageContent { return ai.TextContent{Text: s} }
	image := ai.ImageContent{Data: "AAAA", MimeType: "image/png"}
	got := joinAdjacentText([]ai.ToolResultMessageContent{text("a"), text("b\n"), text("c"), text(""), text("d"), image, text("e"), image, image, text("f")})
	want := []ai.ToolResultMessageContent{text("a\nb\nc\nd"), image, text("e"), image, image, text("f")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}
