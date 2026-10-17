//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// The Node oracle (coding/testdata/rpc33-observation/providers/bedrock-converse-stream/probe.mjs) drives Pi's real bedrock-converse-stream pipeline against a loopback server and records, per delivery epoch, the microtask job at which every consumer record was made. TestBedrockObservationMatchesPi replays the direct and result-only consumers through PiG's provider over HTTP/1 and requires the same records at the same (epoch, tick).

const bedrockOracleDir = "../coding/testdata/rpc33-observation/providers/bedrock-converse-stream"

type bedrockOracleRecord struct {
	At     string          `json:"at"`
	Epoch  int             `json:"epoch"`
	Tick   int             `json:"tick"`
	Event  json.RawMessage `json:"event"`
	Result json.RawMessage `json:"result"`
}

type bedrockOracleCase struct {
	Shape    string                `json:"shape"`
	Delivery string                `json:"delivery"`
	HTTP     string                `json:"http"`
	Consumer string                `json:"consumer"`
	Layers   json.RawMessage       `json:"layers"`
	Hooks    *bool                 `json:"hooks"`
	Epochs   int                   `json:"epochs"`
	Records  []bedrockOracleRecord `json:"records"`
}

type bedrockOracle struct {
	Cases []bedrockOracleCase `json:"cases"`
}

type bedrockOracleInputs struct {
	Bodies map[string][]string `json:"bodies"`
}

var (
	bedrockOracleOnce sync.Once
	bedrockOracleData bedrockOracle
	bedrockInputsData bedrockOracleInputs
	bedrockOracleErr  error
)

func loadBedrockOracle(t *testing.T) (*bedrockOracle, *bedrockOracleInputs) {
	t.Helper()
	bedrockOracleOnce.Do(func() {
		for name, target := range map[string]any{"pi.json": &bedrockOracleData, "inputs.json": &bedrockInputsData} {
			content, err := os.ReadFile(filepath.Join(bedrockOracleDir, name))
			if err == nil {
				err = json.Unmarshal(content, target)
			}
			if err != nil {
				bedrockOracleErr = fmt.Errorf("%s: %w", name, err)
				return
			}
		}
	})
	if bedrockOracleErr != nil {
		t.Fatal(bedrockOracleErr)
	}
	return &bedrockOracleData, &bedrockInputsData
}

// bedrockJobCounter counts microtask jobs and macrotask epochs the way probe.mjs does: a tick is one microtask job, an epoch is one macrotask that delivers bytes, and two epoch starts with no job between them are one epoch.
type bedrockJobCounter struct {
	mu       sync.Mutex
	epoch    int
	tick     int
	onEpoch1 func()
}

func (counter *bedrockJobCounter) microtask() {
	counter.mu.Lock()
	defer counter.mu.Unlock()
	counter.tick++
	if counter.epoch == 1 && counter.tick == 1 && counter.onEpoch1 != nil {
		release := counter.onEpoch1
		counter.onEpoch1 = nil
		go release()
	}
}

func (counter *bedrockJobCounter) external() {
	counter.mu.Lock()
	defer counter.mu.Unlock()
	if counter.tick > 0 || counter.epoch == 0 {
		counter.epoch++
		counter.tick = 0
	}
}

func (counter *bedrockJobCounter) now() (int, int) {
	counter.mu.Lock()
	defer counter.mu.Unlock()
	return counter.epoch, counter.tick
}

// bedrockFixtureServer answers one ConverseStream request with the delivery the oracle recorded: buffered is one write; pending and split write the headers with the first bytes and the rest after release. It answers only after open, so that the consumer waits on the stream first, as Pi's `for await` does.
type bedrockFixtureServer struct {
	listener net.Listener
	first    []byte
	rest     []byte
	buffered bool
	open     chan struct{}
	release  chan struct{}
	done     chan struct{}
	err      chan error
}

func newBedrockFixtureServer(t testing.TB, first, rest []byte, buffered bool) *bedrockFixtureServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &bedrockFixtureServer{listener: listener, first: first, rest: rest, buffered: buffered, open: make(chan struct{}), release: make(chan struct{}), done: make(chan struct{}), err: make(chan error, 1)}
	go server.serve()
	t.Cleanup(func() {
		_ = listener.Close()
		server.releaseOnce()
		<-server.done
	})
	return server
}

var bedrockReleaseMu sync.Mutex

func (server *bedrockFixtureServer) releaseOnce() {
	bedrockReleaseMu.Lock()
	defer bedrockReleaseMu.Unlock()
	select {
	case <-server.release:
	default:
		close(server.release)
	}
}

func (server *bedrockFixtureServer) openOnce() {
	bedrockReleaseMu.Lock()
	defer bedrockReleaseMu.Unlock()
	select {
	case <-server.open:
	default:
		close(server.open)
	}
}

func (server *bedrockFixtureServer) serve() {
	defer close(server.done)
	conn, err := server.listener.Accept()
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	reader := bufio.NewReader(conn)
	request, err := http.ReadRequest(reader)
	if err != nil {
		server.err <- err
		return
	}
	_, _ = io.Copy(io.Discard, request.Body)
	<-server.open
	head := "HTTP/1.1 200 OK\r\nContent-Type: application/vnd.amazon.eventstream\r\nX-Amzn-Requestid: req-1\r\n"
	if server.buffered {
		body := append(append([]byte{}, server.first...), server.rest...)
		_, _ = conn.Write(append([]byte(head+"Content-Length: "+strconv.Itoa(len(body))+"\r\n\r\n"), body...))
		return
	}
	chunk := func(data []byte) []byte {
		return append(append([]byte(fmt.Sprintf("%x\r\n", len(data))), data...), "\r\n"...)
	}
	_, _ = conn.Write(append([]byte(head+"Transfer-Encoding: chunked\r\n\r\n"), chunk(server.first)...))
	<-server.release
	_, _ = conn.Write(append(chunk(server.rest), "0\r\n\r\n"...))
}

var bedrockTimestamp = regexp.MustCompile(`"timestamp":\d{10,}`)

// bedrockDuration keeps only the presence of durationMs, which Pi 1.1.0 sets from wall time on the final message (event-stream.ts:127-128).
var bedrockDuration = regexp.MustCompile(`"durationMs":\d+`)

func canonicalBedrockJSON(t *testing.T, raw []byte) any {
	t.Helper()
	if len(raw) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(bedrockDuration.ReplaceAll(bedrockTimestamp.ReplaceAll(raw, []byte(`"timestamp":0`)), []byte(`"durationMs":0`)), &value); err != nil {
		t.Fatalf("%v in %s", err, raw)
	}
	// durationMs is a monotonic reading that differs in every run of Pi and Go.
	var drop func(any)
	drop = func(node any) {
		switch node := node.(type) {
		case map[string]any:
			delete(node, "durationMs")
			for _, item := range node {
				drop(item)
			}
		case []any:
			for _, item := range node {
				drop(item)
			}
		}
	}
	drop(value)
	return value
}

func bedrockOracleFrames(t *testing.T, inputs *bedrockOracleInputs, shape, delivery string) (first, rest []byte) {
	t.Helper()
	var frames [][]byte
	for _, encoded := range inputs.Bodies[shape] {
		frame, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		frames = append(frames, frame)
	}
	join := func(parts ...[]byte) []byte { return bytes.Join(parts, nil) }
	switch delivery {
	case "buffered":
		return nil, join(frames...)
	case "pending":
		return frames[0], join(frames[1:]...)
	default:
		cut := len(frames[1]) / 2
		return join(frames[0], frames[1][:cut]), join(append([][]byte{frames[1][cut:]}, frames[2:]...)...)
	}
}

func bedrockObservationProvider(port string) *BedrockProvider {
	return NewBedrockProviderWithModel(Model{
		ID: "probe", DisplayName: "probe",
		ProviderMeta: ProviderMetadata{ProviderID: "probe-provider", API: APIBedrockConverseStream, BaseURL: "http://127.0.0.1:" + port},
		Capabilities: ModelCapabilities{ContextWindow: 4096, MaxOutputTokens: 256},
	})
}

// runBedrockObservation replays one oracle case and returns the consumer records it produced.
func runBedrockObservation(t *testing.T, tc bedrockOracleCase, inputs *bedrockOracleInputs) []bedrockOracleRecord {
	t.Helper()
	first, rest := bedrockOracleFrames(t, inputs, tc.Shape, tc.Delivery)
	server := newBedrockFixtureServer(t, first, rest, tc.Delivery == "buffered")
	_, port, _ := net.SplitHostPort(server.listener.Addr().String())
	base, executor := withContinuationExecutor(t.Context())
	// The signal aborts the request only: the consumer's iteration is not tied to it, as Pi's `for await` is not.
	ctx, cancel := context.WithCancel(base)
	defer cancel()
	counter := &bedrockJobCounter{}
	// A canceled request never receives the withheld bytes: the abort is the only thing left to observe, and releasing them would race it.
	if tc.Delivery != "buffered" && tc.Consumer != "cancel" {
		counter.onEpoch1 = server.releaseOnce
	}
	executor.trace = &executorTrace{external: counter.external, microtask: counter.microtask}
	provider := bedrockObservationProvider(port)
	withHooks := tc.Hooks == nil || *tc.Hooks
	hook := func(f func(any, *Model) (any, error)) func(any, *Model) (any, error) {
		if withHooks {
			return f
		}
		return nil
	}
	hookResponse := func(f func(context.Context, ProviderResponse, *Model) error) func(context.Context, ProviderResponse, *Model) error {
		if withHooks {
			return f
		}
		return nil
	}
	stream, err := provider.Stream(ctx, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("probe")}}}), StreamOptions{
		APIKey: "test", Env: ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1"},
		// sdk.ts:349-388 passes both hooks on every Agent request; the oracle passes them too.
		OnPayload:  hook(func(any, *Model) (any, error) { return nil, nil }),
		OnResponse: hookResponse(func(context.Context, ProviderResponse, *Model) error { return nil }),
	})
	if err != nil {
		t.Fatal(err)
	}
	var records []bedrockOracleRecord
	record := func(at string, event AssistantMessageEvent, result *AssistantMessage) {
		epoch, tick := counter.now()
		entry := bedrockOracleRecord{At: at, Epoch: epoch, Tick: tick}
		var raw any = event
		if result != nil {
			raw = result
		}
		encoded, err := json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		if result != nil {
			entry.Result = encoded
		} else {
			entry.Event = encoded
		}
		records = append(records, entry)
	}
	executor.run(func(turn *continuationTurn) {
		server.openOnce()
		if tc.Consumer == "result-only" {
			record("result-before-iteration", nil, awaitContinuation(turn, stream.resultContinuation(executor)))
		}
		loop := context.WithValue(base, continuationTurnKey{}, turn)
		for _, event := range stream.observeEvents(loop, true) {
			record("entry", event, nil)
			if _, isStart := event.(StartEvent); isStart && tc.Consumer == "cancel" {
				cancel()
			}
		}
		record("result", nil, awaitContinuation(turn, stream.resultContinuation(executor)))
	})
	return records
}

func TestBedrockObservationMatchesPi(t *testing.T) {
	oracle, inputs := loadBedrockOracle(t)
	ran := 0
	for _, tc := range oracle.Cases {
		if tc.HTTP != "h1" || string(tc.Layers) != "0" || (tc.Consumer != "direct" && tc.Consumer != "result-only") {
			continue
		}
		ran++
		name := strings.Join([]string{tc.Shape, tc.Delivery, tc.Consumer}, "/")
		if tc.Hooks != nil && !*tc.Hooks {
			name += "/no-hooks"
		}
		t.Run(name, func(t *testing.T) {
			got := runBedrockObservation(t, tc, inputs)
			describe := func(records []bedrockOracleRecord) string {
				var b strings.Builder
				for _, r := range records {
					var kind struct {
						Type  string `json:"type"`
						Error struct {
							ErrorMessage string `json:"errorMessage"`
						} `json:"error"`
					}
					_ = json.Unmarshal(r.Event, &kind)
					fmt.Fprintf(&b, "  %d:%d %s %s %s\n", r.Epoch, r.Tick, r.At, kind.Type, kind.Error.ErrorMessage)
				}
				return b.String()
			}
			if len(got) != len(tc.Records) {
				t.Fatalf("records: got %d want %d\ngot:\n%swant:\n%s", len(got), len(tc.Records), describe(got), describe(tc.Records))
			}
			for i := range got {
				want := tc.Records[i]
				if got[i].At != want.At || got[i].Epoch != want.Epoch || got[i].Tick != want.Tick {
					t.Fatalf("record %d at (epoch,tick): got %d:%d %s want %d:%d %s\ngot:\n%swant:\n%s", i, got[i].Epoch, got[i].Tick, got[i].At, want.Epoch, want.Tick, want.At, describe(got), describe(tc.Records))
				}
				if g, w := canonicalBedrockJSON(t, got[i].Event), canonicalBedrockJSON(t, want.Event); !reflect.DeepEqual(g, w) {
					t.Fatalf("record %d event at %d:%d\n got %s\nwant %s", i, want.Epoch, want.Tick, got[i].Event, want.Event)
				}
				if g, w := canonicalBedrockJSON(t, got[i].Result), canonicalBedrockJSON(t, want.Result); !reflect.DeepEqual(g, w) {
					t.Fatalf("record %d result at %d:%d\n got %s\nwant %s", i, want.Epoch, want.Tick, got[i].Result, want.Result)
				}
			}
		})
	}
	if ran == 0 {
		t.Fatal("the oracle has no replayable case")
	}
}

// Pi's abort runs the SDK's abort listeners synchronously inside `controller.abort()`, so their reactions join the queue at the call and a pending read fails within the same epoch (`aborted` at epoch 1, tick 52 after `start` at tick 42). PiG delivers a context cancellation to the executor as an external completion. The oracle's cancel consumer therefore fixes the event sequence, the terminal message and the stop reason, not the microtask jobs.
func TestBedrockCancellationMatchesPiOutcome(t *testing.T) {
	oracle, inputs := loadBedrockOracle(t)
	ran := 0
	for _, tc := range oracle.Cases {
		if tc.HTTP != "h1" || string(tc.Layers) != "0" || tc.Consumer != "cancel" || (tc.Hooks != nil && !*tc.Hooks) {
			continue
		}
		ran++
		t.Run(tc.Shape+"/"+tc.Delivery, func(t *testing.T) {
			got := runBedrockObservation(t, tc, inputs)
			outcome := func(records []bedrockOracleRecord) (types []string, terminal any) {
				for _, r := range records {
					var kind struct {
						Type string `json:"type"`
					}
					_ = json.Unmarshal(r.Event, &kind)
					if r.At == "entry" {
						types = append(types, kind.Type)
					}
					if r.At == "result" {
						terminal = canonicalBedrockJSON(t, r.Result)
					}
				}
				return types, terminal
			}
			gotTypes, gotResult := outcome(got)
			wantTypes, wantResult := outcome(tc.Records)
			if !reflect.DeepEqual(gotTypes, wantTypes) {
				t.Fatalf("event types\n got %v\nwant %v", gotTypes, wantTypes)
			}
			if !reflect.DeepEqual(gotResult, wantResult) {
				t.Fatalf("terminal message\n got %v\nwant %v", gotResult, wantResult)
			}
		})
	}
	if ran == 0 {
		t.Fatal("the oracle has no cancel case")
	}
}
