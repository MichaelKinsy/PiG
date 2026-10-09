package coding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

const fauxOraclePath = "testdata/rpc33-observation/providers/faux/pi.json"

type fauxAgentOracle struct {
	Pi      string `json:"pi"`
	Results []struct {
		Fixture struct {
			Name            string  `json:"name"`
			TokensPerSecond float64 `json:"tokensPerSecond"`
			TokenSize       int     `json:"tokenSize"`
			NoResponse      bool    `json:"noResponse"`
			Factory         string  `json:"factory"`
			Content         []struct {
				Type      string         `json:"type"`
				Text      string         `json:"text"`
				Thinking  string         `json:"thinking"`
				ID        string         `json:"id"`
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"content"`
			Message struct {
				StopReason   string `json:"stopReason"`
				ErrorMessage string `json:"errorMessage"`
				ResponseID   string `json:"responseId"`
			} `json:"message"`
		} `json:"fixture"`
		Direct []struct {
			Layers  int               `json:"layers"`
			Mode    string            `json:"mode"`
			Records []json.RawMessage `json:"records"`
		} `json:"direct"`
	} `json:"results"`
}

// TestFauxAgentObservationOracle replays Pi's faux provider through 0-2 lazyStream layers and pi-agent-core's runAgentLoop.
// upstream: packages/ai/src/providers/faux.ts:streamWithDeltas; packages/agent/src/agent-loop.ts:408-453 (shallow copies); packages/ai/src/api/lazy.ts:31-61.
// The oracle is providers/faux/probe.mjs `agent` mode. Each Pi record also carries the faux push count at delivery; the ai package's TestFauxObservationOracle compares that count, this test compares the delivered Agent events, which include the block objects the provider still mutates.
func TestFauxAgentObservationOracle(t *testing.T) {
	var oracle fauxAgentOracle
	readObservationJSON(t, fauxOraclePath, &oracle)
	if oracle.Pi != UpstreamVersion {
		t.Fatalf("faux oracle pins Pi %s; current Pi %s requires a fresh oracle", oracle.Pi, UpstreamVersion)
	}
	for _, result := range oracle.Results {
		for _, direct := range result.Direct {
			if direct.Mode != "agent" {
				continue
			}
			t.Run(fmt.Sprintf("%s/layers%d", result.Fixture.Name, direct.Layers), func(t *testing.T) {
				t.Parallel()
				fixture := result.Fixture
				provider := ai.NewFauxProvider(ai.FauxConfig{API: "faux-probe", ProviderID: "faux-probe", TokenSize: &ai.FauxTokenSize{Min: new(fixture.TokenSize), Max: new(fixture.TokenSize)}, TokensPerSecond: int(fixture.TokensPerSecond)})
				if !fixture.NoResponse {
					response := ai.FauxResponse{StopReason: fixture.Message.StopReason, ErrorMessage: fixture.Message.ErrorMessage, ResponseID: fixture.Message.ResponseID, Timestamp: new(int64(1))}
					for _, block := range fixture.Content {
						switch block.Type {
						case "text":
							response.Content = append(response.Content, ai.FauxText(block.Text))
						case "thinking":
							response.Content = append(response.Content, ai.FauxThinking(block.Thinking))
						default:
							response.Content = append(response.Content, ai.FauxToolCall(block.Name, block.Arguments, &ai.FauxToolCallOptions{ID: block.ID}))
						}
					}
					switch fixture.Factory {
					case "value":
						provider.SetResponses([]ai.FauxResponseStep{ai.FauxFactoryStep(func(ai.TranscriptContext, ai.StreamOptions, *ai.FauxProviderState, *ai.Model) (ai.AssistantMessage, error) {
							return response.AssistantMessage(), nil
						})})
					case "reject":
						provider.SetResponses([]ai.FauxResponseStep{ai.FauxFactoryStep(func(ai.TranscriptContext, ai.StreamOptions, *ai.FauxProviderState, *ai.Model) (ai.AssistantMessage, error) {
							return ai.FauxResponse{}.AssistantMessage(), errors.New("scripted factory failure")
						})})
					default:
						provider.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(response)})
					}
				}
				model := provider.GetModel()
				streamFn := func(ctx context.Context, m *ai.Model, transcript ai.TranscriptContext, opts ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
					makeStream := func(ctx context.Context) (*ai.AssistantMessageEventStream, error) {
						return provider.Stream(ctx, transcript, opts)
					}
					for range direct.Layers {
						inner := makeStream
						makeStream = func(ctx context.Context) (*ai.AssistantMessageEventStream, error) {
							return ai.LazyStream(ctx, m, inner), nil
						}
					}
					return makeStream(ctx)
				}
				var got []json.RawMessage
				a := mustNewAgent(agent.AgentOptions{Model: model, Tools: []agent.AgentTool{}, StreamFn: streamFn,
					FinishTurn: func(context.Context, agent.AgentTurnContext) (*agent.AgentTurnDecision, error) {
						return &agent.AgentTurnDecision{Action: agent.AgentTurnEnd}, nil
					},
					OnEvent: func(event agent.AgentEvent) {
						wire := observationAgentEvent(t, event)
						record, ok := wire.(map[string]any)
						if !ok {
							return
						}
						message, _ := record["message"].(agent.AgentMessage)
						if message.Assistant == nil || !strings.HasPrefix(fmt.Sprint(record["type"]), "message_") {
							return
						}
						got = append(got, observationCopy(t, map[string]any{"event": wire}))
					},
				})
				user := agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "probe"}}, Timestamp: 1}}
				if _, err := a.SendMessages(t.Context(), []agent.AgentMessage{user}); err != nil {
					t.Fatalf("Agent: %v", err)
				}
				want := make([]map[string]json.RawMessage, len(direct.Records))
				for i, raw := range direct.Records {
					if err := json.Unmarshal(raw, &want[i]); err != nil {
						t.Fatal(err)
					}
					delete(want[i], "pushed")
				}
				wantJSON, gotJSON := observationComparable(t, want), observationComparable(t, got)
				if string(wantJSON) != string(gotJSON) {
					t.Errorf("Agent observation differs from Pi\nPi: %s\nGo: %s", wantJSON, gotJSON)
				}
			})
		}
	}
}

const testFauxOraclePath = "testdata/rpc33-observation/providers/test-faux/pi.json"

// TestTestFauxAgentObservationOracle replays Pi's paired test-faux fixture through 0-2 lazyStream layers and pi-agent-core's runAgentLoop.
// upstream: test/parity/testdata/test-faux-provider.ts:streamTestFaux; packages/agent/src/agent-loop.ts:408-453; packages/ai/src/api/lazy.ts:31-61.
// The oracle is providers/test-faux/probe.mjs `agent` mode; the ai package's TestTestFauxObservationOracle compares the push counts.
func TestTestFauxAgentObservationOracle(t *testing.T) {
	var oracle struct {
		Pi      string `json:"pi"`
		Results []struct {
			Scenario struct {
				Name   string `json:"name"`
				Prompt string `json:"prompt"`
			} `json:"scenario"`
			Direct []struct {
				Layers  int               `json:"layers"`
				Mode    string            `json:"mode"`
				Records []json.RawMessage `json:"records"`
			} `json:"direct"`
		} `json:"results"`
	}
	readObservationJSON(t, testFauxOraclePath, &oracle)
	if oracle.Pi != UpstreamVersion {
		t.Fatalf("test-faux oracle pins Pi %s; current Pi %s requires a fresh oracle", oracle.Pi, UpstreamVersion)
	}
	for _, result := range oracle.Results {
		for _, direct := range result.Direct {
			if direct.Mode != "agent" {
				continue
			}
			t.Run(fmt.Sprintf("%s/layers%d", result.Scenario.Name, direct.Layers), func(t *testing.T) {
				t.Parallel()
				provider := &ai.TestFauxProvider{}
				model := &ai.Model{ID: "faux-1", ProviderMeta: ai.ProviderMetadata{ProviderID: "test-faux", API: "test-faux"}, Provider: provider}
				streamFn := func(ctx context.Context, m *ai.Model, transcript ai.TranscriptContext, opts ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
					makeStream := func(ctx context.Context) (*ai.AssistantMessageEventStream, error) {
						return provider.Stream(ctx, transcript, opts)
					}
					for range direct.Layers {
						inner := makeStream
						makeStream = func(ctx context.Context) (*ai.AssistantMessageEventStream, error) {
							return ai.LazyStream(ctx, m, inner), nil
						}
					}
					return makeStream(ctx)
				}
				var got []json.RawMessage
				a := mustNewAgent(agent.AgentOptions{Model: model, Tools: []agent.AgentTool{}, StreamFn: streamFn,
					FinishTurn: func(context.Context, agent.AgentTurnContext) (*agent.AgentTurnDecision, error) {
						return &agent.AgentTurnDecision{Action: agent.AgentTurnEnd}, nil
					},
					OnEvent: func(event agent.AgentEvent) {
						record, ok := observationAgentEvent(t, event).(map[string]any)
						if !ok {
							return
						}
						message, _ := record["message"].(agent.AgentMessage)
						if message.Assistant == nil || !strings.HasPrefix(fmt.Sprint(record["type"]), "message_") {
							return
						}
						got = append(got, observationCopy(t, map[string]any{"event": record}))
					},
				})
				user := agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: result.Scenario.Prompt}}, Timestamp: 1}}
				if _, err := a.SendMessages(t.Context(), []agent.AgentMessage{user}); err != nil {
					t.Fatalf("Agent: %v", err)
				}
				want := make([]map[string]json.RawMessage, len(direct.Records))
				for i, raw := range direct.Records {
					if err := json.Unmarshal(raw, &want[i]); err != nil {
						t.Fatal(err)
					}
					delete(want[i], "pushed")
				}
				if len(want) == 0 || len(got) != len(want) {
					t.Fatalf("Agent delivered %d assistant events; Pi delivered %d", len(got), len(want))
				}
				wantJSON, gotJSON := observationComparable(t, want), observationComparable(t, got)
				if string(wantJSON) != string(gotJSON) {
					t.Errorf("Agent observation differs from Pi\nPi: %s\nGo: %s", wantJSON, gotJSON)
				}
			})
		}
	}
}
