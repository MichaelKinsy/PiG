package coding

import (
	"runtime"
	"testing"
	"unsafe"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// A session request that cache warming skips must not copy the session's messages. The copy is what the warmer compares with the
// transcript later to learn whether the request is still current; for a skipped request nothing reads it, and its size grows with
// the conversation, once per model request.
func TestSkippedCacheWarmingRequestCopiesNoMessages(t *testing.T) {
	sess, provider := newWarmingSession(t, "off", SessionOptions{})
	messages := make([]agent.AgentMessage, 4000)
	sess.agent.SetMessages(messages)
	warmer, sessionID := sess.cacheWarmer()
	if warmer == nil {
		t.Fatal("the session has no cache warmer: the request would return before warming is asked")
	}
	model, options := warmingModel(provider), ai.StreamOptions{SessionID: sessionID}
	sess.startCacheWarming(model, ai.TranscriptContext{}, options)
	if reason := sess.CacheWarmingStatus().Reason; reason != "cache warming disabled" {
		t.Fatalf("status reason = %q, want the request skipped because warming is off", reason)
	}

	copyBytes := uint64(len(messages)) * uint64(unsafe.Sizeof(agent.AgentMessage{}))
	const runs = 40
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range runs {
		sess.startCacheWarming(model, ai.TranscriptContext{}, options)
	}
	runtime.ReadMemStats(&after)
	if perRequest := (after.TotalAlloc - before.TotalAlloc) / runs; perRequest >= copyBytes/2 {
		t.Fatalf("a skipped request allocated %d bytes; a copy of the %d messages is %d bytes", perRequest, len(messages), copyBytes)
	}
}
