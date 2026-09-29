package codingagent

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

type extensionCompactHandle struct {
	*recordingCompactHandle
	result       any
	err          error
	instructions chan string
}

func (h *extensionCompactHandle) CompactForExtension(_ context.Context, customInstructions string) (any, error) {
	h.instructions <- customInstructions
	return h.result, h.err
}

// Upstream ctx.compact({ onComplete, onError }) starts compaction without
// awaiting it and reports the outcome through the callbacks.
func TestCompactForExtensionReportsThroughCallbacks(t *testing.T) {
	handle := &extensionCompactHandle{
		recordingCompactHandle: &recordingCompactHandle{},
		result:                 map[string]any{"summary": "short"},
		instructions:           make(chan string, 2),
	}
	m := &InteractiveMode{opts: InteractiveOptions{SessionHandle: handle}, abortCtx: t.Context()}

	completed := make(chan extension.CompactionResult, 1)
	m.compactForExtension(&extension.CompactOptions{CustomInstructions: "keep todos", OnComplete: func(result extension.CompactionResult) { completed <- result }})
	if got := <-handle.instructions; got != "keep todos" {
		t.Fatalf("custom instructions = %q", got)
	}
	if result := <-completed; result.(map[string]any)["summary"] != "short" {
		t.Fatalf("onComplete result = %v", result)
	}

	handle.err = errors.New("Nothing to compact (session too small)")
	failed := make(chan error, 1)
	m.compactForExtension(&extension.CompactOptions{OnError: func(err error) { failed <- err }})
	<-handle.instructions
	if err := <-failed; err == nil || err.Error() != "Nothing to compact (session too small)" {
		t.Fatalf("onError = %v", err)
	}

	unavailable := make(chan error, 1)
	(&InteractiveMode{abortCtx: t.Context()}).compactForExtension(&extension.CompactOptions{OnError: func(err error) { unavailable <- err }})
	if err := <-unavailable; err == nil || err.Error() != "compaction is not available" {
		t.Fatalf("onError without a session = %v", err)
	}
}
