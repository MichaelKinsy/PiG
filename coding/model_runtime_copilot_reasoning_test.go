package coding

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

func copilotRuntimeModel(t *testing.T, services *AgentSessionServices, api ai.API, id string) *ai.Model {
	t.Helper()
	access := "tid=test;exp=9999999999;proxy-ep=proxy.individual.githubcopilot.com;"
	if err := services.Auth().Set("github-copilot", ai.Credential{Type: ai.CredentialOAuth, Refresh: "github-access-token", Access: access, Expires: time.Now().Add(time.Hour).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range services.ModelRuntime().GetModels() {
		if candidate.ProviderMeta.ProviderID == "github-copilot" && candidate.ProviderMeta.API == api && (id == "" || candidate.ID == id) {
			return candidate
		}
	}
	t.Fatalf("catalog has no github-copilot %s model %q", api, id)
	return nil
}

// Pi 86918f9eeb (#12110) through the production ModelRuntime request path: a GitHub Copilot chat-completions model sends the
// selected level as reasoning_effort, and the reasoning count Copilot reports at the top level of usage reaches the message
// (packages/ai/test/openai-completions-tool-choice.test.ts:1771, :1809).
func TestModelRuntimeCopilotCompletionsReasoning(t *testing.T) {
	services := newRuntimeTestServices(t)
	model := copilotRuntimeModel(t, services, ai.APIOpenAICompletions, "gemini-4-argon")
	const sse = "data: {\"id\":\"chatcmpl-copilot-reasoning\",\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"chatcmpl-copilot-reasoning\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":40,\"prompt_tokens_details\":{\"cached_tokens\":0},\"reasoning_tokens\":30}}\n\n" +
		"data: [DONE]\n\n"
	for _, level := range []ai.ThinkingLevel{ai.ThinkingLevelLow, ai.ThinkingLevelHigh} {
		t.Run(string(level), func(t *testing.T) {
			var body string
			fetch := &http.Client{Transport: cloudflareCompatTransport(func(request *http.Request) (*http.Response, error) {
				data, _ := io.ReadAll(request.Body)
				body = string(data)
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(sse))}, nil
			})}
			message := services.ModelRuntime().CompleteSimple(t.Context(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Reply with exactly OK")}}}, ai.StreamOptions{Fetch: fetch, Thinking: level})
			if want := `"reasoning_effort":"` + string(level) + `"`; !strings.Contains(body, want) {
				t.Fatalf("request body has no %s:\n%s", want, body)
			}
			if message == nil || message.StopReason != ai.StopReasonStop {
				t.Fatalf("message=%+v", message)
			}
			if message.Usage.Reasoning == nil || *message.Usage.Reasoning != 30 || message.Usage.Output != 40 || message.Usage.Input != 10 || message.Usage.TotalTokens != 50 {
				t.Fatalf("usage=%+v, want reasoning 30, output 40, input 10, totalTokens 50", message.Usage)
			}
		})
	}
}

// The commit changes Pi's chat-completions adapter and Copilot's chat-completions records only. A Copilot Responses model's request
// has no reasoning_effort member and keeps its reasoning object, and its usage reads output_tokens_details.reasoning_tokens,
// never a top-level reasoning_tokens.
func TestModelRuntimeCopilotResponsesIgnoreCompletionsReasoningFallback(t *testing.T) {
	services := newRuntimeTestServices(t)
	model := copilotRuntimeModel(t, services, ai.APIOpenAIResponses, "")
	const sse = "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"status\":\"completed\",\"usage\":{\"input_tokens\":10,\"output_tokens\":40,\"total_tokens\":50,\"output_tokens_details\":{\"reasoning_tokens\":5},\"reasoning_tokens\":30}}}\n\n"
	var bodies []string
	for range 2 {
		fetch := &http.Client{Transport: cloudflareCompatTransport(func(request *http.Request) (*http.Response, error) {
			data, _ := io.ReadAll(request.Body)
			bodies = append(bodies, string(data))
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(sse))}, nil
		})}
		message := services.ModelRuntime().CompleteSimple(t.Context(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Reply with exactly OK")}}}, ai.StreamOptions{Fetch: fetch, Thinking: ai.ThinkingLevelHigh, SessionID: "copilot-responses-unchanged"})
		if message == nil || message.StopReason != ai.StopReasonStop {
			t.Fatalf("%s message=%+v", model.ID, message)
		}
		if message.Usage.Reasoning == nil || *message.Usage.Reasoning != 5 || message.Usage.Output != 40 || message.Usage.TotalTokens != 50 {
			t.Fatalf("%s usage=%+v, want reasoning 5 (output_tokens_details), output 40, totalTokens 50", model.ID, message.Usage)
		}
	}
	if len(bodies) != 2 || bodies[0] != bodies[1] {
		t.Fatalf("%s: two equal requests differ:\n%s\n%s", model.ID, bodies[0], bodies[1])
	}
	if strings.Contains(bodies[0], "reasoning_effort") {
		t.Fatalf("%s: a Responses request carries reasoning_effort:\n%s", model.ID, bodies[0])
	}
	var request struct {
		Model           string         `json:"model"`
		MaxOutputTokens int            `json:"max_output_tokens"`
		Reasoning       map[string]any `json:"reasoning"`
	}
	if err := json.Unmarshal([]byte(bodies[0]), &request); err != nil {
		t.Fatalf("%s: the request is not JSON: %v\n%s", model.ID, err, bodies[0])
	}
	if request.Model != model.ID || request.MaxOutputTokens != model.Capabilities.MaxOutputTokens || request.Reasoning["effort"] != "high" || request.Reasoning["summary"] != "auto" {
		t.Fatalf("%s: the Responses request keeps the model, its output limit %d and the reasoning object {effort high, summary auto}; got %s", model.ID, model.Capabilities.MaxOutputTokens, bodies[0])
	}
}
