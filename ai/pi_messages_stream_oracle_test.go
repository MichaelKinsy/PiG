package ai

import (
	"encoding/json"
	"math/rand"
	"strings"
	"testing"
)

// piMessagesStreamBodies returns seeded pi-messages backend streams: serialized assistant-message events (text, thinking, tool-call blocks, done/error with usage), with some out-of-order, missing or malformed events.
func piMessagesStreamBodies() [][][]byte {
	random := rand.New(rand.NewSource(20261111))
	pick := func(options ...string) string { return options[random.Intn(len(options))] }
	var bodies [][][]byte
	for range 3000 {
		var body strings.Builder
		emit := func(event map[string]any) {
			encoded, _ := json.Marshal(event)
			prefix := pick("data: ", "data:", "event: x\ndata: ")
			body.WriteString(prefix + string(encoded) + pick("\n\n", "\n\n", "\r\n\r\n"))
		}
		usage := func() map[string]any {
			return map[string]any{"input": random.Intn(50), "output": random.Intn(50), "cacheRead": random.Intn(5), "cacheWrite": random.Intn(5), "totalTokens": random.Intn(100), "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}}
		}
		if random.Intn(10) != 0 {
			emit(map[string]any{"type": "start"})
		}
		for index := range random.Intn(4) {
			switch random.Intn(3) {
			case 0:
				emit(map[string]any{"type": "text_start", "contentIndex": index})
				var text string
				for range random.Intn(3) {
					d := pick("hi", " there", "é𝄞", "")
					text += d
					emit(map[string]any{"type": "text_delta", "contentIndex": index, "delta": d})
				}
				end := map[string]any{"type": "text_end", "contentIndex": index, "content": text}
				if random.Intn(2) == 0 {
					end["contentSignature"] = "sig"
				}
				emit(end)
			case 1:
				emit(map[string]any{"type": "thinking_start", "contentIndex": index})
				var text string
				for range random.Intn(3) {
					d := pick("hm", " so")
					text += d
					emit(map[string]any{"type": "thinking_delta", "contentIndex": index, "delta": d})
				}
				end := map[string]any{"type": "thinking_end", "contentIndex": index, "content": text}
				if random.Intn(2) == 0 {
					end["contentSignature"] = "sig"
					// Pi keeps a backend's redacted:false on the block; Pig's ThinkingContent.Redacted is a plain bool and omits it (not mirrored), so only true is sent.
					if random.Intn(2) == 0 {
						end["redacted"] = true
					}
				}
				emit(end)
			default:
				emit(map[string]any{"type": "toolcall_start", "contentIndex": index, "id": pick("call_1", "call_2"), "toolName": pick("read", "bash")})
				for _, part := range []string{`{"path"`, `:"a"}`, `x`}[:random.Intn(4)] {
					emit(map[string]any{"type": "toolcall_delta", "contentIndex": index, "delta": part})
				}
				emit(map[string]any{"type": "toolcall_end", "contentIndex": index, "toolCall": map[string]any{"type": "toolCall", "id": pick("call_1", "call_2"), "name": pick("read", "bash"), "arguments": map[string]any{"path": "a"}}})
			}
		}
		switch random.Intn(6) {
		case 0:
		case 1:
			emit(map[string]any{"type": "error", "reason": pick("error", "aborted"), "errorMessage": pick("boom", "é"), "usage": usage(), "responseId": "r1"})
		default:
			done := map[string]any{"type": "done", "reason": pick("stop", "length", "toolUse"), "usage": usage()}
			if random.Intn(2) == 0 {
				done["responseId"] = "r1"
			}
			emit(done)
		}
		if random.Intn(8) == 0 {
			body.WriteString("data: [DONE]\n\n")
		}
		bodies = append(bodies, splitSeededChunks(random, []byte(body.String())))
	}
	return bodies
}

// TestPiMessagesProviderStreamMatchesPi compares Pi's pi-messages provider with Pig's over seeded backend event streams.
func TestPiMessagesProviderStreamMatchesPi(t *testing.T) {
	providerStreamOracle(t, [4]string{"pi-messages", "piMessagesApi", "pi-messages", "pi"}, false, piMessagesStreamBodies(), func(baseURL string) Provider {
		return NewPiMessagesProvider(PiMessagesConfig{APIKey: "k", Model: "m", ProviderID: "pi", BaseURL: baseURL})
	})
}
