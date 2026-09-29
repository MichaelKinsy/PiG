package ai

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Pi names a generated Gemini tool-call ID `${name}_${Date.now()}_${++toolCallCounter}` and generates one
// when the provided ID is missing or repeats an ID already in the output:
// .upstream/v0.87.1/packages/ai/src/api/google-generative-ai.ts:196-200 and google-vertex.ts:204-208.
func TestGoogleGeneratedToolCallIDShape(t *testing.T) {
	const wire = `data: {"candidates":[{"content":{"parts":[` +
		`{"functionCall":{"name":"read","args":{"path":"a"}}},` +
		`{"functionCall":{"id":"call-1","name":"read","args":{"path":"b"}}},` +
		`{"functionCall":{"id":"call-1","name":"read","args":{"path":"c"}}},` +
		`{"functionCall":{"id":"call-2","name":"read","args":{"path":"d"}}}` +
		`]},"finishReason":"STOP"}]}` + "\n\n"
	pattern := regexp.MustCompile(`^read_(\d+)_(\d+)$`)

	before := time.Now().UnixMilli()
	builder := newAssistantStreamBuilder(t.Context(), APIGoogleGenerativeAI, "google", "gemini-2.5-flash")
	provider := &googleProvider{}
	provider.parseGeminiSSE(t.Context(), strings.NewReader(wire), builder)
	message := builder.stream.Result()
	after := time.Now().UnixMilli()

	var ids []string
	for _, block := range message.Content {
		if call, ok := block.(ToolCall); ok {
			ids = append(ids, call.ID)
		}
	}
	if len(ids) != 4 {
		t.Fatalf("tool calls = %d (%#v)", len(ids), message.Content)
	}
	generated := func(id string) (int64, int64) {
		match := pattern.FindStringSubmatch(id)
		if match == nil {
			t.Fatalf("id %q does not match read_<ms>_<counter>", id)
		}
		ms, _ := strconv.ParseInt(match[1], 10, 64)
		counter, _ := strconv.ParseInt(match[2], 10, 64)
		if ms < before || ms > after {
			t.Fatalf("id %q timestamp outside [%d,%d]", id, before, after)
		}
		return ms, counter
	}
	_, first := generated(ids[0])
	if ids[1] != "call-1" || ids[3] != "call-2" {
		t.Fatalf("provided IDs not preserved: %q", ids)
	}
	_, second := generated(ids[2])
	if second != first+1 {
		t.Fatalf("counter %d then %d, want consecutive", first, second)
	}
}
