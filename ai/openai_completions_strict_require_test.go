package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Oracle: Pi 1.5.0 openai-completions.js stream() over a system message that adds this tool
// returns stopReason "error" with this message before any request is sent for every
// provider/compat row below whose supportsStrictMode is not true; prefer rows send a plain
// function tool without a strict field. Rows were produced by running Pi, not by hand.
const strictRequireUnsupportedError = `Tool "lookup" requires JSON-schema constrained sampling, but strict tools are unsupported.`

// strictSamplingTool decodes the tool from JSON so it records key order as a decoded tool does;
// Pi sends the parameters in the order type, properties, required.
func strictSamplingTool(strict string) ToolSchema {
	var tool ToolSchema
	raw := `{"name":"lookup","description":"d","parameters":{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]},"constrainedSampling":{"type":"json_schema","strict":"` + strict + `"}}`
	if err := json.Unmarshal([]byte(raw), &tool); err != nil {
		panic(err)
	}
	return tool
}

func strictRequireRows() map[string]OpenAIConfig {
	on, off := true, false
	return map[string]OpenAIConfig{
		"default compat":             {ProviderID: "openai", BaseURL: "https://api.openai.com/v1", Model: "m"},
		"oauth copilot, no compat":   {ProviderID: "github-copilot", BaseURL: "https://api.individual.githubcopilot.com", Model: "m"},
		"api key, strict off":        {ProviderID: "openai", BaseURL: "https://api.openai.com/v1", Model: "m", Compat: &OpenAICompat{SupportsStrictMode: &off}},
		"custom base url":            {ProviderID: "custom", BaseURL: "http://localhost:1/v1", Model: "m"},
		"custom base url strict off": {ProviderID: "custom", BaseURL: "http://localhost:1/v1", Model: "m", Compat: &OpenAICompat{SupportsStrictMode: &off}},
		"strict on":                  {ProviderID: "custom", BaseURL: "http://localhost:1/v1", Model: "m", Compat: &OpenAICompat{SupportsStrictMode: &on}},
	}
}

func TestCompletionsConvertToolsStrictRequireUnsupported(t *testing.T) {
	for name, cfg := range strictRequireRows() {
		t.Run(name, func(t *testing.T) {
			p := &openAIProvider{cfg: cfg}
			unsupported := cfg.Compat == nil || cfg.Compat.SupportsStrictMode == nil || !*cfg.Compat.SupportsStrictMode
			out, err := p.convertTools([]ToolSchema{strictSamplingTool("require")})
			if unsupported {
				if err == nil || err.Error() != strictRequireUnsupportedError {
					t.Fatalf("err = %v, want %q", err, strictRequireUnsupportedError)
				}
				return
			}
			if err != nil || out[0].Function.Strict == nil || !*out[0].Function.Strict {
				t.Fatalf("out = %+v err = %v, want strict:true", out, err)
			}
			// With strict mode on, prefer sends strict:true like require.
			out, err = p.convertTools([]ToolSchema{strictSamplingTool("prefer")})
			if err != nil || out[0].Function.Strict == nil || !*out[0].Function.Strict {
				t.Fatalf("prefer out = %+v err = %v", out, err)
			}
		})
	}
}

func TestCompletionsConvertToolsStrictPreferUnsupportedPlain(t *testing.T) {
	for name, cfg := range strictRequireRows() {
		if cfg.Compat != nil && cfg.Compat.SupportsStrictMode != nil && *cfg.Compat.SupportsStrictMode {
			continue
		}
		t.Run(name, func(t *testing.T) {
			out, err := (&openAIProvider{cfg: cfg}).convertTools([]ToolSchema{strictSamplingTool("prefer")})
			if err != nil {
				t.Fatal(err)
			}
			got, _ := json.Marshal(out[0])
			const want = `{"type":"function","function":{"name":"lookup","description":"d","parameters":{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]}}}`
			if string(got) != want {
				t.Fatalf("got %s\nwant %s", got, want)
			}
		})
	}
}

// streamStrictRequireError streams the transcript through an openAIProvider on cfg and returns the surfaced
// error message and whether any request reached the transport.
func streamStrictRequireError(t *testing.T, cfg OpenAIConfig, messages []Message) (string, bool) {
	t.Helper()
	sent := false
	p := &openAIProvider{cfg: cfg}
	p.client = &http.Client{Transport: openAITestRoundTripperFunc(func(*http.Request) (*http.Response, error) {
		sent = true
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: [DONE]\n\n"))}, nil
	})}
	var msg string
	stream, err := p.Stream(context.Background(), NormalizeContext(Context{Messages: messages}), StreamOptions{})
	if err != nil {
		msg = err.Error()
	} else {
		for ev := range stream.Events(context.Background()) {
			if e, ok := ev.(ErrorEvent); ok {
				msg = e.Error.ErrorMessage
			}
		}
	}
	return msg, sent
}

func TestCompletionsStreamStrictRequireUnsupportedFailsBeforeRequest(t *testing.T) {
	for name, cfg := range strictRequireRows() {
		if cfg.Compat != nil && cfg.Compat.SupportsStrictMode != nil && *cfg.Compat.SupportsStrictMode {
			continue
		}
		t.Run(name, func(t *testing.T) {
			msg, sent := streamStrictRequireError(t, cfg, []Message{
				SystemMessage{Content: SystemText("s"), ToolsAdded: []ToolSchema{strictSamplingTool("require")}},
				UserMessage{Content: UserText("hi")},
			})
			if msg != strictRequireUnsupportedError {
				t.Fatalf("error = %q, want %q", msg, strictRequireUnsupportedError)
			}
			if sent {
				t.Fatal("request was sent")
			}
		})
	}
}

// TestCompletionsStreamStrictRequireMidConversationToolAdditions covers a compat with mid-conversation
// system messages and tool additions but no strict mode (kimi-coding k3 in the catalog). The tool arrives
// where Pi converts it through the per-system-message path rather than the request tool list.
func TestCompletionsStreamStrictRequireMidConversationToolAdditions(t *testing.T) {
	on, off := true, false
	cfg := OpenAIConfig{ProviderID: "custom", BaseURL: "http://localhost:1/v1", Model: "m", Compat: &OpenAICompat{SupportsMidConvoSystemMessages: &on, SupportsMidConvoToolAdditions: &on, SupportsStrictMode: &off}}
	rows := map[string][]Message{
		"later system message": {
			SystemMessage{Content: SystemText("s")},
			UserMessage{Content: UserText("hi")},
			AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: "ok"}}, StopReason: StopReasonStop},
			SystemMessage{ToolsAdded: []ToolSchema{strictSamplingTool("require")}},
			UserMessage{Content: UserText("again")},
		},
		// An aborted first assistant is dropped by the transform, which leaves the tool-bearing system message
		// first in the converted transcript but not in the given one: the leading-system-message path converts it.
		"leading system message after dropped aborted assistant": {
			AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: "partial"}}, StopReason: StopReasonAborted},
			SystemMessage{Content: SystemText("s"), ToolsAdded: []ToolSchema{strictSamplingTool("require")}},
			UserMessage{Content: UserText("hi")},
		},
	}
	for name, messages := range rows {
		t.Run(name, func(t *testing.T) {
			msg, sent := streamStrictRequireError(t, cfg, messages)
			if msg != strictRequireUnsupportedError {
				t.Fatalf("error = %q, want %q", msg, strictRequireUnsupportedError)
			}
			if sent {
				t.Fatal("request was sent")
			}
		})
	}
}
