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

func strictSamplingTool(strict string) ToolSchema {
	return ToolSchema{
		Name: "lookup", Description: "d",
		Parameters:          map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}}, "required": []any{"a"}},
		ConstrainedSampling: &ConstrainedSamplingConfig{Type: "json_schema", Strict: ConstrainedSamplingStrict(strict)},
	}
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
			// prefer never errors; unsupported rows send a plain tool without strict.
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
			const want = `{"type":"function","function":{"name":"lookup","description":"d","parameters":{"properties":{"a":{"type":"string"}},"required":["a"],"type":"object"}}}`
			if string(got) != want {
				t.Fatalf("got %s\nwant %s", got, want)
			}
		})
	}
}

func TestCompletionsStreamStrictRequireUnsupportedFailsBeforeRequest(t *testing.T) {
	for name, cfg := range strictRequireRows() {
		if cfg.Compat != nil && cfg.Compat.SupportsStrictMode != nil && *cfg.Compat.SupportsStrictMode {
			continue
		}
		t.Run(name, func(t *testing.T) {
			sent := false
			p := &openAIProvider{cfg: cfg}
			p.client = &http.Client{Transport: openAITestRoundTripperFunc(func(*http.Request) (*http.Response, error) {
				sent = true
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: [DONE]\n\n"))}, nil
			})}
			tctx := NormalizeContext(Context{Messages: []Message{
				SystemMessage{Content: SystemText("s"), ToolsAdded: []ToolSchema{strictSamplingTool("require")}},
				UserMessage{Content: UserText("hi")},
			}})
			var msg string
			stream, err := p.Stream(context.Background(), tctx, StreamOptions{})
			if err != nil {
				msg = err.Error()
			} else {
				for ev := range stream.Events(context.Background()) {
					if e, ok := ev.(ErrorEvent); ok {
						msg = e.Error.ErrorMessage
					}
				}
			}
			if msg != strictRequireUnsupportedError {
				t.Fatalf("error = %q, want %q", msg, strictRequireUnsupportedError)
			}
			if sent {
				t.Fatal("request was sent")
			}
		})
	}
}
