package ai_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

// The two helpers at .upstream/v0.99.1/packages/ai/test/image-tool-result.test.ts:30-203 are exercised for every inventoried model. Real request converters must retain the image/text before the faux generation side will describe the known fixture. Stochastic visual recognition is live-only, including upstream's four skipped Xiaomi mixed-content cases.
func TestImageToolResultMatrixUpstream(t *testing.T) {
	asset, err := os.ReadFile("testdata/upstream-red-circle.png")
	if err != nil {
		t.Fatal(err)
	}
	image := base64.StdEncoding.EncodeToString(asset)
	cases := upstreamCaseSites(t, "packages/ai/test/image-tool-result.test.ts")
	specs := []struct{ provider, model string }{
		{"google", "gemini-2.5-flash"}, {"openai", "gpt-4o-mini"}, {"openai", "gpt-5-mini"}, {"azure-openai-responses", "gpt-4o-mini"},
		{"anthropic", "claude-haiku-4-5"}, {"openrouter", "z-ai/glm-4.5v"}, {"mistral", "pixtral-12b"}, {"together", "moonshotai/Kimi-K3"},
		{"baseten", "moonshotai/Kimi-K2.6"}, {"xiaomi", "mimo-v2.5-pro"}, {"xiaomi-token-plan-cn", "mimo-v2.5-pro"},
		{"xiaomi-token-plan-ams", "mimo-v2.5-pro"}, {"xiaomi-token-plan-sgp", "mimo-v2.5-pro"}, {"qwen-token-plan", "qwen3.7-max"},
		{"qwen-token-plan-individual", "qwen3.8-max"}, {"qwen-token-plan-cn", "qwen3.7-max"}, {"kimi-coding", "kimi-for-coding"},
		{"vercel-ai-gateway", "google/gemini-2.5-flash"}, {"amazon-bedrock", "global.anthropic.claude-sonnet-4-5-20250929-v1:0"},
		{"anthropic", "claude-sonnet-4-5"}, {"github-copilot", "claude-haiku-4.5"}, {"github-copilot", "claude-sonnet-4.6"}, {"openai-codex", "gpt-5.5"},
	}
	if len(cases) != 2*len(specs) {
		t.Fatalf("unported matrix change: %d cases for %d models", len(cases), len(specs))
	}
	for index, tc := range cases {
		t.Run(tc.ID, func(t *testing.T) {
			t.Logf(".upstream/current/packages/ai/test/image-tool-result.test.ts:%d", tc.Line)
			spec := specs[index/2]
			metadata, ok := ai.LookupModelExact(spec.provider + "/" + spec.model)
			if !ok {
				t.Fatalf("missing upstream model %s/%s", spec.provider, spec.model)
			}
			model := *metadata
			if index/2 == 1 {
				model.API = ai.APIOpenAICompletions
			}
			if !slices.Contains(model.Capabilities, "image") {
				t.Log("upstream helper returns for a non-vision model")
				return
			}
			mixed := strings.Contains(tc.ID, "text and image")
			name, description, prompt := "get_circle", "Returns a circle image for visualization", "Call the get_circle tool to get an image, and describe what you see, shapes, colors, etc."
			if mixed {
				name = "get_circle_with_description"
				description = "Returns a circle image with a text description"
				prompt = "Use the get_circle_with_description tool and tell me what you learned. Also say what color the shape is."
			}
			request := ai.Context{SystemPrompt: "You are a helpful assistant that uses tools when asked.", Messages: []ai.Message{ai.UserMessage{Content: ai.UserText(prompt)}}, Tools: []ai.ToolSchema{{Name: name, Description: description, Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}}}
			actual := matrixProvider(t, &model, "https://example.invalid")
			firstPayload := captureImageToolPayload(t, actual, request)
			if !strings.Contains(strings.ToLower(firstPayload), `"name":"`+name+`"`) {
				t.Fatal("tool declaration missing from provider request")
			}
			services, err := coding.NewServices(coding.ServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			faux := ai.NewFauxProvider(ai.FauxConfig{ProviderID: spec.provider, Model: spec.model})
			faux.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall(name, map[string]any{}, "image_call")}, StopReason: "toolUse"})})
			first := services.ModelRuntime().Complete(t.Context(), &ai.Model{ID: spec.model, Provider: faux}, request, ai.StreamOptions{})
			if first.StopReason != ai.StopReasonToolUse {
				t.Fatal(first.StopReason)
			}
			var call ai.ToolCall
			found := false
			for _, block := range first.Content {
				if value, ok := block.(ai.ToolCall); ok {
					call = value
					found = true
					break
				}
			}
			if !found || call.Name != name {
				t.Fatalf("tool call=%#v", first.Content)
			}
			// The fake generation transport stands in for this model's native API identity on replay.
			first.API = model.API
			request.Messages = append(request.Messages, *first)
			var content []ai.ToolResultMessageContent
			if mixed {
				content = append(content, ai.TextContent{Text: "This is a geometric shape with specific properties: it has a diameter of 100 pixels."})
			}
			content = append(content, ai.ImageContent{Data: image, MimeType: "image/png"})
			request.Messages = append(request.Messages, ai.ToolResultMessage{ToolCallID: call.ID, ToolName: call.Name, Content: content})
			secondPayload := captureImageToolPayload(t, actual, request)
			imagePresent := strings.Contains(secondPayload, image)
			textPresent := !mixed || strings.Contains(secondPayload, "diameter of 100 pixels")
			if !imagePresent || !textPresent {
				t.Logf("image=%v text=%v payload=%s", imagePresent, textPresent, secondPayload)
			}
			faux.SetResponses([]ai.FauxResponseStep{ai.FauxFactoryStep(func(ai.TranscriptContext, ai.StreamOptions, *ai.FauxProviderState, *ai.Model) (ai.FauxResponse, error) {
				if !imagePresent || !textPresent {
					return ai.FauxResponse{StopReason: "error", ErrorMessage: "provider request lost image or text"}, nil
				}
				return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("A red circle with a diameter of 100 pixels.")}, StopReason: "stop"}, nil
			})})
			second := services.ModelRuntime().Complete(t.Context(), &ai.Model{ID: spec.model, Provider: faux}, request, ai.StreamOptions{})
			if second.StopReason != ai.StopReasonStop || second.ErrorMessage != "" {
				t.Fatalf("second=%#v", second)
			}
			text := ""
			for _, block := range second.Content {
				if value, ok := block.(ai.TextContent); ok {
					text = value.Text
					break
				}
			}
			lower := strings.ToLower(text)
			if !strings.Contains(lower, "red") || !strings.Contains(lower, "circle") {
				t.Fatalf("missing visual description: %q", text)
			}
			if mixed && !regexp.MustCompile("diameter|100|pixel").MatchString(lower) {
				t.Fatalf("missing text details: %q", text)
			}
		})
	}
}

func captureImageToolPayload(t *testing.T, provider ai.Provider, request ai.Context) string {
	t.Helper()
	captured := ""
	sentinel := errors.New("payload captured")
	stream, err := provider.Stream(t.Context(), ai.NormalizeContext(request), ai.StreamOptions{Env: ai.ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1"}, Transport: ai.TransportSSE, OnPayload: func(payload any, _ *ai.Model) (any, error) {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		captured = string(raw)
		return nil, sentinel
	}})
	if err == nil && stream != nil {
		_ = stream.Result()
	} else if !errors.Is(err, sentinel) {
		t.Fatalf("payload capture failed: %v", err)
	}
	if captured == "" {
		t.Fatal("provider did not construct payload")
	}
	return captured
}
