package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

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
	data, err := os.ReadFile("test/parity/testdata/thinking-disable.json")
	if err != nil {
		return err
	}
	var cases []struct {
		Provider, Model string
		MaxTokens       int
	}
	if err = json.Unmarshal(data, &cases); err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "pig-thinking-disable-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
	if err != nil {
		return err
	}
	for _, test := range cases {
		model, err := coding.BuildModel(test.Provider+"/"+test.Model, services)
		if err != nil {
			return err
		}
		var payload map[string]json.RawMessage
		result := services.ModelRuntime().StreamSimple(context.Background(), model, ai.Context{SystemPrompt: "You are a precise assistant. Follow the requested output format exactly.", Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Before replying, carefully solve 36863 * 5279 internally. Then reply with the word pong repeated exactly 40 times, separated by single spaces. Do not add any other text.")}}}, ai.StreamOptions{APIKey: "test", MaxTokens: test.MaxTokens, TemperatureSet: test.Provider != "openai", OnPayload: func(value any, _ *ai.Model) (any, error) {
			data, err := json.Marshal(value)
			if err != nil {
				return nil, err
			}
			if err = json.Unmarshal(data, &payload); err != nil {
				return nil, err
			}
			return nil, errors.New("payload captured")
		}}).Result()
		if !strings.Contains(result.ErrorMessage, "payload captured") {
			return fmt.Errorf("result=%+v", result)
		}
		control := payload["reasoning"]
		if test.Provider == "anthropic" {
			control = payload["thinking"]
		}
		if test.Provider == "google" || test.Provider == "google-vertex" {
			var config map[string]json.RawMessage
			if err = json.Unmarshal(payload["config"], &config); err != nil {
				return err
			}
			control = config["thinkingConfig"]
		}
		if len(control) == 0 {
			control = json.RawMessage("null")
		}
		fmt.Printf("%s/%s %s\n", test.Provider, test.Model, control)
	}
	return nil
}
