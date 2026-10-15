package ai_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// pi: packages/ai/src/api/google-generative-ai.ts

// Scripted Gemini streamGenerateContent streams (text and thought parts with signatures, function calls with and without ids, finish reasons, usage
// metadata with cached and thought tokens, response ids, truncated streams) answered by a local server to the pinned pi-ai (its adapter rejects a custom
// fetch) and to PiG; the stream events and the final message must be equal.
func TestGoogleResponseMatchesPi(t *testing.T) {
	checkGoogleResponse(t, "google-generative-ai.js", "google-generative-ai", ai.APIGoogleGenerativeAI, ai.StreamGoogle)
}

// TestGoogleVertexResponseMatchesPi is the same comparison for the google-vertex API module.
func TestGoogleVertexResponseMatchesPi(t *testing.T) {
	checkGoogleResponse(t, "google-vertex.js", "google-vertex", ai.APIGoogleVertex, ai.StreamGoogleVertex)
}

func checkGoogleResponse(t *testing.T, module, apiName string, api ai.API, run func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error)) {
	t.Helper()
	rng := rand.New(rand.NewSource(181))
	quote := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	texts := []string{"", "a", "hello ", "wörld", "😀", "line\n", "<&>", " "}
	var probes []responseProbe
	for range 400 {
		var sb strings.Builder
		event := func(candidate string, extra string) {
			fmt.Fprintf(&sb, "data: {%s%s}\n\n", candidate, extra)
		}
		for range 1 + rng.Intn(8) {
			var parts []string
			for range 1 + rng.Intn(2) {
				switch rng.Intn(8) {
				case 0, 1, 2:
					parts = append(parts, fmt.Sprintf(`{"text":%s}`, quote(pick(texts...))))
				case 3:
					parts = append(parts, fmt.Sprintf(`{"text":%s,"thought":true}`, quote(pick(texts...))))
				case 4:
					parts = append(parts, fmt.Sprintf(`{"text":%s,"thought":true,"thoughtSignature":%s}`, quote(pick(texts...)), quote(pick("c2ln", "YWJj", "not base64!", ""))))
				case 5:
					parts = append(parts, fmt.Sprintf(`{"text":%s,"thoughtSignature":"c2ln"}`, quote(pick(texts...))))
				case 6:
					id := ""
					if rng.Intn(2) == 0 {
						id = fmt.Sprintf(`,"id":"call_%d"`, rng.Intn(5))
					}
					parts = append(parts, fmt.Sprintf(`{"functionCall":{"name":%s,"args":%s%s}%s}`, quote(pick("read", "bash")), pick(`{"path":"a"}`, `{}`, `{"n":1,"s":"x"}`), id, pick("", `,"thoughtSignature":"c2ln"`)))
				default:
					parts = append(parts, `{}`)
				}
			}
			content := `"candidates":[{"content":{"role":"model","parts":[` + strings.Join(parts, ",") + `]}`
			if rng.Intn(5) == 0 {
				content += `,"finishReason":` + quote(pick("STOP", "MAX_TOKENS", "SAFETY", "RECITATION", "OTHER", "MALFORMED_FUNCTION_CALL", "UNEXPECTED_TOOL_CALL", "FINISH_REASON_UNSPECIFIED", "BLOCKLIST", "weird"))
			}
			content += `}]`
			extra := ""
			if rng.Intn(3) == 0 {
				extra += fmt.Sprintf(`,"usageMetadata":{"promptTokenCount":%d,"candidatesTokenCount":%d,"totalTokenCount":%d%s%s}`, 5+rng.Intn(100), rng.Intn(50), rng.Intn(200), pick("", fmt.Sprintf(`,"cachedContentTokenCount":%d`, rng.Intn(5))), pick("", fmt.Sprintf(`,"thoughtsTokenCount":%d`, rng.Intn(20))))
			}
			if rng.Intn(3) == 0 {
				extra += fmt.Sprintf(`,"responseId":"resp-%d"`, rng.Intn(10))
			}
			if rng.Intn(6) == 0 {
				extra += `,"modelVersion":"m-001"`
			}
			event(content, extra)
		}
		probes = append(probes, responseProbe{Provider: pick("google", "custom"), Reasoning: rng.Intn(2) == 0, SSE: sb.String()})
	}
	checkResponses(t, module, apiName, api, run, probes)
}
