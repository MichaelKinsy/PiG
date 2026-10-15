package ai

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

const googleOracleBodies = 3000

// googleStreamBodies returns seeded Gemini streamGenerateContent bodies: chunks of text, thought and function-call parts, finish reasons, usage metadata, odd delimiters, split at arbitrary bytes.
func googleStreamBodies() [][][]byte {
	random := rand.New(rand.NewSource(20261104))
	pick := func(options ...string) string { return options[random.Intn(len(options))] }
	var bodies [][][]byte
	for range googleOracleBodies {
		var body strings.Builder
		count := 1 + random.Intn(5)
		for position := range count {
			chunk := map[string]any{}
			if random.Intn(3) != 0 {
				var parts []any
				for range random.Intn(4) {
					part := map[string]any{}
					switch random.Intn(6) {
					case 0, 1:
						part["text"] = pick("hello", " world", "é𝄞", "", "a\nb", "{\"x\":1}")
					case 2:
						part["text"] = pick("think", "pondering ", "")
						part["thought"] = true
						if random.Intn(2) == 0 {
							part["thoughtSignature"] = pick("c2ln", "YWJj", "")
						}
					case 3:
						call := map[string]any{"name": pick("read", "bash", "write"), "id": fmt.Sprintf("call_%d", random.Intn(3))}
						if random.Intn(4) != 0 {
							call["args"] = pick2(random, map[string]any{"path": "a.txt"}, map[string]any{}, map[string]any{"n": 1, "nested": map[string]any{"k": []any{1, "x"}}})
						}
						part["functionCall"] = call
						if random.Intn(3) == 0 {
							part["thoughtSignature"] = "c2ln"
						}
					case 4:
						part["text"] = pick("tail", "x")
						part["thoughtSignature"] = "c2ln"
					default:
						part["text"] = "plain"
					}
					parts = append(parts, part)
				}
				candidate := map[string]any{"content": map[string]any{"role": "model", "parts": parts}}
				if random.Intn(3) == 0 {
					candidate["finishReason"] = pick("STOP", "MAX_TOKENS", "SAFETY", "MALFORMED_FUNCTION_CALL", "OTHER", "RECITATION", "FINISH_REASON_UNSPECIFIED")
				}
				chunk["candidates"] = []any{candidate}
			}
			if position == count-1 && random.Intn(2) == 0 {
				// A well-formed stream ends with a candidate that carries a finish reason.
				if candidates, ok := chunk["candidates"].([]any); ok {
					candidates[0].(map[string]any)["finishReason"] = "STOP"
				} else {
					chunk["candidates"] = []any{map[string]any{"finishReason": "STOP"}}
				}
			}
			if random.Intn(3) == 0 {
				usage := map[string]any{"promptTokenCount": random.Intn(50), "candidatesTokenCount": random.Intn(50), "totalTokenCount": random.Intn(120)}
				if random.Intn(2) == 0 {
					usage["thoughtsTokenCount"] = random.Intn(20)
					usage["cachedContentTokenCount"] = random.Intn(10)
				}
				chunk["usageMetadata"] = usage
			}
			if random.Intn(4) == 0 {
				chunk["responseId"] = pick("r1", "r2")
			}
			encoded, _ := json.Marshal(chunk)
			body.WriteString(pick("data: ", "data:") + string(encoded) + pick("\n\n", "\n\n", "\n\n", "\r\n\r\n", "\r\r"))
		}
		raw := []byte(body.String())
		var chunks [][]byte
		for len(raw) > 0 {
			size := 1 + random.Intn(max(len(raw), 1))
			if random.Intn(3) == 0 {
				size = 1 + random.Intn(6)
			}
			size = min(size, len(raw))
			chunks = append(chunks, raw[:size])
			raw = raw[size:]
		}
		bodies = append(bodies, chunks)
	}
	return bodies
}

func pick2(random *rand.Rand, options ...map[string]any) map[string]any {
	return options[random.Intn(len(options))]
}

// TestGoogleProviderStreamMatchesPi compares Pi's google-generative-ai provider with Pig's over seeded Gemini bodies.
func TestGoogleProviderStreamMatchesPi(t *testing.T) {
	providerStreamOracle(t, [4]string{"google-generative-ai", "googleGenerativeAIApi", "google-generative-ai", "google"}, false, googleStreamBodies(), func(baseURL string) Provider {
		return NewGoogleProvider(GoogleConfig{APIKey: "k", Model: "m", BaseURL: baseURL})
	})
}
