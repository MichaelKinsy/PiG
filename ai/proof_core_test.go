package ai_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// streaming-json.ts: the final arguments of a call are one full parse of the complete text (Pi's parseToolCallArguments); text that is
// not a JSON object gives empty arguments and a message that names the text.
func TestToolCallArgumentsStreamFinishParsesTheWholeTextAndReportsMalformedArguments(t *testing.T) {
	stream := ai.NewToolCallArgumentsStream()
	for _, piece := range []string{`{"path":"a.txt",`, `"lines":[1,`, `2]}`} {
		stream.Append(piece)
	}
	args, _, message := stream.Finish()
	if message != "" || args["path"] != "a.txt" {
		t.Fatalf("Finish() = %v, %q, want the path argument and no message", args, message)
	}
	if lines, ok := args["lines"].([]any); !ok || len(lines) != 2 {
		t.Errorf("lines = %#v, want the two-item array", args["lines"])
	}
	bad := ai.NewToolCallArgumentsStream()
	bad.Append(`not json`)
	args, _, message = bad.Finish()
	if len(args) != 0 || message == "" {
		t.Errorf("Finish() of malformed text = %v, %q, want empty arguments and a message", args, message)
	}
}

// openai-responses-shared.ts getResponsesToolSupport: changes need mid-conversation system messages and additional tools, additions need
// tool search, otherwise none.
func TestGetResponsesToolSupportFollowsWhatTheTransportSupports(t *testing.T) {
	for _, tc := range []struct {
		options ai.ResponsesToolSupportOptions
		want    ai.TranscriptToolSupport
	}{
		{ai.ResponsesToolSupportOptions{SupportsMidConvoSystemMessages: true, SupportsAdditionalTools: true}, ai.TranscriptToolSupportChanges},
		{ai.ResponsesToolSupportOptions{SupportsMidConvoSystemMessages: true, SupportsAdditionalTools: true, SupportsToolSearch: true}, ai.TranscriptToolSupportChanges},
		{ai.ResponsesToolSupportOptions{SupportsToolSearch: true}, ai.TranscriptToolSupportAdditions},
		{ai.ResponsesToolSupportOptions{SupportsMidConvoSystemMessages: true, SupportsToolSearch: true}, ai.TranscriptToolSupportAdditions},
		{ai.ResponsesToolSupportOptions{SupportsMidConvoSystemMessages: true}, ai.TranscriptToolSupportNone},
		{ai.ResponsesToolSupportOptions{SupportsAdditionalTools: true}, ai.TranscriptToolSupportNone},
		{ai.ResponsesToolSupportOptions{}, ai.TranscriptToolSupportNone},
	} {
		if got := ai.GetResponsesToolSupport(tc.options); got != tc.want {
			t.Errorf("GetResponsesToolSupport(%+v) = %v, want %v", tc.options, got, tc.want)
		}
	}
}

// transform-messages.ts splitErrorToolResultImages: only an error result with images splits; the images move to the caption's message and the
// content keeps a placeholder note.
func TestSplitErrorToolResultImagesSplitsOnlyErrorResultsThatHoldImages(t *testing.T) {
	image := ai.ImageContent{Data: "AAAA", MimeType: "image/png"}
	split := ai.SplitErrorToolResultImages(ai.ToolResultMessage{ToolCallID: "call-1", ToolName: "codemode", IsError: true, Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "failed"}, image}})
	if split == nil || len(split.Images) != 1 || split.Images[0] != image {
		t.Fatalf("split = %+v, want the one image", split)
	}
	if want := "Images from the error result of tool call call-1 (codemode):"; split.Caption != want {
		t.Errorf("Caption = %q, want %q", split.Caption, want)
	}
	if len(split.Content) != 2 || split.Content[0].Text != "failed" || split.Content[1].Text != "(image attached after the tool results)" {
		t.Errorf("Content = %+v, want the text then the placeholder", split.Content)
	}
	if got := ai.SplitErrorToolResultImages(ai.ToolResultMessage{ToolCallID: "c", Content: []ai.ToolResultMessageContent{image}}); got != nil {
		t.Errorf("a non-error result split to %+v, want nil", got)
	}
	if got := ai.SplitErrorToolResultImages(ai.ToolResultMessage{ToolCallID: "c", IsError: true, Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "x"}}}); got != nil {
		t.Errorf("an error result without images split to %+v, want nil", got)
	}
}
