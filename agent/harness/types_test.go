package harness

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestAgentToolResultJSONRoundTripsContentUnion(t *testing.T) {
	result := AgentToolResult{
		Content:   []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}, ai.ImageContent{Data: "AA==", MimeType: "image/png"}},
		Details:   map[string]any{"n": 1.0},
		Terminate: new(true),
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"content":[{"type":"text","text":"ok"},{"type":"image","data":"AA==","mimeType":"image/png"}],"details":{"n":1},"terminate":true}`
	if string(encoded) != want {
		t.Fatalf("JSON = %s", encoded)
	}
	var decoded AgentToolResult
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Content) != 2 || decoded.Content[1].(ai.ImageContent).MimeType != "image/png" || !*decoded.Terminate {
		t.Fatalf("decoded = %#v", decoded)
	}
	if err := json.Unmarshal([]byte(`{"content":[{"type":"toolCall","id":"x","name":"y","arguments":{}}]}`), &decoded); err == nil {
		t.Fatal("tool call accepted as tool result content")
	}
}

func TestDeferredOptionJSONForms(t *testing.T) {
	for _, testCase := range []struct {
		option AgentHarnessDeferredOption
		want   string
	}{
		{AgentHarnessDeferredOption{Enabled: true}, `true`},
		{AgentHarnessDeferredOption{}, `false`},
		{AgentHarnessDeferredOption{Object: true}, `{}`},
		{AgentHarnessDeferredOption{Object: true, Window: "1h"}, `{"window":"1h"}`},
	} {
		encoded, err := json.Marshal(testCase.option)
		if err != nil || string(encoded) != testCase.want {
			t.Fatalf("%#v => %s, %v", testCase.option, encoded, err)
		}
		var decoded AgentHarnessDeferredOption
		if err := json.Unmarshal(encoded, &decoded); err != nil || decoded != testCase.option {
			t.Fatalf("%s decoded %#v, %v", encoded, decoded, err)
		}
	}
}

func TestStreamOptionsOmitAbsentFields(t *testing.T) {
	encoded, err := json.Marshal(AgentHarnessStreamOptions{})
	if err != nil || string(encoded) != `{}` {
		t.Fatalf("empty options = %s, %v", encoded, err)
	}
	options := AgentHarnessStreamOptions{Transport: ai.TransportSSE, TimeoutMs: new(0), Headers: map[string]string{}, Deferred: &AgentHarnessDeferredOption{Object: true, Window: "24h"}}
	encoded, err = json.Marshal(options)
	if err != nil || string(encoded) != `{"transport":"sse","timeoutMs":0,"deferred":{"window":"24h"}}` {
		t.Fatalf("options = %s, %v", encoded, err)
	}
}

func TestShellOutputUpdateJSONKinds(t *testing.T) {
	metadata := ShellOutputMetadata{Truncation: ShellOutputTruncation{MaxLines: 2, MaxBytes: 10}, LastLineBytes: new(3)}
	cases := []struct {
		update ShellOutputUpdate
		want   string
	}{
		{ShellOutputUpdate{Kind: ShellOutputUpdateAppend, Text: "", Metadata: metadata}, `{"kind":"append","text":"","metadata":{"truncation":{"truncated":false,"truncatedBy":null,"totalLines":0,"totalBytes":0,"outputLines":0,"outputBytes":0,"lastLinePartial":false,"firstLineExceedsLimit":false,"maxLines":2,"maxBytes":10},"lastLineBytes":3}}`},
		{ShellOutputUpdate{Kind: ShellOutputUpdateReplace, Output: ShellOutputView{Text: "x", ShellOutputMetadata: ShellOutputMetadata{SpillPath: "/tmp/s"}}}, `{"kind":"replace","output":{"text":"x","truncation":{"truncated":false,"truncatedBy":null,"totalLines":0,"totalBytes":0,"outputLines":0,"outputBytes":0,"lastLinePartial":false,"firstLineExceedsLimit":false,"maxLines":0,"maxBytes":0},"spillPath":"/tmp/s"}}`},
	}
	for _, testCase := range cases {
		encoded, err := json.Marshal(testCase.update)
		if err != nil || string(encoded) != testCase.want {
			t.Fatalf("%s\n got: %s (%v)\nwant: %s", testCase.update.Kind, encoded, err, testCase.want)
		}
		var decoded ShellOutputUpdate
		if err := json.Unmarshal(encoded, &decoded); err != nil || decoded.Kind != testCase.update.Kind {
			t.Fatalf("decode %s: %#v, %v", encoded, decoded, err)
		}
	}
	slide, err := json.Marshal(ShellOutputUpdate{Kind: ShellOutputUpdateSlide, Drop: 4, Text: "ab", Metadata: metadata})
	if err != nil {
		t.Fatal(err)
	}
	var decoded ShellOutputUpdate
	if err := json.Unmarshal(slide, &decoded); err != nil || decoded.Drop != 4 || decoded.Text != "ab" {
		t.Fatalf("slide = %#v, %v", decoded, err)
	}
	if _, err := json.Marshal(ShellOutputUpdate{Kind: "bogus"}); err == nil {
		t.Fatal("unknown kind marshalled")
	}
	if err := json.Unmarshal([]byte(`{"kind":"bogus"}`), &decoded); err == nil {
		t.Fatal("unknown kind decoded")
	}
}

func TestFileAndExecutionErrorsUnwrapCause(t *testing.T) {
	cause := errors.New("EACCES")
	fileErr := error(&FileError{Code: FileErrorPermissionDenied, Message: "denied", Path: "/x", Cause: cause})
	var typed *FileError
	if !errors.As(fileErr, &typed) || typed.Code != FileErrorPermissionDenied || !errors.Is(fileErr, cause) || fileErr.Error() != "denied" {
		t.Fatalf("file error = %#v", fileErr)
	}
	for _, err := range []error{
		&ExecutionError{Code: ExecutionErrorTimeout, Message: "timeout", Cause: cause},
		&CompactionError{Code: CompactionErrorAborted, Message: "aborted", Cause: cause},
		&BranchSummaryError{Code: BranchSummaryErrorSummarizationFailed, Message: "failed", Cause: cause},
	} {
		if !errors.Is(err, cause) || err.Error() == "" {
			t.Fatalf("error %#v does not unwrap", err)
		}
	}
}
