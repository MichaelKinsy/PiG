package subprocess

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi's ToolDefinition.promptSnippet reaches the host's tool definition.
func TestHost_BuildExtension_ToolPromptSnippet(t *testing.T) {
	h := NewHost(t.TempDir())
	me := &managedExt{config: ExtConfig{Name: "snippet-ext", Path: "/bin/true"}}
	ext := h.buildExtension(me, &RegisterPayload{Name: "snippet-ext", Tools: []ToolDecl{{
		Name: "lookup", Label: "Lookup", Description: "d", Parameters: json.RawMessage(`{"type":"object"}`),
		PromptSnippet: "Look up a symbol",
	}}})
	def := ext.Tools["lookup"].Definition
	if def.PromptSnippet != "Look up a symbol" || def.Label != "Lookup" {
		t.Fatalf("definition = %+v, want the declared prompt snippet and label", def)
	}
}

// Pi's AgentToolResult.usage survives the wire.
func TestToolResultUnmarshal_Usage(t *testing.T) {
	var r ToolResult
	if err := json.Unmarshal([]byte(`{"content":"ok","usage":{"input":3,"output":4,"cacheRead":0,"cacheWrite":0,"totalTokens":7,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}}`), &r); err != nil {
		t.Fatal(err)
	}
	if r.Usage == nil || r.Usage.Input != 3 || r.Usage.Output != 4 || r.Usage.TotalTokens != 7 {
		t.Fatalf("usage = %+v", r.Usage)
	}
}

// ctx.compact({ onComplete, onError }): with awaitCompletion the call answers
// when compaction finishes, with the result or the failure.
func TestUIBridge_CompactAwaitsCompletion(t *testing.T) {
	var got *extension.CompactOptions
	b := newTestBridge(&mockUIContext{})
	b.SetActions(&HostCallbacks{Compact: func(_ context.Context, opts *extension.CompactOptions) {
		got = opts
		go opts.OnComplete(map[string]any{"summary": "short", "firstKeptEntryId": "e9", "tokensBefore": 12})
	}})
	result, err := call(b, "compact", `{"customInstructions":"keep todos","awaitCompletion":true}`)
	if err != nil || result.Error != nil {
		t.Fatalf("compact = %+v, %v", result, err)
	}
	if got == nil || got.CustomInstructions != "keep todos" {
		t.Fatalf("options = %+v", got)
	}
	var decoded map[string]any
	if err := json.Unmarshal(result.Result, &decoded); err != nil || decoded["summary"] != "short" || decoded["firstKeptEntryId"] != "e9" {
		t.Fatalf("result = %s (%v)", result.Result, err)
	}

	b.SetActions(&HostCallbacks{Compact: func(_ context.Context, opts *extension.CompactOptions) {
		opts.OnError(errors.New("Nothing to compact (session too small)"))
	}})
	result, err = call(b, "compact", `{"awaitCompletion":true}`)
	if err != nil || result.Error == nil || result.Error.Code != "" || result.Error.Message != "Nothing to compact (session too small)" {
		t.Fatalf("failed compact = %+v, %v", result, err)
	}

	// Without the flag the call stays fire-and-forget and sets no callbacks.
	b.SetActions(&HostCallbacks{Compact: func(_ context.Context, opts *extension.CompactOptions) { got = opts }})
	if _, err := call(b, "compact", `{"customInstructions":"x"}`); err != nil {
		t.Fatal(err)
	}
	if got.OnComplete != nil || got.OnError != nil {
		t.Fatal("a compact without awaitCompletion must not install callbacks")
	}

	// A host without compaction reports it to an awaiting caller.
	b.SetActions(&HostCallbacks{})
	result, err = call(b, "compact", `{"awaitCompletion":true}`)
	if err != nil || result.Error == nil || result.Error.Message != "compaction is not available" {
		t.Fatalf("unavailable compact = %+v, %v", result, err)
	}
}
