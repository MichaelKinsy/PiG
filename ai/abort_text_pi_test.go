package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/bodyreadhook/readbarrier"
)

type abortObservation struct {
	Phase        string   `json:"phase"`
	Types        []string `json:"types"`
	StopReason   string   `json:"stopReason"`
	ErrorMessage string   `json:"errorMessage"`
	Content      []string `json:"content"`
}

func abortGoldenPath() string { return filepath.Join("testdata", "d82-w7", "abort-pi.json") }

func readAbortGolden(t *testing.T) map[string]map[string]abortObservation {
	t.Helper()
	raw, err := os.ReadFile(abortGoldenPath())
	if err != nil {
		t.Fatal(err)
	}
	var golden map[string]map[string]abortObservation
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	return golden
}

// The golden is real Pi 0.87.1 --mode rpc output for an abort at each phase. This proves it is not stale.
func TestAbortGoldenMatchesPi(t *testing.T) {
	t.Parallel()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required for the Pi differential probe: %v", err)
	}
	cli, err := filepath.Abs(filepath.Join("..", "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent", "dist", "cli.js"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cli); err != nil {
		t.Fatalf("the pinned Pi package is required: %v", err)
	}
	out := filepath.Join(t.TempDir(), "abort.json")
	command := exec.CommandContext(t.Context(), node, filepath.Join("testdata", "d82-w7", "abort-probe.mjs"), "all", node, cli, out)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("probe failed: %v\n%s", err, output)
	}
	live, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile(abortGoldenPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(live) != string(golden) {
		t.Fatalf("abort-pi.json is stale; regenerate it with the probe.\nlive:\n%s", live)
	}
}

// abortFirstRecord is the first SSE record abort-probe.mjs sends in its mid-body phase.
func abortFirstRecord(api API) string {
	switch api {
	case APIAnthropicMessages:
		return "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"strict\",\"content\":[],\"stop_reason\":null,\"usage\":{\"input_tokens\":10,\"output_tokens\":1}}}\n\n" +
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"a\"}}\n\n"
	case APIOpenAIResponses:
		return "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"r1\"}}\n\n" +
			"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"m1\",\"role\":\"assistant\",\"status\":\"in_progress\",\"content\":[]}}\n\n" +
			"event: response.content_part.added\ndata: {\"type\":\"response.content_part.added\",\"output_index\":0,\"content_index\":0,\"part\":{\"type\":\"output_text\",\"text\":\"\",\"annotations\":[]}}\n\n" +
			"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"a\"}\n\n"
	default:
		return "data: {\"id\":\"c\",\"model\":\"strict\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"a\"},\"finish_reason\":null}]}\n\n"
	}
}

func abortProvider(api API, baseURL string) Provider {
	switch api {
	case APIAnthropicMessages:
		return NewAnthropicProvider(AnthropicConfig{ProviderID: "p", Model: "strict", BaseURL: baseURL, APIKey: "k"})
	case APIOpenAIResponses:
		return NewOpenAIResponsesProvider(OpenAIResponsesConfig{ProviderID: "p", Model: "strict", BaseURL: baseURL + "/v1", APIKey: "k"})
	default:
		return NewOpenAIProvider(OpenAIConfig{ProviderID: "p", Model: "strict", BaseURL: baseURL + "/v1", APIKey: "k"})
	}
}

// Every abort text below is what Pi prints for the same abort (abort-probe.mjs drives the real CLI): provider-retry.ts:70 "Request aborted" before headers, the SDK/fetch signal reason "This operation was aborted" for a read the abort interrupted, and each provider's own signal check.
func TestProviderAbortTextsMatchPi(t *testing.T) {
	golden := readAbortGolden(t)
	barrier := readbarrier.New(t)
	for _, api := range []API{APIOpenAICompletions, APIOpenAIResponses, APIAnthropicMessages} {
		for _, phase := range []string{"pre-headers", "post-headers", "mid-body"} {
			t.Run(string(api)+"/"+phase, func(t *testing.T) {
				want := golden[string(api)][phase]
				ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
				defer cancel()
				held := make(chan struct{})
				var heldOnce sync.Once
				server := barrier.Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					buffer := make([]byte, 4096)
					for {
						if _, err := r.Body.Read(buffer); err != nil {
							break
						}
					}
					if phase != "pre-headers" {
						w.Header().Set("Content-Type", "text/event-stream")
						w.WriteHeader(http.StatusOK)
						if phase == "mid-body" {
							_, _ = strings.NewReader(abortFirstRecord(api)).WriteTo(w)
						}
						w.(http.Flusher).Flush()
						// Abort only after the client has consumed what was sent and is reading the socket again, as Pi's probe aborts after its timer.
						if !barrier.WaitReading(r.Context(), r.RemoteAddr) {
							return
						}
					}
					heldOnce.Do(func() { close(held) })
					<-r.Context().Done()
				}))
				provider := abortProvider(api, server.URL)
				defer func() { _ = provider.Close() }()
				// Some providers send the request inside Stream, so the abort must not wait for Stream to return.
				go func() {
					select {
					case <-held:
						cancel()
					case <-ctx.Done():
					}
				}()
				stream, err := provider.Stream(ctx, piMessagesTestContext(), StreamOptions{MaxRetries: new(0)})
				if err != nil {
					t.Fatal(err)
				}
				var types []string
				for event := range stream.Events(context.WithoutCancel(ctx)) {
					switch event.EventType() {
					case EventStart, EventDone, EventError:
					default:
						types = append(types, string(event.EventType()))
					}
				}
				result := stream.Result()
				got := abortObservation{Phase: phase, Types: types, StopReason: string(result.StopReason), ErrorMessage: result.ErrorMessage, Content: []string{}}
				for _, block := range result.Content {
					switch block := block.(type) {
					case TextContent:
						got.Content = append(got.Content, block.Text)
					default:
						got.Content = append(got.Content, string(block.contentType()))
					}
				}
				if want.Types == nil {
					want.Types = []string{}
				}
				if got.Types == nil {
					got.Types = []string{}
				}
				if want.Content == nil {
					want.Content = []string{}
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("abort differs from Pi\n got: %+v\nwant: %+v", got, want)
				}
			})
		}
	}
}

type abortAfterReader struct {
	frames []string
	index  int
	// atRead runs before read i; it returns a non-nil error to fail that read.
	atRead func(int) error
}

func (r *abortAfterReader) Read(p []byte) (int, error) {
	if r.atRead != nil {
		if err := r.atRead(r.index); err != nil {
			return 0, err
		}
	}
	if r.index == len(r.frames) {
		return 0, io.EOF
	}
	n := copy(p, r.frames[r.index])
	r.index++
	return n, nil
}

// anthropic-messages.ts:iterateSseMessages checks the signal before every read (:422-424) and again after the loop (:791-793). A read the abort interrupts rejects with the signal's reason, which the SDK's fetch body reports as "This operation was aborted" (verified against real Pi in TestProviderAbortTextsMatchPi).
func TestAnthropicAbortAtParserReadBoundaries(t *testing.T) {
	prefix := abortFirstRecord(APIAnthropicMessages)
	done := "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	for _, boundary := range []struct{ name, want string }{
		{"next-event", "Request was aborted"},
		{"interrupted-read", "This operation was aborted"},
		{"after-last-read", "Request was aborted"},
	} {
		t.Run(boundary.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			reader := &abortAfterReader{}
			switch boundary.name {
			case "next-event":
				// The abort lands while buffered records are still being handled.
				reader.frames = []string{prefix, done}
				reader.atRead = func(i int) error {
					if i == 1 {
						cancel()
					}
					return nil
				}
			case "interrupted-read":
				reader.frames = []string{prefix}
				reader.atRead = func(i int) error {
					if i == 1 {
						cancel()
						return fetchAbortError(ctx)
					}
					return nil
				}
			default:
				// The stream ends normally, then the signal is found aborted.
				reader.frames = []string{prefix, done}
				reader.atRead = func(i int) error {
					if i == 2 {
						cancel()
					}
					return nil
				}
			}
			builder := newAssistantStreamBuilder(ctx, APIAnthropicMessages, "p", "strict")
			(&anthropicProvider{cfg: AnthropicConfig{ProviderID: "p", Model: "strict"}}).parseAnthropicSSE(ctx, reader, builder, anthropicStreamNames{})
			result := builder.stream.Result()
			if result.StopReason != StopReasonAborted || result.ErrorMessage != boundary.want {
				t.Errorf("stop = %q, error = %q; want aborted / %q", result.StopReason, result.ErrorMessage, boundary.want)
			}
		})
	}
}
