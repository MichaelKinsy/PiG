//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/requestvalidity"
)

// pi: packages/ai/src/api/bedrock-converse-stream.ts

// bedrockResponseProbe is one scripted ConverseStream response: the event-stream frames (kind, JSON payload) and, for an error response, an HTTP status and body.
type bedrockResponseProbe struct {
	ModelID   string `json:"modelId"`
	Reasoning bool   `json:"reasoning"`
	Body      string `json:"body"`
	Status    int    `json:"status,omitempty"`
	ErrorBody string `json:"errorBody,omitempty"`
	frames    [][2]string
}

// bedrockFrames draws a ConverseStream response: well-formed turns of text, tool-use and reasoning blocks with random deltas, usage and stop reasons, and the
// departures a service or a proxy can produce (a missing messageStart, stop or metadata, indices out of order or reused, a stop reason out of the enum, an
// exception frame, an unknown event type, a payload that is not JSON).
func bedrockFrames(rng *rand.Rand) [][2]string {
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	quote := func(s string) string { encoded, _ := json.Marshal(s); return string(encoded) }
	text := func() string {
		return pick("hello", "", "line1\nline2", "😀 é", "  padded  ", `quote " and \ slash`, "<&>\u2028", "x")
	}
	var frames [][2]string
	// A stream with no messageStart is not drawn: Pi then emits no "start" event and PiG's builder always emits one (open finding, questions/lg-help-4.md).
	frames = append(frames, [2]string{"messageStart", pick(`{"role":"assistant"}`, `{"role":"assistant"}`, `{"role":"user"}`, `{}`)})
	blocks := rng.Intn(4)
	for index := range blocks {
		at := index
		if rng.Intn(10) == 0 {
			at = rng.Intn(4)
		}
		switch rng.Intn(3) {
		case 0:
			for range rng.Intn(4) {
				frames = append(frames, [2]string{"contentBlockDelta", fmt.Sprintf(`{"contentBlockIndex":%d,"delta":{"text":%s}}`, at, quote(text()))})
			}
		case 1:
			if rng.Intn(8) != 0 {
				frames = append(frames, [2]string{"contentBlockStart", fmt.Sprintf(`{"contentBlockIndex":%d,"start":{"toolUse":{"toolUseId":%s,"name":%s}}}`, at, quote(pick("tool_1", "call-2", "", "a b")), quote(pick("read", "bash", "")))})
			}
			input := pick(`{"path":"a.txt"}`, `{"a":1,"b":{"c":[1,2]}}`, `{}`, `{"x":`, ``, `[1,2]`, `{"s":"é😀"}`, `not json`)
			for len(input) > 0 {
				cut := min(len(input), 1+rng.Intn(6))
				for cut < len(input) && input[cut]&0xC0 == 0x80 {
					cut++
				}
				frames = append(frames, [2]string{"contentBlockDelta", fmt.Sprintf(`{"contentBlockIndex":%d,"delta":{"toolUse":{"input":%s}}}`, at, quote(input[:cut]))})
				input = input[cut:]
			}
		default:
			for range 1 + rng.Intn(3) {
				switch rng.Intn(4) {
				case 0, 1:
					frames = append(frames, [2]string{"contentBlockDelta", fmt.Sprintf(`{"contentBlockIndex":%d,"delta":{"reasoningContent":{"text":%s}}}`, at, quote(text()))})
				case 2:
					frames = append(frames, [2]string{"contentBlockDelta", fmt.Sprintf(`{"contentBlockIndex":%d,"delta":{"reasoningContent":{"signature":%s}}}`, at, quote(pick("sig", "EqQB", "")))})
				default:
					frames = append(frames, [2]string{"contentBlockDelta", fmt.Sprintf(`{"contentBlockIndex":%d,"delta":{"reasoningContent":{"redactedContent":%s}}}`, at, quote(base64.StdEncoding.EncodeToString([]byte(pick("opaque", "\x00\x01\x02", "é")))))})
				}
			}
		}
		if rng.Intn(10) != 0 {
			frames = append(frames, [2]string{"contentBlockStop", fmt.Sprintf(`{"contentBlockIndex":%d}`, at)})
		}
	}
	usage := func() [2]string {
		var members []string
		for _, name := range []string{"inputTokens", "outputTokens", "totalTokens", "cacheReadInputTokens", "cacheWriteInputTokens"} {
			if rng.Intn(4) != 0 {
				members = append(members, fmt.Sprintf(`"%s":%d`, name, rng.Intn(5000)))
			}
		}
		if rng.Intn(4) == 0 {
			return [2]string{"metadata", `{"metrics":{"latencyMs":5}}`}
		}
		return [2]string{"metadata", `{"usage":{` + strings.Join(members, ",") + `},"metrics":{"latencyMs":5}}`}
	}
	// A second messageStop never carries an empty stop reason: Pi then sets rawStopReason to "" and PiG's AssistantMessage cannot tell "" from unset (open ruling).
	stopFrame := func(reasons ...string) [2]string {
		fields := pick("", "", `,"additionalModelResponseFields":{"source":"test"}`)
		return [2]string{"messageStop", `{"stopReason":` + quote(pick(reasons...)) + fields + `}`}
	}
	allReasons := []string{"end_turn", "tool_use", "max_tokens", "stop_sequence", "guardrail_intervened", "content_filtered", "model_context_window_exceeded", "malformed_model_output", "malformed_tool_use", "bogus", ""}
	metadataFirst := rng.Intn(8) == 0
	if metadataFirst && rng.Intn(2) == 0 {
		frames = append(frames, usage())
	}
	if rng.Intn(10) != 0 {
		frames = append(frames, stopFrame(allReasons...))
		if rng.Intn(12) == 0 {
			frames = append(frames, stopFrame(allReasons[:len(allReasons)-1]...))
		}
	}
	if !metadataFirst && rng.Intn(6) != 0 {
		frames = append(frames, usage())
	}
	switch rng.Intn(16) {
	case 0:
		frames = append(frames, [2]string{pick("!internalServerException", "!throttlingException", "!validationException", "!serviceUnavailableException"), `{"message":` + quote(pick("boom", "", "<&>")) + `}`})
	case 1:
		frames = append(frames, [2]string{"!modelStreamErrorException", `{"message":"stream failed","originalStatusCode":502,"originalMessage":"upstream"}`})
	case 2:
		at := 1 + rng.Intn(len(frames))
		frames = append(frames[:at], append([][2]string{{"unknownEvent", `{"x":1}`}}, frames[at:]...)...)
	case 3:
		at := 1 + rng.Intn(len(frames))
		frames = append(frames[:at], append([][2]string{{"contentBlockDelta", `{not json`}}, frames[at:]...)...)
	case 4:
		frames = frames[:1+rng.Intn(len(frames))]
	case 5, 6:
		// The first event fails inside client.send, where the SDK's deserializer middleware adds its hint and a response-failure diagnostic.
		frames = append([][2]string{{pick("contentBlockDelta", "messageStart", "metadata"), pick("{not json", "", "[1,", `{"a":}`)}}, frames...)
	}
	return frames
}

// The message and events Pi builds from a ConverseStream response (processing of messageStart, contentBlockStart/Delta/Stop, messageStop and metadata, usage and cost, the stop reason
// mapping, and the errors of a malformed or truncated stream) against PiG's, over random scripted responses served to both.
func TestBedrockResponseStreamMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(131))
	var probes []bedrockResponseProbe
	for range 1000 {
		p := bedrockResponseProbe{ModelID: "anthropic.claude-sonnet-4-5-20250929-v1:0", Reasoning: rng.Intn(2) == 0}
		if rng.Intn(25) == 0 {
			p.Status = []int{400, 403, 429, 500, 503}[rng.Intn(5)]
			p.ErrorBody = []string{`{"message":"bad request"}`, `{"message":"throttled","__type":"ThrottlingException"}`, `not json`, ``}[rng.Intn(4)]
		} else {
			p.frames = bedrockFrames(rng)
			var body bytes.Buffer
			writeBedrockFrames(t, &body, p.frames)
			p.Body = base64.StdEncoding.EncodeToString(body.Bytes())
		}
		probes = append(probes, p)
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/bedrock_response_oracle.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []struct {
		Events  []map[string]any `json:"events"`
		Message map[string]any   `json:"message"`
	}
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_DEFAULT_PROFILE", "")
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(t.TempDir(), "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "credentials"))
	failures, answered := 0, 0
	classes := map[string]int{}
	for i, p := range probes {
		if content, _ := want[i].Message["content"].([]any); len(content) > 0 {
			answered++
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = bedrockOracleReadAll(r)
			if p.Status != 0 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(p.Status)
				_, _ = w.Write([]byte(p.ErrorBody))
				return
			}
			w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
			writeBedrockFrames(t, w, p.frames)
		}))
		model := &Model{ID: p.ModelID, DisplayName: "m", Input: []string{"text"}, Capabilities: ModelCapabilities{ContextWindow: 200000, MaxOutputTokens: 16384, InputCostPer1M: 1, OutputCostPer1M: 2, CacheReadCostPer1M: 0.5, CacheWriteCostPer1M: 3}, ProviderMeta: ProviderMetadata{API: APIBedrockConverseStream, ProviderID: "amazon-bedrock", BaseURL: server.URL, Reasoning: p.Reasoning}}
		provider := NewBedrockProviderWithModel(*model)
		stream, err := provider.Stream(requestvalidity.Allow(t.Context()), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi"), Timestamp: 1}}}), StreamOptions{ModelCost: model.CostRates(), CacheRetention: CacheRetentionNone, Env: ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1"}})
		var events []map[string]any
		var message map[string]any
		if err != nil {
			message = map[string]any{"thrown": err.Error()}
		} else {
			for event := range stream.Events(t.Context()) {
				raw, _ := json.Marshal(event)
				var decoded map[string]any
				_ = json.Unmarshal(raw, &decoded)
				delete(decoded, "partial")
				delete(decoded, "message")
				delete(decoded, "error")
				events = append(events, decoded)
			}
			raw, _ := json.Marshal(stream.Result())
			_ = json.Unmarshal(raw, &message)
			delete(message, "timestamp")
			delete(message, "durationMs")
		}
		_ = provider.Close()
		server.Close()
		got := map[string]any{"events": events, "message": message}
		wantParts := map[string]any{"events": want[i].Events, "message": want[i].Message}
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(wantParts)
		// D120: a tool call streamed without an id gets a generated "<name>_<epoch ms>_<counter>" id in PiG where Pi stores "".
		gotJSON = bedrockOracleGeneratedID.ReplaceAll(gotJSON, []byte(`"id":""`))
		// A diagnostic carries the clock.
		gotJSON, wantJSON = bedrockOracleClock.ReplaceAll(gotJSON, []byte(`"timestamp":0`)), bedrockOracleClock.ReplaceAll(wantJSON, []byte(`"timestamp":0`))
		// An empty rawStopReason is a present member in Pi and an omitted one in PiG (open ruling: AssistantMessage "" vs omitted).
		gotJSON, wantJSON = bytes.ReplaceAll(gotJSON, []byte(`"rawStopReason":"",`), nil), bytes.ReplaceAll(wantJSON, []byte(`"rawStopReason":"",`), nil)
		var gotValue, wantValue any
		_ = json.Unmarshal(gotJSON, &gotValue)
		_ = json.Unmarshal(wantJSON, &wantValue)
		if !reflect.DeepEqual(gotValue, wantValue) {
			classes[bedrockOracleClass(p)]++
			if failures++; failures <= 3 {
				label, _ := json.Marshal(p.frames)
				t.Errorf("probe %d status=%d frames=%s:\n%s", i, p.Status, label, bedrockOracleDiff(gotJSON, wantJSON))
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d probes differ from Pi; classes %v", failures, len(probes), classes)
	}
	if answered*10 < len(probes)*3 {
		t.Fatalf("Pi built content for only %d of %d probes", answered, len(probes))
	}
}

var bedrockOracleGeneratedID = regexp.MustCompile(`"id":"[^"]*_\d{13}_\d+"`)

var bedrockOracleClock = regexp.MustCompile(`"timestamp":\d+`)

func bedrockOracleReadAll(r *http.Request) ([]byte, error) {
	var body bytes.Buffer
	_, err := body.ReadFrom(r.Body)
	return body.Bytes(), err
}

func bedrockOracleDiff(got, want []byte) string {
	i := 0
	for i < len(got) && i < len(want) && got[i] == want[i] {
		i++
	}
	from := max(0, i-80)
	return fmt.Sprintf(" PiG …%.500s\n Pi  …%.500s", got[from:], want[from:])
}

// bedrockOracleClass names the departures a failing probe's frames contain.
func bedrockOracleClass(p bedrockResponseProbe) string {
	var parts []string
	if p.Status != 0 {
		return fmt.Sprintf("status %d body %q", p.Status, p.ErrorBody)
	}
	seen := map[string]bool{}
	for _, frame := range p.frames {
		seen[frame[0]] = true
		if !json.Valid([]byte(frame[1])) {
			seen["badjson"] = true
		}
	}
	if !seen["messageStart"] {
		parts = append(parts, "no-messageStart")
	}
	for _, name := range []string{"badjson", "unknownEvent", "!internalServerException", "!throttlingException", "!validationException", "!serviceUnavailableException", "!modelStreamErrorException"} {
		if seen[name] {
			parts = append(parts, name)
		}
	}
	return strings.Join(parts, "+")
}
