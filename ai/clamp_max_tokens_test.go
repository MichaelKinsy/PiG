package ai

import "testing"

// Ports packages/ai/test/clamp-max-tokens.test.ts.

const clampWindow = 1_048_576

func clampModel(window int) *Model {
	return &Model{Capabilities: ModelCapabilities{ContextWindow: window}}
}

// The last assistant usage anchors the estimate, so the estimated input is exactly inputTokens.
func clampContextWithInput(inputTokens int) TranscriptContext {
	return NormalizeContext(Context{Messages: []Message{
		UserMessage{Content: UserText("hi"), Timestamp: 1},
		estimateAssistant(2, inputTokens),
	}})
}

func TestClampMaxTokensToContextOpenRouterOvercountMargin(t *testing.T) {
	for _, tc := range []struct {
		name      string
		window    int
		maxTokens int
		input     int
		want      int
	}{
		// The failing request from #10497: OpenRouter counted 229,747 input tokens where pi estimated 220,815.
		{"leaves 15% of the input unused so an OpenRouter overcount still fits (#10497)", clampWindow, 943_718, 220_815, clampWindow - 220_815 - 33_123},
		{"leaves at least 4096 tokens unused for small inputs", clampWindow, clampWindow, 10_000, clampWindow - 10_000 - 4096},
		{"keeps the model output limit when it already fits", clampWindow, 64_000, 100_000, 64_000},
		{"sends up to 32768 tokens when the proportional margin leaves no room", clampWindow, 943_718, 912_000, 32_768},
		{"uses the real room minus 4096 when less than 32768 tokens fit", clampWindow, 943_718, 1_030_000, clampWindow - 1_030_000 - 4096},
		{"keeps only the fixed 4096 margin for small windows", 32_768, 32_768, 20_000, 32_768 - 20_000 - 4096},
		{"sends 1 token when the input already exceeds the window", clampWindow, 943_718, 1_050_000, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ClampMaxTokensToContext(clampModel(tc.window), clampContextWithInput(tc.input), tc.maxTokens)
			if got != tc.want {
				t.Fatalf("ClampMaxTokensToContext = %d, want %d", got, tc.want)
			}
		})
	}
	// #10497: the overcounted request must fit the window.
	if got := ClampMaxTokensToContext(clampModel(clampWindow), clampContextWithInput(220_815), 943_718); 229_747+got > clampWindow {
		t.Fatalf("overcounted input 229747 + max tokens %d exceeds the window", got)
	}
}
