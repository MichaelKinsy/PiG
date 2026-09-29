package ai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

const googleRealSocketOracleDir = "../coding/testdata/rpc33-observation/providers/google-generative-ai"

// TestGoogleUndiciConstantsMatchProbe ties the Node constants the model uses to undici.mjs's recorded output. The probe measures them against the real undici and Web Streams of Node 24.19.0.
func TestGoogleUndiciConstantsMatchProbe(t *testing.T) {
	var probe struct {
		Node    string `json:"node"`
		Results []struct {
			Delivery           string `json:"delivery"`
			FetchResolvedRound int    `json:"fetchResolvedRound"`
			FirstRead          struct {
				From   string `json:"from"`
				Rounds int    `json:"rounds"`
			} `json:"firstRead"`
			JSON int `json:"json"`
		} `json:"results"`
	}
	data, err := os.ReadFile(googleRealSocketOracleDir + "/undici.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		t.Fatal(err)
	}
	if probe.Node != "v24.19.0" {
		t.Fatalf("undici.json was recorded on %s; the model is pinned to Node 24.19.0", probe.Node)
	}
	seen := 0
	for _, result := range probe.Results {
		switch result.Delivery {
		case "buffered", "split":
			seen++
			if result.FetchResolvedRound != googleUndiciFetchRounds || result.FirstRead.From != "issue" || result.FirstRead.Rounds != googleUndiciBufferedReadRounds {
				t.Errorf("%s: probe fetch=%d read=%+v; model fetch=%d buffered read=%d", result.Delivery, result.FetchResolvedRound, result.FirstRead, googleUndiciFetchRounds, googleUndiciBufferedReadRounds)
			}
		case "pending":
			seen++
			if result.FetchResolvedRound != googleUndiciFetchRounds || result.FirstRead.From != "socket-data" || result.FirstRead.Rounds != googleUndiciPendingReadRounds {
				t.Errorf("pending: probe fetch=%d read=%+v; model fetch=%d pending read=%d", result.FetchResolvedRound, result.FirstRead, googleUndiciFetchRounds, googleUndiciPendingReadRounds)
			}
		case "":
			if result.JSON != googleUndiciJSONRounds {
				t.Errorf("probe Response.json settles in %d rounds; model %d", result.JSON, googleUndiciJSONRounds)
			}
		}
	}
	if seen != 3 {
		t.Fatalf("undici.json has %d delivery results; want buffered, pending and split", seen)
	}
}

// TestGoogleProviderRoundsMatchNodeRealSockets replays the real-socket oracle (Pi over undici over TCP) through the Go provider over a real HTTP connection and requires that a plain consumer sees each event in the same (socket read, round) with the same partial message. Rounds after the end of the body are not compared: Node delivers it from a process.nextTick callback that the oracle's round chain starves, which the oracle records as round 160.
// upstream: packages/ai/src/api/google-generative-ai.ts:59-268; @google/genai 2.21.0 dist/node/index.mjs:13780-13860
func TestGoogleProviderRoundsMatchNodeRealSockets(t *testing.T) {
	var inputs struct {
		Bodies map[string][]string `json:"bodies"`
	}
	readGoogleOracleJSON(t, googleRealSocketOracleDir+"/inputs.json", &inputs)
	var oracle struct {
		Cases []struct {
			Shape    string            `json:"shape"`
			Delivery string            `json:"delivery"`
			Consumer string            `json:"consumer"`
			Layers   json.RawMessage   `json:"layers"`
			Records  []json.RawMessage `json:"records"`
			Pushes   []json.RawMessage `json:"pushes"`
		} `json:"cases"`
	}
	readGoogleOracleJSON(t, googleRealSocketOracleDir+"/pi.json", &oracle)
	const roundCap = 160
	compared := 0
	for _, c := range oracle.Cases {
		if string(c.Layers) != "0" || c.Consumer != "direct" {
			continue
		}
		compared++
		t.Run(c.Shape+"/"+c.Delivery, func(t *testing.T) {
			t.Parallel()
			type stamp struct {
				Type       string
				Epoch      int
				Round      int
				StopReason string
				Blocks     int
				Total      int
			}
			var want []stamp
			for _, raw := range c.Records {
				var record struct {
					At    string `json:"at"`
					Epoch int    `json:"epoch"`
					Round int    `json:"round"`
					Event struct {
						Type    string `json:"type"`
						Partial *struct {
							StopReason string            `json:"stopReason"`
							Content    []json.RawMessage `json:"content"`
							Usage      struct {
								TotalTokens int `json:"totalTokens"`
							} `json:"usage"`
						} `json:"partial"`
						Message *struct {
							StopReason string            `json:"stopReason"`
							Content    []json.RawMessage `json:"content"`
							Usage      struct {
								TotalTokens int `json:"totalTokens"`
							} `json:"usage"`
						} `json:"message"`
					} `json:"event"`
				}
				if err := json.Unmarshal(raw, &record); err != nil {
					t.Fatal(err)
				}
				if record.At != "entry" || record.Round >= roundCap {
					continue
				}
				message := record.Event.Partial
				if message == nil {
					message = record.Event.Message
				}
				want = append(want, stamp{Type: record.Event.Type, Epoch: record.Epoch, Round: record.Round, StopReason: message.StopReason, Blocks: len(message.Content), Total: message.Usage.TotalTokens})
			}

			var wantPushes []stamp
			for _, raw := range c.Pushes {
				var record struct {
					Epoch int `json:"epoch"`
					Round int `json:"round"`
					Event struct {
						Type    string `json:"type"`
						Partial *struct {
							StopReason string            `json:"stopReason"`
							Content    []json.RawMessage `json:"content"`
							Usage      struct {
								TotalTokens int `json:"totalTokens"`
							} `json:"usage"`
						} `json:"partial"`
					} `json:"event"`
				}
				if err := json.Unmarshal(raw, &record); err != nil {
					t.Fatal(err)
				}
				if record.Round >= roundCap || record.Event.Partial == nil {
					continue
				}
				partial := record.Event.Partial
				wantPushes = append(wantPushes, stamp{Type: record.Event.Type, Epoch: record.Epoch, Round: record.Round, StopReason: partial.StopReason, Blocks: len(partial.Content), Total: partial.Usage.TotalTokens})
			}

			first, rest := googleRealSocketParts(inputs.Bodies[c.Shape], c.Delivery)
			released := make(chan struct{})
			release := sync.OnceFunc(func() { close(released) })
			defer release()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				if c.Delivery == "buffered" {
					_, _ = io.WriteString(w, first+rest)
					return
				}
				if first != "" {
					_, _ = io.WriteString(w, first)
				}
				w.(http.Flusher).Flush()
				select {
				case <-released:
				case <-r.Context().Done():
					return
				}
				_, _ = io.WriteString(w, rest)
			}))
			defer server.Close()

			ctx, executor := withContinuationExecutor(WithStreamContinuations(t.Context()))
			clock := &googleRoundClock{executor: executor, cap: roundCap, segment: 0}
			var gotPushes []stamp
			var stream *AssistantMessageEventStream
			executor.trace = &executorTrace{external: clock.startSegment, push: func(pushed *AssistantMessageEventStream, event AssistantMessageEvent) {
				if pushed != stream {
					return
				}
				var partial *AssistantMessage
				switch event := event.(type) {
				case StartEvent:
					partial = event.Partial
				case DoneEvent, ErrorEvent:
					return
				default:
					partial = eventPartial(event)
				}
				// The push carries the provider's live message; Observe reads it as it is at the push.
				live := partial.Observe()
				gotPushes = append(gotPushes, stamp{Type: string(event.EventType()), Epoch: clock.segment, Round: clock.tick, StopReason: string(live.StopReason), Blocks: len(live.Content), Total: live.Usage.TotalTokens})
			}}
			provider := NewGoogleProvider(GoogleConfig{BaseURL: server.URL + "/v1beta", APIVersion: "", APIKey: "test", Model: "probe", ProviderID: "probe-provider"})
			var got []stamp
			err := RunStreamContinuation(ctx, func(observation *StreamObservation) error {
				events := observation.Context(ctx)
				var err error
				stream, err = provider.Stream(events, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("probe")}}}), StreamOptions{})
				if err != nil {
					return err
				}
				for event := range stream.Events(events) {
					var message *AssistantMessage
					switch event := event.(type) {
					case StartEvent:
						message = event.Partial
						if c.Delivery != "buffered" {
							release()
						}
					case DoneEvent:
						message = event.Message
					case ErrorEvent:
						message = event.Error
					default:
						message = eventPartial(event)
					}
					encoded, err := json.Marshal(message)
					if err != nil {
						return err
					}
					var view struct {
						StopReason string `json:"stopReason"`
						Content    []any  `json:"content"`
						Usage      struct {
							TotalTokens int `json:"totalTokens"`
						} `json:"usage"`
					}
					if err := json.Unmarshal(encoded, &view); err != nil {
						return err
					}
					got = append(got, stamp{Type: string(event.EventType()), Epoch: clock.segment, Round: clock.tick, StopReason: view.StopReason, Blocks: len(view.Content), Total: view.Usage.TotalTokens})
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			// The round chain caps at roundCap; events after the end of the body carry the cap, or a later socket read.
			var comparable []stamp
			for _, s := range got {
				if s.Round >= roundCap-1 || s.Epoch > want[len(want)-1].Epoch && s.Type == "done" {
					break
				}
				comparable = append(comparable, s)
			}
			if len(comparable) < len(want) {
				t.Fatalf("got %d comparable events, Node has %d\n got: %+v\nwant: %+v", len(comparable), len(want), got, want)
			}
			for i := range want {
				if comparable[i] != want[i] {
					t.Errorf("event %d differs from Node\n got: %+v\nwant: %+v", i, comparable[i], want[i])
				}
			}
			var comparablePushes []stamp
			for _, s := range gotPushes {
				if s.Round >= roundCap-1 {
					break
				}
				comparablePushes = append(comparablePushes, s)
			}
			if len(comparablePushes) < len(wantPushes) {
				t.Fatalf("got %d comparable pushes, Node has %d\n got: %+v\nwant: %+v", len(comparablePushes), len(wantPushes), gotPushes, wantPushes)
			}
			for i := range wantPushes {
				if comparablePushes[i] != wantPushes[i] {
					t.Errorf("push %d differs from Node\n got: %+v\nwant: %+v", i, comparablePushes[i], wantPushes[i])
				}
			}
		})
	}
	if compared != 12 {
		t.Fatalf("compared %d oracle cases; want 4 shapes x 3 deliveries", compared)
	}
}

func googleRealSocketParts(records []string, delivery string) (string, string) {
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

func readGoogleOracleJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, value); err != nil {
		t.Fatal(err)
	}
}
