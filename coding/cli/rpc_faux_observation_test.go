package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

const fauxRPCOraclePath = "../../coding/testdata/rpc33-observation/providers/faux/pi.json"

type fauxRPCFixture struct {
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
}

type fauxRPCOracle struct {
	Pi      string `json:"pi"`
	Results []struct {
		Fixture fauxRPCFixture `json:"fixture"`
		RPC     [][]struct {
			Kind   string          `json:"kind"`
			Record json.RawMessage `json:"record"`
		} `json:"rpc"`
	} `json:"results"`
}

// fauxRPCComparable keeps every key and value except the clocks and the usage numbers. Usage derives from the request text, which includes each product's system prompt; the probe's system prompt is Pi's.
func fauxRPCComparable(t testing.TB, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	var canonicalize func(value any, inUsage bool)
	canonicalize = func(value any, inUsage bool) {
		switch value := value.(type) {
		case []any:
			for _, item := range value {
				canonicalize(item, inUsage)
			}
		case map[string]any:
			if value["role"] == "assistant" {
				if _, ok := value["timestamp"].(json.Number); ok {
					value["timestamp"] = json.Number("0")
				}
				delete(value, "durationMs") // the measured response time (Pi 1.1.0, #10549) depends on the clock, as timestamp does
			}
			for key, item := range value {
				if _, ok := item.(json.Number); ok && inUsage {
					value[key] = json.Number("0")
					continue
				}
				if key != "arguments" {
					canonicalize(item, inUsage || key == "usage")
				}
			}
		}
	}
	canonicalize(decoded, false)
	out, err := json.MarshalIndent(decoded, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(out, '\n')
}

func fauxRPCRecords(t testing.TB, fixture fauxRPCFixture) []json.RawMessage {
	t.Helper()
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
		var steps []ai.FauxResponseStep
		switch fixture.Factory {
		case "value":
			steps = append(steps, ai.FauxFactoryStep(func(ai.TranscriptContext, ai.StreamOptions, *ai.FauxProviderState, *ai.Model) (ai.AssistantMessage, error) {
				return response.AssistantMessage(), nil
			}))
		case "reject":
			steps = append(steps, ai.FauxFactoryStep(func(ai.TranscriptContext, ai.StreamOptions, *ai.FauxProviderState, *ai.Model) (ai.AssistantMessage, error) {
				return ai.FauxResponse{}.AssistantMessage(), errors.New("scripted factory failure")
			}))
		default:
			steps = append(steps, ai.FauxStaticStep(response))
		}
		if fixture.Message.StopReason == "toolUse" {
			steps = append(steps, ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}, Timestamp: new(int64(2))}))
		}
		provider.SetResponses(steps)
	}
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer services.Close()
	session, err := coding.NewSession(services, coding.SessionOptions{Model: provider.GetModel(), SkipBuiltinTools: true, SessionDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	var records []json.RawMessage
	unsubscribe := subscribeRPCEvents(session, func(frame any) {
		raw, err := json.Marshal(frame)
		if err != nil {
			t.Error(err)
			return
		}
		var head struct {
			Type    string `json:"type"`
			Message *struct {
				Role string `json:"role"`
			} `json:"message"`
			AssistantMessageEvent json.RawMessage `json:"assistantMessageEvent"`
		}
		if err := json.Unmarshal(raw, &head); err != nil {
			t.Error(err)
			return
		}
		// probe.mjs `extension.mjs` records assistant message_start/message_end and message_update.
		if (head.Type == "message_start" || head.Type == "message_end") && head.Message != nil && head.Message.Role == "assistant" || head.Type == "message_update" {
			records = append(records, raw)
		}
	}, func(err error) { t.Error(err) })
	defer unsubscribe()
	if _, err := session.Send(t.Context(), "hello"); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	return records
}

func readFauxRPCOracle(t testing.TB) fauxRPCOracle {
	t.Helper()
	data, err := os.ReadFile(fauxRPCOraclePath)
	if err != nil {
		t.Fatal(err)
	}
	var oracle fauxRPCOracle
	if err := json.Unmarshal(data, &oracle); err != nil {
		t.Fatal(err)
	}
	return oracle
}

// TestFauxRPCObservation replays the RPC records of Pi's real `--mode rpc` process for the faux provider through the RPC serializer.
// upstream: packages/coding-agent/src/modes/rpc/rpc-mode.ts:354-363; packages/ai/src/providers/faux.ts:streamWithDeltas.
func TestFauxRPCObservation(t *testing.T) {
	oracle := readFauxRPCOracle(t)
	if oracle.Pi != coding.UpstreamVersion {
		t.Fatalf("faux oracle pins Pi %s; current Pi %s requires a fresh oracle", oracle.Pi, coding.UpstreamVersion)
	}
	runs := 1
	if os.Getenv("PIG_FAUX_RPC_RUNS") != "" {
		if _, err := fmt.Sscan(os.Getenv("PIG_FAUX_RPC_RUNS"), &runs); err != nil {
			t.Fatal(err)
		}
	}
	for _, result := range oracle.Results {
		t.Run(result.Fixture.Name, func(t *testing.T) {
			t.Parallel()
			var want []json.RawMessage
			for _, entry := range result.RPC[0] {
				if entry.Kind == "serialize" {
					want = append(want, entry.Record)
				}
			}
			if len(want) < 2 {
				t.Fatalf("oracle holds %d assistant records", len(want))
			}
			wantJSON := fauxRPCComparable(t, want)
			for run := range runs {
				got := fauxRPCRecords(t, result.Fixture)
				if gotJSON := fauxRPCComparable(t, got); !bytes.Equal(wantJSON, gotJSON) {
					t.Fatalf("run %d differs from Pi:\n%s", run, gotextdiff.ToUnified("Pi", "Go", string(wantJSON), myers.ComputeEdits(span.URIFromPath(t.Name()), string(wantJSON), string(gotJSON))))
				}
			}
		})
	}
}
