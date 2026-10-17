package main

import (
	"context"
	"encoding/json"
	"errors"
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
	data, err := os.ReadFile("test/parity/testdata/google-thinking-maps.json")
	if err != nil {
		return err
	}
	var cases []struct {
		Name, ID string
		Mapping  ai.ThinkingLevelMap
		Level    ai.ThinkingLevel
		Budget   int
	}
	if err = json.Unmarshal(data, &cases); err != nil {
		return err
	}
	for _, vertex := range []bool{false, true} {
		for _, test := range cases {
			var provider ai.Provider
			adapter := "google"
			if vertex {
				adapter = "vertex"
				provider = ai.NewGoogleVertexProvider(ai.GoogleVertexConfig{Model: test.ID, APIKey: "test", ProviderID: "test-vertex", BaseURL: "https://example.invalid/v1", ThinkingLevelMap: test.Mapping})
			} else {
				provider = ai.NewGoogleProvider(ai.GoogleConfig{Model: test.ID, APIKey: "test", ProviderID: "test-google", BaseURL: "https://example.invalid/v1beta", ThinkingLevelMap: test.Mapping})
			}
			captured := errors.New("payload captured")
			var payload struct {
				Config struct {
					ThinkingConfig json.RawMessage `json:"thinkingConfig"`
				} `json:"config"`
			}
			// The paired Pi probe calls streamSimple, which sends `thinking: { enabled: false }` for omitted reasoning.
			api := ai.APIGoogleGenerativeAI
			if vertex {
				api = ai.APIGoogleVertex
			}
			options := ai.StreamOptions{IsReasoning: true, Thinking: test.Level, ThinkingBudgets: &ai.ThinkingBudgets{High: test.Budget}, OnPayload: func(value any, _ *ai.Model) (any, error) {
				data, e := json.Marshal(value)
				if e != nil {
					return nil, e
				}
				if e = json.Unmarshal(data, &payload); e != nil {
					return nil, e
				}
				return nil, captured
			}}
			ai.ApplyOmittedReasoning(&ai.Model{ProviderMeta: ai.ProviderMetadata{API: api}}, &options)
			_, err = provider.Stream(context.Background(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Hello")}}}), options)
			if !errors.Is(err, captured) {
				return err
			}
			fmt.Printf("%s/%s %s\n", adapter, test.Name, payload.Config.ThinkingConfig)
			if err = provider.Close(); err != nil {
				return err
			}
		}
	}
	return nil
}
