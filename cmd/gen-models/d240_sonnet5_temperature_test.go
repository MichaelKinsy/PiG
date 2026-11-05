package main

import (
	"encoding/json"
	"testing"
)

// D240 (Pi 4b9777737): the Sonnet 5 entries of an anthropic-messages provider get supportsTemperature false after forceAdaptiveThinking; nothing else changes.
func TestApplyD240SonnetFiveTemperature(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"sonnet 5 on anthropic-messages", `{"id":"claude-sonnet-5","api":"anthropic-messages","compat":{"forceAdaptiveThinking":true,"supportsStrictTools":true}}`, `{"id":"claude-sonnet-5","api":"anthropic-messages","compat":{"forceAdaptiveThinking":true,"supportsTemperature":false,"supportsStrictTools":true}}`},
		{"openrouter prefix", `{"id":"anthropic/claude-sonnet-5","api":"anthropic-messages","compat":{"forceAdaptiveThinking":true}}`, `{"id":"anthropic/claude-sonnet-5","api":"anthropic-messages","compat":{"forceAdaptiveThinking":true,"supportsTemperature":false}}`},
		{"without forceAdaptiveThinking", `{"id":"claude-sonnet-5","api":"anthropic-messages","compat":{"supportsStrictTools":true}}`, `{"id":"claude-sonnet-5","api":"anthropic-messages","compat":{"supportsStrictTools":true,"supportsTemperature":false}}`},
		{"already set (sonnet 5.5)", `{"id":"claude-sonnet-5-5","api":"anthropic-messages","compat":{"forceAdaptiveThinking":true,"supportsTemperature":false}}`, `{"id":"claude-sonnet-5-5","api":"anthropic-messages","compat":{"forceAdaptiveThinking":true,"supportsTemperature":false}}`},
		{"bedrock converse", `{"id":"anthropic.claude-sonnet-5","api":"bedrock-converse-stream","compat":{}}`, `{"id":"anthropic.claude-sonnet-5","api":"bedrock-converse-stream","compat":{}}`},
		{"openai batch entry", `{"id":"anthropic/claude-sonnet-5:batch","api":"openai-completions","compat":{"a":1}}`, `{"id":"anthropic/claude-sonnet-5:batch","api":"openai-completions","compat":{"a":1}}`},
		{"another family", `{"id":"claude-opus-4-6","api":"anthropic-messages","compat":{"forceAdaptiveThinking":true}}`, `{"id":"claude-opus-4-6","api":"anthropic-messages","compat":{"forceAdaptiveThinking":true}}`},
		{"no compat gets one", `{"id":"claude-sonnet-5","api":"anthropic-messages"}`, `{"id":"claude-sonnet-5","api":"anthropic-messages","compat":{"supportsTemperature":false}}`},
		{"no compat, another family", `{"id":"claude-opus-4-6","api":"anthropic-messages"}`, `{"id":"claude-opus-4-6","api":"anthropic-messages"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := applyD240SonnetFiveTemperature(json.RawMessage(tc.in))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}
