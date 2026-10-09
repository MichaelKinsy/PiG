package tui

import (
	"strings"
	"testing"
	"time"
)

// upstream: modes/interactive/components/tool-execution.ts:187-200 updateResult(result, isPartial) stores the result and
// the partial flag. A partial result streams its text, a final one finishes the card with its error state and duration.
func TestToolExecutionUpdateResultPartialThenFinal(t *testing.T) {
	c := newToolCardForTest("custom", "custom args")
	c.UpdateResult(ToolResultUpdate{Content: []ToolResultContent{{Type: "text", Text: "one"}, {Type: "text", Text: "two"}}}, true)
	if c.State != ToolStateRunning || c.Output != "one\ntwo" {
		t.Fatalf("partial: state %v output %q, want running and the joined text", c.State, c.Output)
	}
	if c.DurationMs != nil {
		t.Fatalf("partial result recorded a duration %v", *c.DurationMs)
	}
	ms := int64(1250)
	c.UpdateResult(ToolResultUpdate{
		Content:    []ToolResultContent{{Type: "text", Text: "boom"}},
		IsError:    true,
		DurationMs: &ms,
		Elapsed:    1250 * time.Millisecond,
	}, false)
	if c.State != ToolStateError || c.Output != "boom" || c.Elapsed != 1250*time.Millisecond {
		t.Fatalf("final: state %v output %q elapsed %v", c.State, c.Output, c.Elapsed)
	}
	if c.DurationMs == nil || *c.DurationMs != 1250 {
		t.Fatalf("final DurationMs = %v, want 1250", c.DurationMs)
	}
	if !strings.Contains(strings.Join(c.Render(80), "\n"), "boom") {
		t.Fatal("final output not rendered")
	}
}

// upstream: tool-execution.ts:187 updateResult replaces the whole result, so image blocks come from the newest result
// (partial or final) and a later result without images drops them; the host value reaches definition renderers.
func TestToolExecutionUpdateResultReplacesImagesAndValue(t *testing.T) {
	c := newToolCardForTest("custom", "")
	c.UpdateResult(ToolResultUpdate{
		Content: []ToolResultContent{{Type: "image", Data: "AAAA", MimeType: "image/png"}, {Type: "text", Text: "x"}},
		Result:  "first",
	}, true)
	if len(c.ImageBlocks) != 1 || c.ImageBlocks[0].Data != "AAAA" || c.ImageBlocks[0].MIMEType != "image/png" {
		t.Fatalf("partial image blocks = %+v", c.ImageBlocks)
	}
	if c.ResultValue() != "first" {
		t.Fatalf("ResultValue = %v", c.ResultValue())
	}
	c.UpdateResult(ToolResultUpdate{Content: []ToolResultContent{{Type: "text", Text: "done"}}}, false)
	if len(c.ImageBlocks) != 0 {
		t.Fatalf("final result without images kept %+v", c.ImageBlocks)
	}
	if c.ResultValue() != "first" {
		t.Fatalf("a result without Value must keep the previous value, got %v", c.ResultValue())
	}
	if c.State != ToolStateDone || c.Output != "done" {
		t.Fatalf("final: state %v output %q", c.State, c.Output)
	}
}
