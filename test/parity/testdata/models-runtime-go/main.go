package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/ai"
)

func model(id string) *ai.Model {
	return &ai.Model{ID: id, DisplayName: id, ProviderMeta: ai.ProviderMetadata{ProviderID: "p1", API: "test-api", BaseURL: "https://example.test/v1"}, Input: []string{"text"}, Capabilities: ai.ModelCapabilities{ContextWindow: 10000, MaxOutputTokens: 1000}}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()
	credentials := ai.NewInMemoryCredentialStore()
	if _, err := credentials.Modify(ctx, "p1", func(*ai.Credential) (*ai.Credential, error) {
		return &ai.Credential{Type: ai.CredentialAPIKey, Key: "stored-key"}, nil
	}); err != nil {
		return err
	}
	store := ai.NewInMemoryModelsStore()
	models := ai.CreateModels(ai.CreateModelsOptions{Credentials: credentials, ModelsStore: store})
	var fetchedCredential *ai.Credential
	var force *bool
	var call map[string]any
	transforms := 0
	respond := func(_ context.Context, m *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		call = map[string]any{"model": m.ID, "baseUrl": m.ProviderMeta.BaseURL, "apiKey": options.APIKey, "headers": options.Headers, "env": options.Env, "transformForwarded": options.TransformHeaders != nil, "messages": transcript.Messages()}
		stream := ai.NewAssistantMessageEventStream()
		message := &ai.AssistantMessage{API: m.ProviderMeta.API, Provider: m.ProviderMeta.ProviderID, Model: m.ID, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "ok"}}, StopReason: ai.StopReasonStop}
		if err := stream.Push(ai.StartEvent{Partial: message}); err != nil {
			return nil, err
		}
		if err := stream.Push(ai.DoneEvent{Reason: ai.StopReasonStop, Message: message}); err != nil {
			return nil, err
		}
		return stream, nil
	}
	provider := ai.CreateProvider(ai.CreateProviderOptions{ID: "p1", Models: []ai.AnyModel{model("baseline")}, Auth: ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Name: "Key", Resolve: func(_ context.Context, input ai.APIKeyAuthInput) (*ai.AuthResult, error) {
		key := "ambient-key"
		if input.Credential != nil {
			key = input.Credential.Key
		}
		return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: key, BaseURL: "https://auth.test/v1", Headers: ai.ProviderHeaders{"X-Shared": new("auth"), "x-provider": new("provider")}}, Env: map[string]string{"PROVIDER": "provider", "SHARED": "provider"}, Source: "stored"}, nil
	}}}, FetchModels: func(refresh ai.RefreshModelsContext) ([]ai.AnyModel, error) {
		fetchedCredential = refresh.Credential
		force = refresh.Force
		return []ai.AnyModel{model("dynamic")}, nil
	}, API: &ai.ProviderStreams{Stream: respond, StreamSimple: respond}})
	models.SetProvider(provider)
	refresh := models.Refresh(ctx, ai.ModelsRefreshOptions{Force: new(true)})
	if refresh.Aborted || len(refresh.Errors) != 0 {
		return fmt.Errorf("refresh: %+v", refresh)
	}
	stored, err := store.Read(ctx, "p1")
	if err != nil {
		return err
	}
	var storedModels []any
	for _, raw := range stored.Models {
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		storedModels = append(storedModels, value)
	}
	list := []string{}
	for _, m := range models.GetModels() {
		list = append(list, m.ID)
	}
	selected := models.GetModel("p1", "dynamic")
	selected.ProviderMeta.Headers = map[string]string{"x-model": "model", "X-Shared": "model"}
	request := ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi"), Timestamp: 7}}}
	stream := models.StreamSimple(ctx, selected, request, ai.StreamOptions{APIKey: "explicit-key", Headers: ai.ProviderHeaders{"x-shared": new("request")}, Env: ai.ProviderEnv{"REQUEST": "request", "SHARED": "request"}, TransformHeaders: func(_ context.Context, headers ai.ProviderHeaders) (ai.ProviderHeaders, error) {
		transforms++
		headers["x-transformed"] = new("yes")
		return headers, nil
	}})
	events := []string{}
	for event := range stream.Events(ctx) {
		events = append(events, string(event.EventType()))
	}
	message := stream.Result()
	var publication func(ai.ModelsPublication) (bool, error)
	models.SetProvider(&ai.ModelsProvider{ID: "old", Name: "old", GetModels: func() ([]*ai.Model, error) { return nil, nil }, RefreshModels: func(refresh ai.RefreshModelsContext) error { publication = refresh.Publish; return nil }})
	models.Refresh(ctx, ai.ModelsRefreshOptions{Providers: []string{"old"}, AllowNetwork: new(false)})
	models.SetProvider(&ai.ModelsProvider{ID: "old", Name: "replacement"})
	lateUpdate := false
	accepted, err := publication(ai.ModelsPublication{Update: func() { lateUpdate = true }})
	if err != nil {
		return err
	}
	ghost := models.CompleteSimple(ctx, &ai.Model{ID: "ghost-model", ProviderMeta: ai.ProviderMetadata{ProviderID: "ghost", API: "test-api"}}, request)
	emptyContext := ai.AuthContext{Env: func(name string) (string, bool) {
		if name == "EMPTY" {
			return "", true
		}
		return "fallback-key", true
	}}
	emptyModels := ai.CreateModels(ai.CreateModelsOptions{AuthContext: &emptyContext})
	emptyModels.SetProvider(&ai.ModelsProvider{ID: "env", Name: "Env", Auth: ai.ProviderAuth{APIKey: ai.EnvAPIKeyAuth("Key", "EMPTY", "FALLBACK")}})
	envAuth, err := emptyModels.GetAuth(ctx, "env")
	if err != nil {
		return err
	}
	output := map[string]any{"envFallback": envAuth.Auth.APIKey, "models": list, "stored": storedModels, "credential": fetchedCredential, "force": force, "call": call, "events": events, "result": map[string]any{"content": message.Content, "stopReason": message.StopReason}, "transforms": transforms, "staleAccepted": accepted, "staleUpdated": lateUpdate, "unknownProvider": ghost.ErrorMessage}
	data, err := json.Marshal(output)
	if err != nil {
		return err
	}
	var canonical any
	if err := json.Unmarshal(data, &canonical); err != nil {
		return err
	}
	data, err = json.Marshal(canonical)
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}
