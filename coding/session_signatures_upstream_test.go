package coding

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// agent-session.ts: compact(customInstructions?): Promise<CompactionResult>, executeBash(command, onChunk?, options?): Promise<BashResult> and prompt(text, options?): Promise<void>. Each is one method with Pi's parameters and result.
var (
	_ func(*Session, context.Context, string) (*CompactionResult, error)                             = (*Session).Compact
	_ func(*Session, context.Context, string, func(string), *ExecuteBashOptions) (BashResult, error) = (*Session).ExecuteBash
	_ func(*Session, context.Context, string, ...*PromptOptions) error                               = (*Session).Prompt
)

// compact() rejects with "Nothing to compact (session too small)" for an empty session (agent-session.ts compact), rather than reporting success.
func TestCompactRejectsAnEmptySessionLikePi(t *testing.T) {
	session := newBashTestSession(t, `{}`)
	result, err := session.Compact(t.Context(), "")
	if err == nil || !strings.Contains(err.Error(), "Nothing to compact") {
		t.Fatalf("Compact = %+v, %v, want the Nothing to compact error", result, err)
	}
	if result != nil {
		t.Fatalf("a rejected compaction returns no result, got %+v", result)
	}
}

// executeBash(command, onChunk, { excludeFromContext }) streams each chunk to onChunk and records the result; excludeFromContext keeps it out of the LLM context.
func TestExecuteBashTakesPisOnChunkAndOptions(t *testing.T) {
	session := newBashTestSession(t, `{}`)
	var chunks []string
	result, err := session.ExecuteBash(t.Context(), "echo hello", func(chunk string) { chunks = append(chunks, chunk) }, &ExecuteBashOptions{ExcludeFromContext: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(strings.Join(chunks, "")) != "hello" || strings.TrimSpace(result.Output) != "hello" {
		t.Fatalf("chunks %q, output %q, want hello", chunks, result.Output)
	}
	encoded, err := json.Marshal(session.Entries())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"command":"echo hello"`) || !strings.Contains(string(encoded), `"excludeFromContext":true`) {
		t.Fatalf("entries = %s, want the echo hello bashExecution excluded from context", encoded)
	}
}
