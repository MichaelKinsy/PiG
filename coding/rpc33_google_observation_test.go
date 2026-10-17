package coding

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
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

var googleObservationOutput = flag.String("rpc33-google-output", "", "directory for complete Google observations and diffs; each execution creates a separate child")

const googleObservationDir = "testdata/rpc33-observation/providers/google-generative-ai"

type googleObservationInputs struct {
	PiVersion    string `json:"piVersion"`
	GenAIVersion string `json:"genaiVersion"`
	Axes         struct {
		Shapes     []string          `json:"shapes"`
		Deliveries []string          `json:"deliveries"`
		Consumers  []string          `json:"consumers"`
		Layers     []json.RawMessage `json:"layers"`
	} `json:"axes"`
	Bodies    map[string][]string `json:"bodies"`
	LaterBody string              `json:"laterBody"`
}

type googleObservationCase struct {
	Shape    string            `json:"shape"`
	Delivery string            `json:"delivery"`
	Consumer string            `json:"consumer"`
	Layers   json.RawMessage   `json:"layers"`
	Records  []json.RawMessage `json:"records"`
}

func (c googleObservationCase) name() string {
	return fmt.Sprintf("%s/%s/%s/%s", strings.Trim(string(c.Layers), `"`), c.Shape, c.Delivery, c.Consumer)
}

var googleGeneratedID = regexp.MustCompile(`_\d{13}_\d+"`)

// TestRPC33GoogleObservationMatrix compares what each consumer observes of Pi's google-generative-ai stream: every delivered event with its partial message, for buffered, pending and split body delivery, through zero, one and two lazyStream layers and the Model Runtime, for plain, held, result-only, cancelling and Agent consumers.
// upstream: packages/ai/src/api/google-generative-ai.ts:59-268; packages/ai/src/utils/event-stream.ts:44-91; packages/agent/src/agent-loop.ts:408-453
// testdata/rpc33-observation/providers/google-generative-ai/probe.mjs is the exact-Pi oracle.
func TestRPC33GoogleObservationMatrix(t *testing.T) {
	for _, key := range []string{"HOME", "PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR"} {
		t.Setenv(key, t.TempDir())
	}
	var inputs googleObservationInputs
	readObservationJSON(t, googleObservationDir+"/inputs.json", &inputs)
	if inputs.PiVersion != UpstreamVersion {
		t.Fatalf("Google oracle pins Pi %s; current Pi %s requires a fresh oracle", inputs.PiVersion, UpstreamVersion)
	}
	var oracle struct {
		Cases []googleObservationCase `json:"cases"`
	}
	readObservationJSON(t, googleObservationDir+"/pi.json", &oracle)
	var cases []googleObservationCase
	for _, layers := range inputs.Axes.Layers {
		for _, shape := range inputs.Axes.Shapes {
			for _, delivery := range inputs.Axes.Deliveries {
				for _, consumer := range inputs.Axes.Consumers {
					cases = append(cases, googleObservationCase{Shape: shape, Delivery: delivery, Consumer: consumer, Layers: layers})
				}
			}
		}
	}
	if len(oracle.Cases) != len(cases) {
		t.Fatalf("oracle has %d cases; Cartesian axes require %d", len(oracle.Cases), len(cases))
	}
	dir := t.TempDir()
	if *googleObservationOutput != "" {
		if err := os.MkdirAll(*googleObservationOutput, 0o700); err != nil {
			t.Fatal(err)
		}
		var err error
		dir, err = os.MkdirTemp(*googleObservationOutput, "run-")
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("complete observations and diffs: %s", dir)
	for i, c := range cases {
		if c.name() != oracle.Cases[i].name() {
			t.Fatalf("oracle[%d] = %s; axis order requires %s", i, oracle.Cases[i].name(), c.name())
		}
		t.Run(c.name(), func(t *testing.T) {
			t.Parallel()
			got := runGoogleObservationCase(t, c, &inputs)
			want := googleOracleComparable(t, oracle.Cases[i].Records)
			have := googleOracleComparable(t, got.Records)
			if !bytes.Equal(want, have) {
				name := strings.ReplaceAll(c.name(), "/", "-")
				diff := fmt.Sprint(gotextdiff.ToUnified("Pi", "Go", string(want), myers.ComputeEdits(span.URIFromPath(name), string(want), string(have))))
				if err := os.WriteFile(filepath.Join(dir, name+".diff"), []byte(diff), 0o600); err != nil {
					t.Fatal(err)
				}
				t.Errorf("complete observation mismatch: %s/%s.diff\n%s", dir, name, diff)
			}
		})
	}
}

// googleOracleComparable removes the oracle's own clocks (epoch, seq, tick and round describe Node's schedule), the wall clock and the generated tool-call id's timestamp and counter.
func googleOracleComparable(t *testing.T, records []json.RawMessage) []byte {
	t.Helper()
	cleaned := make([]any, 0, len(records))
	for _, record := range records {
		var value map[string]any
		decoder := json.NewDecoder(bytes.NewReader(record))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"epoch", "seq", "tick", "round"} {
			delete(value, key)
		}
		cleaned = append(cleaned, value)
	}
	// The matrix drops the oracle's ticks, and whether an earlier record shows the final message's durationMs (Pi 1.1.0, #10549)
	// depends on which tick the consumer reads it in, so durationMs is not compared here. The Google tick order itself is
	// compared in ai (TestGoogleProviderConsumerMatchesNodeTickOrder), and durationMs presence in the rpc, json and
	// observation scenarios for the other providers.
	stripGoogleDuration(cleaned)
	data := observationComparable(t, cleaned)
	return googleGeneratedID.ReplaceAll(data, []byte(`_T_N"`))
}

// googleBodyParts is the fixture server's split: the bytes that leave with the headers and the bytes withheld until release (probe.mjs parts()).
func googleBodyParts(records []string, delivery string) (string, string) {
	all := strings.Join(records, "")
	if delivery != "split" {
		return "", all
	}
	if len(records) > 1 {
		return records[0], strings.Join(records[1:], "")
	}
	cut := len(all) / 2
	return all[:cut], all[cut:]
}

func runGoogleObservationCase(t *testing.T, c googleObservationCase, inputs *googleObservationInputs) googleObservationCase {
	t.Helper()
	// This deadline detects a missing start/body handshake; it never releases the body or changes successful scheduling. A timed-out row remains a failure.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	first, rest := googleBodyParts(inputs.Bodies[c.Shape], c.Delivery)
	released := make(chan struct{})
	release := sync.OnceFunc(func() { close(released) })
	defer release()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if c.Delivery == "buffered" {
			_, _ = io.WriteString(w, first+rest)
			return
		}
		// Headers, and for split delivery the first bytes, leave in one write; the rest waits for the release.
		if first != "" {
			_, _ = io.WriteString(w, first)
		}
		w.(http.Flusher).Flush()
		if c.Consumer == "result-only" {
			// Nothing observes start, so the body follows the headers.
			release()
		}
		select {
		case <-released:
		case <-ctx.Done():
			return
		}
		if _, err := io.WriteString(w, rest); err != nil && ctx.Err() == nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	baseURL := server.URL + "/v1beta"
	model := &ai.Model{ID: "probe", DisplayName: "probe", Input: []string{"text"}, ProviderMeta: ai.ProviderMetadata{ProviderID: "probe-provider", API: ai.APIGoogleGenerativeAI, BaseURL: baseURL}, Capabilities: ai.ModelCapabilities{ContextWindow: 4096, MaxOutputTokens: 256}}
	var runtime *ModelRuntime
	if string(c.Layers) == `"runtime"` {
		config := fmt.Sprintf(`{"providers":{"probe-provider":{"apiKey":"test","baseUrl":%q,"api":%q,"models":[{"id":"probe","name":"probe","reasoning":false,"input":["text"],"contextWindow":4096,"maxTokens":256,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}}]}}}`, baseURL, ai.APIGoogleGenerativeAI)
		services, _ := nativeCompatServices(t, config, nil)
		runtime = services.ModelRuntime()
		model = runtime.GetModel("probe-provider", "probe")
		if model == nil {
			t.Error("configured runtime model absent")
			return c
		}
	}
	provider := ai.NewGoogleProvider(ai.GoogleConfig{BaseURL: baseURL, APIVersion: "", APIKey: "test", Model: "probe", ProviderID: "probe-provider"})
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
	// Buffered delivery has nothing to release; pending and split delivery release at the first start observation.
	releaseOnStart := release
	if c.Delivery == "buffered" {
		releaseOnStart = func() {}
	}
	user := agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "probe"}}, Timestamp: 1}}
	held := strings.HasPrefix(c.Consumer, "held")
	if strings.HasSuffix(c.Consumer, "agent") {
		a := mustNewAgent(agent.AgentOptions{Model: model, Tools: []agent.AgentTool{}, StreamFn: streamFn,
			FinishTurn: func(context.Context, agent.AgentTurnContext) (*agent.AgentTurnDecision, error) {
				return &agent.AgentTurnDecision{Action: agent.AgentTurnEnd}, nil
			},
			OnEvent: func(event agent.AgentEvent) {
				wire := observationAgentEvent(t, event)
				if wire == nil {
					return
				}
				record("entry", "event", wire)
				if start, ok := event.(agent.MessageStartEvent); ok && start.Message.Assistant != nil {
					releaseOnStart()
					if held {
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
		consumeErr := ai.RunStreamContinuation(ai.WithStreamContinuations(ctx), func(consumer *ai.StreamObservation) error {
			var err error
			response, err = streamFn(consumer.Context(ctx), model, ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: user.User.Content, Timestamp: 1}}}), ai.StreamOptions{})
			if err != nil {
				return err
			}
			if c.Consumer == "result-only" {
				record("result-before-iteration", "result", response.Result())
			}
			var terminal *ai.AssistantMessage
			// Cancellation belongs to the producer, not this consumer's iterator.
			for event := range response.Events(consumer.Context(context.WithoutCancel(ctx))) {
				record("entry", "event", event)
				switch event := event.(type) {
				case ai.StartEvent:
					if c.Consumer == "cancel" {
						cancel()
					}
					releaseOnStart()
					if held {
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
			return nil
		})
		if consumeErr != nil {
			t.Errorf("stream: %v", consumeErr)
			return c
		}
	}
	if errors := ctx.Err(); errors != nil && c.Consumer != "cancel" {
		t.Errorf("observation handshake did not complete: %v", errors)
	}
	if requests.Load() != 1 {
		t.Errorf("provider requests = %d; Pi finishTurn ends after the single response", requests.Load())
	}
	return c
}

func stripGoogleDuration(value any) {
	switch value := value.(type) {
	case []any:
		for _, item := range value {
			stripGoogleDuration(item)
		}
	case map[string]any:
		delete(value, "durationMs")
		for _, item := range value {
			stripGoogleDuration(item)
		}
	}
}
