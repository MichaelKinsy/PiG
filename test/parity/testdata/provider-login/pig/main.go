package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/ai"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	output := map[string]any{}
	cases := []struct {
		name, provider string
		answers        []string
	}{{"env", "", []string{"entered-key"}}, {"bedrock-bearer", "amazon-bedrock", []string{"bearer-token", "bedrock-token"}}, {"bedrock-profile", "amazon-bedrock", []string{"aws-profile", "work"}}, {"vertex-key", "google-vertex", []string{"api-key", "vertex-key"}}, {"vertex-adc", "google-vertex", []string{"adc", "project-id", "us-central1"}}}
	for _, tc := range cases {
		auth := ai.EnvAPIKeyAuth("Test key", "TEST_KEY")
		if tc.provider != "" {
			provider, err := ai.BuiltinProviderAuth(tc.provider)
			if err != nil {
				return err
			}
			auth = provider.APIKey
		}
		prompts := []ai.AuthPrompt{}
		events := []ai.AuthEvent{}
		index := 0
		credential, err := auth.Login(context.Background(), ai.AuthInteraction{Prompt: func(_ context.Context, prompt ai.AuthPrompt) (string, error) {
			prompts = append(prompts, prompt)
			if index >= len(tc.answers) {
				return "", fmt.Errorf("unexpected prompt")
			}
			value := tc.answers[index]
			index++
			return value, nil
		}, Notify: func(event ai.AuthEvent) { events = append(events, event) }})
		if err != nil {
			return err
		}
		output[tc.name] = map[string]any{"credential": credential, "prompts": prompts, "events": events}
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return err
	}
	var canonical any
	if err := json.Unmarshal(raw, &canonical); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(canonical)
}
