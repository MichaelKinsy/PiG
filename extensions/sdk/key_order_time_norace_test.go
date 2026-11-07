//go:build !race

package sdk

import (
	"testing"
	"time"
)

// Setting the content of every message in place takes time linear in the messages: a search for the element each object had moved to took 1.2 s at 3000 messages
// and 10 s at 10000. The race detector slows the decode and the reply by an order of magnitude, so the bound is checked in non-race builds.
func TestRedactingEveryMessageTakesLinearTime(t *testing.T) {
	const count, limit = 10000, 2 * time.Second
	ext := New("key-order-time")
	ext.OnEvent(EventContext, func(_ Context, data map[string]any) (any, error) {
		for _, message := range data["messages"].([]any) {
			message.(map[string]any)["content"] = "[redacted]"
		}
		return nil, nil
	})
	dispatch := keyOrderFixture(t, ext)
	args, want := redactAllArgs(count)
	start := time.Now()
	got := dispatch(1, EventContext, args)
	if elapsed := time.Since(start); elapsed > limit {
		t.Errorf("redacting %d messages in place took %v, want under %v", count, elapsed, limit)
	}
	if got != want {
		t.Errorf("redacting every message changed the order of their members (reply of %d bytes)", len(got))
	}
}
