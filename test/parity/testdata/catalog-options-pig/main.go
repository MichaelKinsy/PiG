package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	transcript := ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Use the tool")}}, Tools: []ai.ToolSchema{{Name: "lookup", Description: "Look up a value", Parameters: map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}, "required": []string{"value"}}}}})
	var records []map[string]any
	for _, tc := range []struct {
		provider, session string
		retention         ai.CacheRetention
		compat            *ai.AnthropicMessagesCompat
		env               string
	}{
		{"fireworks", "fireworks-session-1", ai.CacheRetentionShort, &ai.AnthropicMessagesCompat{SendSessionAffinityHeaders: new(true), SupportsCacheControlOnTools: new(false), SupportsEagerToolInputStreaming: new(false)}, ""},
		{"fireworks", "fireworks-session-2", ai.CacheRetentionNone, &ai.AnthropicMessagesCompat{SendSessionAffinityHeaders: new(true), SupportsCacheControlOnTools: new(false), SupportsEagerToolInputStreaming: new(false)}, ""},
		{"openrouter", "openrouter-session-1", ai.CacheRetentionShort, &ai.AnthropicMessagesCompat{}, ""},
		{"openrouter", "openrouter-session-2", ai.CacheRetentionNone, &ai.AnthropicMessagesCompat{}, ""},
		{"openrouter", "openrouter-session-3", ai.CacheRetentionShort, &ai.AnthropicMessagesCompat{SendSessionAffinityHeaders: new(false)}, ""},
		{"anthropic", "anthropic-session-1", ai.CacheRetentionShort, &ai.AnthropicMessagesCompat{}, ""},
		{"openrouter", "env-none", "", &ai.AnthropicMessagesCompat{}, "none"},
		{"openrouter", "env-long", "", &ai.AnthropicMessagesCompat{}, "long"},
		{"openrouter", "explicit-none", ai.CacheRetentionNone, &ai.AnthropicMessagesCompat{}, "long"},
	} {
		var record map[string]any
		requestError := make(chan error, 1)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				requestError <- err
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			record = map[string]any{"provider": tc.provider, "retention": tc.retention, "session": tc.session, "affinity": r.Header.Get("x-session-affinity"), "sessionId": r.Header.Get("x-session-id"), "tools": body["tools"], "messages": body["messages"]}
			requestError <- nil
			w.Header().Set("Content-Type", "text/event-stream")
		}))
		id := "claude-opus-4-8"
		switch tc.provider {
		case "fireworks":
			id = "accounts/fireworks/models/kimi-k2p6"
		case "openrouter":
			id = "anthropic/claude-opus-4.8"
		}
		p := ai.NewAnthropicProvider(ai.AnthropicConfig{APIKey: "test-key", Model: id, ProviderID: tc.provider, BaseURL: server.URL, Compat: tc.compat})
		stream, err := p.Stream(context.Background(), transcript, ai.StreamOptions{SessionID: tc.session, CacheRetention: tc.retention, Env: ai.ProviderEnv{"PI_CACHE_RETENTION": tc.env}})
		if err == nil {
			_ = stream.Result()
		}
		closeErr := p.Close()
		server.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if err := <-requestError; err != nil {
			return err
		}
		if record == nil {
			return errors.New("request not captured")
		}
		records = append(records, record)
	}
	agentDir, err := os.MkdirTemp("", "catalog-options-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(agentDir) }()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: agentDir, AgentDir: agentDir})
	if err != nil {
		return err
	}
	model := services.ModelRuntime().GetModel("fireworks", "accounts/fireworks/models/kimi-k3")
	if model == nil {
		return errors.New("Kimi K3 missing")
	}
	captured := errors.New("captured")
	var payload map[string]any
	result := services.ModelRuntime().CompleteSimple(context.Background(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Use the tool")}}}, ai.StreamOptions{APIKey: "test-key", Thinking: ai.ThinkingLevelMax, OnPayload: func(value any, _ *ai.Model) (any, error) {
		data, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		if err = json.Unmarshal(data, &payload); err != nil {
			return nil, err
		}
		return nil, captured
	}})
	if result.StopReason != ai.StopReasonError || payload == nil {
		return fmt.Errorf("payload capture: %+v", result)
	}
	records = append(records, map[string]any{"provider": "fireworks-kimi-k3", "effort": payload["reasoning_effort"]})
	return json.NewEncoder(os.Stdout).Encode(records)
}
