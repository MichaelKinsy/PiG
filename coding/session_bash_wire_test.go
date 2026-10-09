package coding

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

type cancelledWireOperations struct{}

func (cancelledWireOperations) Exec(ctx context.Context, _, _ string, options extension.BashOperationsExecOptions) (extension.BashOperationsResult, error) {
	options.OnData([]byte("BASH_READY\n"))
	<-ctx.Done()
	return extension.BashOperationsResult{}, ctx.Err()
}

func TestDeferredBashRetainsCompletionState(t *testing.T) {
	sess := newBashTestSession(t, `{}`)
	code := 7
	before := time.Now().UnixMilli()
	sess.mu.Lock()
	recordErr := sess.RecordBashResult("deferred", BashResult{Output: "done", ExitCode: &code}, false)
	code = 8 // Pi copies the numeric status, not the caller's mutable storage.
	if len(sess.pendingBashMessages) != 1 {
		sess.mu.Unlock()
		t.Fatal("missing deferred record")
	}
	captured := sess.pendingBashMessages[0].timestamp
	if captured < before || captured > time.Now().UnixMilli() {
		sess.mu.Unlock()
		t.Fatalf("completion timestamp = %d", captured)
	}
	// A known older completion time distinguishes preserving the record from stamping at flush.
	sess.pendingBashMessages[0].timestamp = 1700000000123
	flushErr := sess.flushPendingBashLocked()
	sess.mu.Unlock()
	if recordErr != nil || flushErr != nil {
		t.Fatalf("record=%v flush=%v", recordErr, flushErr)
	}
	message := sess.agent.Messages()[0].Custom
	if message["timestamp"] != float64(1700000000123) || message["exitCode"] != float64(7) {
		t.Fatalf("deferred state changed: %#v", message)
	}
}

// Pi 0.87.1 bash-executor.ts:143-146 omits exitCode on cancellation.
// agent-session.ts:3498-3508 records a numeric message timestamp before any deferral.
func TestCancelledBashWireAndPersistence(t *testing.T) {
	sess := newBashTestSession(t, `{}`)
	before := time.Now().UnixMilli()
	result, err := sess.ExecuteBash(t.Context(), "held", func(string) { sess.AbortBash() }, &ExecuteBashOptions{Operations: cancelledWireOperations{}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if _, exists := wire["exitCode"]; exists {
		t.Errorf("cancelled bash has exitCode: %s", data)
	}
	if wire["output"] != "BASH_READY\n" || wire["cancelled"] != true || wire["truncated"] != false {
		t.Errorf("result=%s", data)
	}
	messages := sess.agent.Messages()
	if len(messages) == 0 {
		t.Fatal("missing bash message")
	}
	message := messages[len(messages)-1].Custom
	if _, exists := message["exitCode"]; exists {
		t.Errorf("persisted cancelled exitCode: %#v", message)
	}
	timestamp, ok := message["timestamp"].(float64)
	if !ok || timestamp < float64(before) || timestamp > float64(time.Now().UnixMilli()) {
		t.Errorf("invalid timestamp: %#v", message)
	}
}
