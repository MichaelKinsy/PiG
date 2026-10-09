package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type anthropicSetupProbe struct {
	Simple   bool   `json:"simple"`
	Provider string `json:"provider"`
	Aborted  bool   `json:"aborted"`
	Options  struct {
		APIKey  *string            `json:"apiKey,omitempty"`
		Headers map[string]*string `json:"headers,omitempty"`
	} `json:"options"`
}

// pi: packages/ai/src/api/anthropic-messages.lazy.ts

// anthropicMessagesApi (anthropic-messages.lazy.ts, lazy.ts lazyApi): the returned stream exists at once and a request that fails while it is
// set up ends it with the setup error message (anthropic-messages.ts:316-326, 611-613, 933-935: "No API key for provider: <provider>" unless the
// options carry an API key or a non-blank authorization, x-api-key or cf-aig-authorization header, matched case-insensitively). The pinned pi-ai
// and PiG give each request the same events, stop reason, error text, identity fields, content and usage, for stream and streamSimple.
func TestAnthropicMessagesAPISetupMatchesPi(t *testing.T) {
	headerSets := []map[string]*string{
		nil, {}, {"authorization": new("Bearer x")}, {"Authorization": new("  ")}, {"AUTHORIZATION": new("Bearer x")}, {"x-api-key": new("k")}, {"X-Api-Key": new("")},
		{"cf-aig-authorization": new("t")}, {"x-api-key": nil}, {"authorization": nil, "x-api-key": new("k")}, {"x-other": new("v")}, {"authorization": new("\t\n")},
	}
	keys := []*string{nil, new(""), new("sk-ant-api"), new("sk-ant-oat-x")}
	var probes []anthropicSetupProbe
	for _, simple := range []bool{false, true} {
		for _, provider := range []string{"anthropic", "github-copilot", "acme"} {
			for _, key := range keys {
				for _, headers := range headerSets {
					probe := anthropicSetupProbe{Simple: simple, Provider: provider, Aborted: true}
					probe.Options.APIKey, probe.Options.Headers = key, headers
					probes = append(probes, probe)
				}
			}
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/anthropic_setup.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	// A key or federation variable in the environment would change what the probes see.
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "ANTHROPIC_") && !strings.HasPrefix(entry, "CLAUDE_") && !strings.HasPrefix(entry, "AWS_") && !strings.HasPrefix(entry, "GOOGLE_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []struct {
		Events       []string `json:"events"`
		StopReason   string   `json:"stopReason"`
		ErrorMessage *string  `json:"errorMessage"`
		API          string   `json:"api"`
		Provider     string   `json:"provider"`
		Model        string   `json:"model"`
		Content      []any    `json:"content"`
	}
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_AUTH_TOKEN"} {
		t.Setenv(name, "")
	}
	failures := 0
	for i, probe := range probes {
		model := &Model{ID: "m", DisplayName: "m", Input: []string{"text"}, ProviderMeta: ProviderMetadata{API: APIAnthropicMessages, ProviderID: probe.Provider, BaseURL: "http://127.0.0.1:1"}, Capabilities: ModelCapabilities{ContextWindow: 1000, MaxOutputTokens: 100}}
		options := StreamOptions{Headers: ProviderHeaders(probe.Options.Headers)}
		if probe.Options.APIKey != nil {
			options.APIKey = *probe.Options.APIKey
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		transcript := newTranscriptContext([]Message{UserMessage{Content: UserText("hi"), Timestamp: 1}})
		run := AnthropicMessagesAPI().Stream
		if probe.Simple {
			run = AnthropicMessagesAPI().StreamSimple
		}
		stream, err := run(ctx, model, transcript, options)
		if err != nil {
			t.Fatalf("probe %d: %v; the lazy API returns the stream at once", i, err)
		}
		var events []string
		for event := range stream.Events(t.Context()) {
			events = append(events, string(event.EventType()))
		}
		got := stream.Result()
		wantMessage := ""
		if want[i].ErrorMessage != nil {
			wantMessage = *want[i].ErrorMessage
		}
		if string(got.StopReason) != want[i].StopReason || got.ErrorMessage != wantMessage || string(got.API) != want[i].API || got.Provider != want[i].Provider || got.Model != want[i].Model ||
			len(got.Content) != len(want[i].Content) || !reflect.DeepEqual(events, want[i].Events) {
			if failures++; failures <= 10 {
				label, _ := json.Marshal(probe)
				t.Errorf("probe %s:\n PiG %v stop=%q error=%q %s/%s/%s content=%d\n Pi  %v stop=%q error=%q %s/%s/%s content=%d", label, events, got.StopReason, got.ErrorMessage, got.API, got.Provider, got.Model, len(got.Content),
					want[i].Events, want[i].StopReason, wantMessage, want[i].API, want[i].Provider, want[i].Model, len(want[i].Content))
			}
		}
	}
	if failures > 10 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}
