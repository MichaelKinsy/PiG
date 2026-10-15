package ai

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

// A backend event that is not an object has no `type` member: Pi's createEventConverter returns `{ ...event, partial }` for it, as for any unknown event,
// the stream carries it, and the agent loop's switch ignores it (pi-messages.ts:193-272, agent-loop.ts:415-445). The reply that follows still streams.
// Pig skips it as it skips an unknown type. Pi's provider stream for each truthy body is [start, (typeless), text_start, text_delta, text_end, done]
// with the text "hi"; readPiMessagesEvents drops a falsy event (pi-messages.ts:290-292).
func TestPiMessagesNonObjectEventIsSkippedLikeAnUnknownEvent(t *testing.T) {
	text := `data: {"type":"text_start","contentIndex":0}` + "\n\n" + `data: {"type":"text_delta","contentIndex":0,"delta":"hi"}` + "\n\n" + `data: {"type":"text_end","contentIndex":0,"content":"hi"}` + "\n\n"
	done := `data: {"type":"done","reason":"stop","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}}` + "\n\n"
	for _, odd := range []string{"5", `"x"`, "[1]", "true", "{}", `{"type":"ping"}`, "null", "0", "false", `""`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("content-type", "text/event-stream")
			_, _ = fmt.Fprint(w, `data: {"type":"start"}`+"\n\ndata: "+odd+"\n\n"+text+done)
		}))
		events, message := collectPiMessages(t, newTestPiMessagesProvider(server.URL, nil), StreamOptions{})
		server.Close()
		var types []string
		for _, event := range events {
			types = append(types, string(event.EventType()))
		}
		if !slices.Equal(types, []string{"start", "text_start", "text_delta", "text_end", "done"}) || message.StopReason != StopReasonStop || len(message.Content) != 1 {
			t.Errorf("%s: events %v, stop %q, error %q, want the reply", odd, types, message.StopReason, message.ErrorMessage)
		}
	}
}
