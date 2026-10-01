package coding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

var observationOutput = flag.String("rpc33-observation-output", "", "directory for complete RPC33 observations and diffs; each execution creates a separate child")

type observationInputs struct {
	PiVersion     string `json:"piVersion"`
	OpenAIVersion string `json:"openaiVersion"`
	Axes          struct {
		APIs   []ai.API          `json:"apis"`
		Shapes []string          `json:"shapes"`
		Layers []json.RawMessage `json:"layers"`
		Modes  []string          `json:"modes"`
	} `json:"axes"`
	Bodies map[ai.API]map[string]string `json:"bodies"`
}

type observationCase struct {
	API           ai.API            `json:"api"`
	Shape         string            `json:"shape"`
	Mode          string            `json:"mode"`
	Layers        json.RawMessage   `json:"layers"`
	Records       []json.RawMessage `json:"records"`
	NativeRecords []json.RawMessage `json:"-"`
}

func (c observationCase) name() string {
	return fmt.Sprintf("%s/%s/%s/%s", strings.Trim(string(c.Layers), `"`), c.API, c.Shape, c.Mode)
}

// TestRPC33ObservationMatrix compares entire observations, not event summaries.
// upstream: packages/ai/src/utils/event-stream.ts:44-91 (queued references)
// upstream: packages/agent/src/agent-loop.ts:408-453 (shallow copies and awaited sinks)
// testdata/rpc33-observation/probe.mjs is the exact-Pi oracle; inputs.json is its body/axis export.
func TestRPC33ObservationMatrix(t *testing.T) {
	runObservationMatrix(t, "testdata/rpc33-observation")
}

// TestRPC33ObservationMatrixAzure replays the Azure OpenAI Responses oracle: packages/ai/src/api/azure-openai-responses.ts,
// exported by testdata/rpc33-observation/providers/azure-openai-responses/probe.mjs.
func TestRPC33ObservationMatrixAzure(t *testing.T) {
	runObservationMatrix(t, "testdata/rpc33-observation/providers/azure-openai-responses")
}

func runObservationMatrix(t *testing.T, oracleDir string) {
	t.Helper()
	for _, key := range []string{"HOME", "PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR"} {
		t.Setenv(key, t.TempDir())
	}
	var inputs observationInputs
	readObservationJSON(t, filepath.Join(oracleDir, "inputs.json"), &inputs)
	if inputs.PiVersion != UpstreamVersion {
		t.Fatalf("RPC33 oracle pins Pi %s; current Pi %s requires a fresh oracle", inputs.PiVersion, UpstreamVersion)
	}
	t.Logf("oracle: Pi %s, OpenAI %s", inputs.PiVersion, inputs.OpenAIVersion)
	var oracle []observationCase
	readObservationJSON(t, filepath.Join(oracleDir, "pi.json"), &oracle)
	var cases []observationCase
	for _, layers := range inputs.Axes.Layers {
		for _, api := range inputs.Axes.APIs {
			for _, shape := range inputs.Axes.Shapes {
				for _, mode := range inputs.Axes.Modes {
					cases = append(cases, observationCase{API: api, Shape: shape, Mode: mode, Layers: layers, Records: []json.RawMessage{}})
				}
			}
		}
	}
	if len(oracle) != len(cases) {
		t.Fatalf("oracle has %d cases; Cartesian axes require %d", len(oracle), len(cases))
	}
	dir := t.TempDir()
	if *observationOutput != "" {
		if err := os.MkdirAll(*observationOutput, 0o700); err != nil {
			t.Fatal(err)
		}
		var err error
		dir, err = os.MkdirTemp(*observationOutput, "run-")
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("complete observations and diffs: %s", dir)
	got := make([]observationCase, len(cases))
	// A child owns one slot. Cleanup runs after all parallel children have joined.
	t.Cleanup(func() { writeObservationJSON(t, filepath.Join(dir, "go.json"), got) })
	for i, c := range cases {
		if c.name() != oracle[i].name() {
			t.Fatalf("oracle[%d] = %s; axis order requires %s", i, oracle[i].name(), c.name())
		}
		t.Run(c.name(), func(t *testing.T) {
			t.Parallel()
			got[i] = runObservationCase(t, c, inputs.Bodies[c.API][c.Shape])
			name := strings.ReplaceAll(c.name(), "/", "-")
			writeObservationJSON(t, filepath.Join(dir, name+".go.json"), got[i])
			writeObservationJSON(t, filepath.Join(dir, name+".native.json"), got[i].NativeRecords)
			wantJSON := observationComparable(t, oracle[i])
			gotJSON := observationComparable(t, got[i])
			if !bytes.Equal(wantJSON, gotJSON) {
				diff := fmt.Sprint(gotextdiff.ToUnified("Pi", "Go", string(wantJSON), myers.ComputeEdits(span.URIFromPath(name), string(wantJSON), string(gotJSON))))
				if err := os.WriteFile(filepath.Join(dir, name+".diff"), []byte(diff), 0o600); err != nil {
					t.Fatal(err)
				}
				t.Errorf("complete observation mismatch: %s/%s.diff\n%s", dir, name, diff)
			}
		})
	}
}

func TestRPC33ObservationComparator(t *testing.T) {
	base := json.RawMessage(`{"message":{"role":"assistant","timestamp":42,"stopReason":"pending","content":[{"type":"toolCall","arguments":{"timestamp":3,"role":"assistant"},"partialArgs":"{","streamIndex":0}]},"delta":"one","user":{"role":"user","timestamp":1}}`)
	for _, change := range []struct {
		name, from, to string
		equal          bool
	}{
		{"clock-value", `"timestamp":42`, `"timestamp":43`, true},
		{"clock-null", `"timestamp":42`, `"timestamp":null`, false},
		{"clock-string", `"timestamp":42`, `"timestamp":"42"`, false},
		{"clock-missing", `"timestamp":42,`, ``, false},
		{"user-clock", `"timestamp":1`, `"timestamp":2`, false},
		{"argument-clock", `"timestamp":3`, `"timestamp":4`, false},
		{"scratch-deleted", `,"partialArgs":"{"`, ``, false},
		{"scratch-null", `"partialArgs":"{"`, `"partialArgs":null`, false},
		{"index-type", `"streamIndex":0`, `"streamIndex":"0"`, false},
		{"delta", `"delta":"one"`, `"delta":"two"`, false},
		{"stop", `"pending"`, `"toolUse"`, false},
	} {
		t.Run(change.name, func(t *testing.T) {
			changed := json.RawMessage(strings.Replace(string(base), change.from, change.to, 1))
			if bytes.Equal(base, changed) {
				t.Fatal("mutation did not change input")
			}
			if equal := bytes.Equal(observationComparable(t, base), observationComparable(t, changed)); equal != change.equal {
				t.Fatalf("comparison equal = %v; want %v", equal, change.equal)
			}
		})
	}
	for _, pair := range [][2]string{{`[]`, `null`}, {`[1,2]`, `[2,1]`}, {`[{}]`, `[]`}, {`{"a":0}`, `{}`}} {
		if bytes.Equal(observationComparable(t, json.RawMessage(pair[0])), observationComparable(t, json.RawMessage(pair[1]))) {
			t.Errorf("comparator erased %s versus %s", pair[0], pair[1])
		}
	}
}

func readObservationJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, value); err != nil {
		t.Fatal(err)
	}
}

func writeObservationJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Error(err)
		return
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Error(err)
	}
}

func observationCopy(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Error(err)
		return json.RawMessage(`null`)
	}
	return data
}

func observationComparable(t *testing.T, value any) []byte {
	t.Helper()
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(observationCopy(t, value)))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	canonicalizeObservationClocks(decoded)
	data, err := json.MarshalIndent(decoded, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(data, '\n')
}

func canonicalizeObservationClocks(value any) {
	switch value := value.(type) {
	case []any:
		for _, item := range value {
			canonicalizeObservationClocks(item)
		}
	case map[string]any:
		// Only message timestamps are clocks. In particular do not change numbers
		// in delta strings, tool arguments, usage, or the fixed user timestamp.
		if value["role"] == "assistant" || value["role"] == "toolResult" {
			if _, ok := value["timestamp"].(json.Number); ok {
				value["timestamp"] = json.Number("0")
			}
			return
		}
		for key, item := range value {
			if key != "arguments" && key != "args" && key != "details" {
				canonicalizeObservationClocks(item)
			}
		}
	}
}

func runObservationCase(t *testing.T, c observationCase, body string) observationCase {
	t.Helper()
	// This deadline detects a missing start/body handshake; it never releases the
	// body or changes successful scheduling. A timed-out row remains a failure.
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	first := make(chan struct{})
	release := sync.OnceFunc(func() { close(first) })
	defer release()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Error(err)
			return
		}
		if err := r.Body.Close(); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if strings.HasPrefix(c.Mode, "delayed") || c.Mode == "cancel" {
			w.(http.Flusher).Flush()
			select {
			case <-first:
			case <-ctx.Done():
				return
			}
		}
		if _, err := io.WriteString(w, body); err != nil && ctx.Err() == nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	model := &ai.Model{ID: "probe", DisplayName: "probe", Input: []string{"text"}, ProviderMeta: ai.ProviderMetadata{ProviderID: "probe-provider", API: c.API, BaseURL: server.URL + "/v1"}, Capabilities: ai.ModelCapabilities{ContextWindow: 4096, MaxOutputTokens: 256}}
	var runtime *ModelRuntime
	if string(c.Layers) == `"runtime"` {
		config := fmt.Sprintf(`{"providers":{"probe-provider":{"apiKey":"test","baseUrl":%q,"api":%q,"models":[{"id":"probe","name":"probe","reasoning":false,"input":["text"],"contextWindow":4096,"maxTokens":256,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}}]}}}`, model.ProviderMeta.BaseURL, c.API)
		services, _ := nativeCompatServices(t, config, nil)
		runtime = services.ModelRuntime()
		model = runtime.GetModel("probe-provider", "probe")
		if model == nil {
			t.Error("configured runtime model absent")
			return c
		}
	}
	var provider ai.Provider
	switch c.API {
	case ai.APIOpenAICompletions:
		provider = ai.NewOpenAIProvider(ai.OpenAIConfig{BaseURL: server.URL + "/v1", APIKey: "test", Model: "probe", ProviderID: "probe-provider", ModelMetadata: model})
	case ai.APIOpenAIResponses:
		provider = ai.NewOpenAIResponsesProvider(ai.OpenAIResponsesConfig{BaseURL: server.URL + "/v1", APIKey: "test", Model: "probe", ProviderID: "probe-provider", ModelMetadata: model})
	case ai.APIAzureOpenAIResponses:
		provider = ai.NewAzureOpenAIResponsesProvider(ai.AzureOpenAIResponsesConfig{BaseURL: server.URL + "/v1", APIKey: "test", Model: "probe", ProviderID: "probe-provider", ModelMetadata: model})
	default:
		t.Errorf("unsupported oracle API %q", c.API)
		return c
	}
	defer func() {
		if err := provider.Close(); err != nil {
			t.Error(err)
		}
	}()
	var response *ai.AssistantMessageEventStream
	streamFn := func(requestCtx context.Context, m *ai.Model, transcript ai.TranscriptContext, opts ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		opts.MaxRetries = new(0)
		if runtime != nil {
			response = runtime.StreamSimple(requestCtx, m, ai.Context{Messages: transcript.Messages()}, opts)
			return response, nil
		}
		opts.APIKey = "test"
		makeStream := func(ctx context.Context) (*ai.AssistantMessageEventStream, error) {
			return provider.Stream(ctx, transcript, opts)
		}
		var layers int
		if err := json.Unmarshal(c.Layers, &layers); err != nil {
			return nil, err
		}
		for range layers {
			inner := makeStream
			makeStream = func(ctx context.Context) (*ai.AssistantMessageEventStream, error) {
				return ai.LazyStream(ctx, m, inner), nil
			}
		}
		var err error
		response, err = makeStream(requestCtx)
		return response, err
	}
	record := func(at, field string, value any) {
		c.Records = append(c.Records, observationCopy(t, map[string]any{"at": at, field: value}))
	}
	user := agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "probe"}}, Timestamp: 1}}
	if strings.HasSuffix(c.Mode, "agent") {
		a := agent.NewAgent(agent.AgentOptions{Model: model, Tools: []agent.AgentTool{}, StreamFn: streamFn,
			FinishTurn: func(context.Context, agent.AgentTurnContext) (*agent.AgentTurnDecision, error) {
				return &agent.AgentTurnDecision{Action: agent.AgentTurnEnd}, nil
			},
			OnEvent: func(event agent.AgentEvent) {
				c.NativeRecords = append(c.NativeRecords, observationCopy(t, map[string]any{"type": fmt.Sprintf("%T", event), "event": event}))
				wire := observationAgentEvent(t, event)
				if wire == nil {
					return
				}
				record("entry", "event", wire)
				if start, ok := event.(agent.MessageStartEvent); ok && start.Message.Assistant != nil {
					release()
					if strings.HasPrefix(c.Mode, "held") || strings.HasPrefix(c.Mode, "delayed-held") {
						if _, err := response.ResultContext(ctx); err != nil {
							t.Error(err)
							return
						}
						record("after-result", "event", observationAgentEvent(t, event))
					}
				}
			},
		})
		if _, err := a.SendMessages(ctx, []agent.AgentMessage{user}); err != nil {
			t.Errorf("Agent: %v", err)
		}
	} else {
		// Pi's probe starts its for-await in the job that called stream(), so no socket completion can run between them. The consumer holds its continuation across the stream call and the first iterator read, as the Agent does (agent-loop.ts:402-411).
		stopped := false
		consumeErr := ai.RunStreamContinuation(ai.WithStreamContinuations(ctx), func(consumer *ai.StreamObservation) error {
			var err error
			response, err = streamFn(consumer.Context(ctx), model, ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: user.User.Content, Timestamp: 1}}}), ai.StreamOptions{})
			if err != nil {
				return err
			}
			if c.Mode == "result-only" {
				record("result-before-iteration", "result", response.Result())
			}
			var terminal *ai.AssistantMessage
			// Cancellation belongs to the producer, not this consumer's iterator.
			for event := range response.Events(consumer.Context(context.WithoutCancel(ctx))) {
				record("entry", "event", event)
				switch event := event.(type) {
				case ai.StartEvent:
					if c.Mode == "cancel" {
						cancel()
					}
					release()
					if c.Mode == "held-direct" {
						response.Result()
						record("after-result", "event", event)
					}
				case ai.DoneEvent:
					terminal = event.Message
				case ai.ErrorEvent:
					terminal = event.Error
				}
			}
			result := response.Result()
			record("result", "result", result)
			if result != terminal || response.Result() != result {
				t.Error("terminal/result pointer identity changed")
			}
			if result == nil {
				t.Error("terminal result is nil")
				stopped = true
				return nil
			}
			wantStop := ai.StopReasonStop
			if c.Shape == "tool" {
				wantStop = ai.StopReasonToolUse
			}
			if c.Mode == "cancel" {
				wantStop = ai.StopReasonAborted
			}
			if result.StopReason != wantStop {
				t.Errorf("stopReason = %s; want %s", result.StopReason, wantStop)
			}
			return nil
		})
		if consumeErr != nil {
			t.Errorf("stream: %v", consumeErr)
			return c
		}
		if stopped {
			return c
		}
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || (ctx.Err() != nil && c.Mode != "cancel") {
		t.Errorf("observation handshake did not complete: %v", ctx.Err())
	}
	if requests.Load() != 1 {
		t.Errorf("provider requests = %d; Pi finishTurn ends after the single response", requests.Load())
	}
	return c
}

// Go's AgentEvent is not JSON-tagged. Project only the upstream AgentEvent union,
// not RPC's reduced message_update. Message and provider-event marshalers stay real.
func observationAgentEvent(t *testing.T, event agent.AgentEvent) any {
	t.Helper()
	switch event := event.(type) {
	case agent.AgentStartEvent:
		return map[string]any{"type": "agent_start"}
	case agent.TurnStartEvent:
		return map[string]any{"type": "turn_start"}
	case agent.AgentEndEvent:
		return map[string]any{"type": "agent_end", "messages": event.Messages}
	case agent.MessageStartEvent:
		return map[string]any{"type": "message_start", "message": event.Message}
	case agent.MessageUpdateEvent:
		return map[string]any{"type": "message_update", "message": event.Message, "assistantMessageEvent": event.AssistantMessageEvent}
	case agent.MessageEndEvent:
		return map[string]any{"type": "message_end", "message": event.Message}
	case agent.TurnEndEvent:
		results := append([]agent.ToolResultMessage{}, event.ToolResults...)
		return map[string]any{"type": "turn_end", "message": event.Message, "toolResults": results}
	case agent.ToolExecutionStartEvent:
		return map[string]any{"type": "tool_execution_start", "toolCallId": event.ToolCallID, "toolName": event.ToolName, "args": event.Args}
	case agent.ToolExecutionEndEvent:
		result := map[string]any{"content": event.Result.Content}
		if event.Result.Details != nil {
			result["details"] = event.Result.Details
		}
		if event.Result.IsError {
			result["isError"] = true
		}
		if event.Result.Usage != nil {
			result["usage"] = event.Result.Usage
		}
		// upstream: agent-loop.ts:912-919 emits `isError: finalized.isError` beside `result`; result.isError exists only when the tool returned it.
		return map[string]any{"type": "tool_execution_end", "toolCallId": event.ToolCallID, "toolName": event.ToolName, "result": result, "isError": event.IsError}
	default:
		t.Errorf("unexpected AgentEvent %T", event)
		return map[string]any{"unexpected": reflect.TypeOf(event).String(), "value": event}
	}
}
