//go:build !pig_strip_bedrock_converse_stream

package bedrock

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/requestvalidity"
)

// bedrockSurrogateDelta finds the kind and the delta of a delta event as JSON.stringify writes it.
var bedrockSurrogateDelta = regexp.MustCompile(`\{"type":"(text_delta|thinking_delta|toolcall_delta)","contentIndex":\d+,"delta":("(?:[^"\\]|\\.)*")`)

// bedrockSurrogateMarks finds the marked texts of a message: T[..] of a text block, H[..] of a thinking block, A[..] of a tool call argument.
var bedrockSurrogateMarks = regexp.MustCompile(`\b[THA]\[[^\]]*\]`)

// An event payload of a ConverseStream response is JSON the SDK reads with JSON.parse, which reads the escape of a UTF-16 surrogate as that
// unit. Two rows, each with the unit in a reasoning delta, a text delta and a tool-input delta: a character split over two deltas is one
// character in the message, and a unit that stays alone is in the message as the unit. The deltas the stream yields and the marked texts of the
// message it ends with are the ones of Pi, compared in the bytes JSON.stringify writes.
func TestBedrockStreamKeepsSurrogatesAsPi(t *testing.T) {
	modes := []struct{ name, second, text string }{
		{"a character split over two deltas", `\ude00`, "T[\U0001F600]"},
		{"a unit that stays alone", "", `T[\ud83d]`},
	}
	var probes []bedrockResponseProbe
	for _, mode := range modes {
		p := bedrockResponseProbe{ModelID: "anthropic.claude-sonnet-4-5-20250929-v1:0", Reasoning: true}
		p.frames = [][2]string{
			{"messageStart", `{"role":"assistant"}`},
			{"contentBlockDelta", `{"contentBlockIndex":0,"delta":{"reasoningContent":{"text":"H[\ud83d"}}}`},
			{"contentBlockDelta", `{"contentBlockIndex":0,"delta":{"reasoningContent":{"text":"§]"}}}`},
			{"contentBlockStop", `{"contentBlockIndex":0}`},
			{"contentBlockDelta", `{"contentBlockIndex":1,"delta":{"text":"T[\ud83d"}}`},
			{"contentBlockDelta", `{"contentBlockIndex":1,"delta":{"text":"§]"}}`},
			{"contentBlockStop", `{"contentBlockIndex":1}`},
			{"contentBlockStart", `{"contentBlockIndex":2,"start":{"toolUse":{"toolUseId":"tool_1","name":"N"}}}`},
			{"contentBlockDelta", `{"contentBlockIndex":2,"delta":{"toolUse":{"input":"{\"p\":\"A[\ud83d"}}}`},
			{"contentBlockDelta", `{"contentBlockIndex":2,"delta":{"toolUse":{"input":"§]\"}"}}}`},
			{"contentBlockStop", `{"contentBlockIndex":2}`},
			{"messageStop", `{"stopReason":"tool_use"}`},
			{"metadata", `{"usage":{"inputTokens":1,"outputTokens":1,"totalTokens":2},"metrics":{"latencyMs":5}}`},
		}
		for i := range p.frames {
			p.frames[i][1] = strings.ReplaceAll(p.frames[i][1], "§", mode.second)
		}
		var body bytes.Buffer
		writeBedrockFrames(t, &body, p.frames)
		p.Body = base64.StdEncoding.EncodeToString(body.Bytes())
		probes = append(probes, p)
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/bedrock_response_oracle.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = withoutAWSEnvironment(os.Environ())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []struct {
		Events  json.RawMessage `json:"events"`
		Message json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	clearAWSEnvironment(t)
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(t.TempDir(), "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "credentials"))
	for i, p := range probes {
		t.Run(modes[i].name, func(t *testing.T) {
			var wantDeltas []string
			for _, found := range bedrockSurrogateDelta.FindAllSubmatch(want[i].Events, -1) {
				wantDeltas = append(wantDeltas, string(found[1])+" "+string(found[2]))
			}
			wantMarks := strings.Join(bedrockSurrogateMarks.FindAllString(string(want[i].Message), -1), " ")
			if len(wantDeltas) != 6 || !strings.Contains(wantMarks, modes[i].text) || !strings.Contains(wantMarks, "A[") || !strings.Contains(wantMarks, "H[") {
				t.Fatalf("the stream of Pi does not hold the six deltas and the three texts: %s\n%s", want[i].Events, want[i].Message)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = bedrockOracleReadAll(r)
				w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
				writeBedrockFrames(t, w, p.frames)
			}))
			defer server.Close()
			model := &ai.Model{ID: p.ModelID, DisplayName: "m", Input: []string{"text"}, Capabilities: ai.ModelCapabilities{ContextWindow: 200000, MaxOutputTokens: 16384, InputCostPer1M: 1, OutputCostPer1M: 2, CacheReadCostPer1M: 0.5, CacheWriteCostPer1M: 3}, ProviderMeta: ai.ProviderMetadata{API: ai.APIBedrockConverseStream, ProviderID: "amazon-bedrock", BaseURL: server.URL, Reasoning: p.Reasoning}}
			provider := NewBedrockProviderWithModel(*model)
			defer func() { _ = provider.Close() }()
			stream, err := provider.Stream(requestvalidity.Allow(t.Context()), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi"), Timestamp: 1}}}), ai.StreamOptions{ModelCost: model.CostRates(), CacheRetention: ai.CacheRetentionNone, Env: ai.ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1"}})
			if err != nil {
				t.Fatal(err)
			}
			var deltas []string
			for event := range stream.Events(t.Context()) {
				kind, delta := "", ""
				switch event := event.(type) {
				case ai.TextDeltaEvent:
					kind, delta = "text_delta", event.Delta
				case ai.ThinkingDeltaEvent:
					kind, delta = "thinking_delta", event.Delta
				case ai.ToolCallDeltaEvent:
					kind, delta = "toolcall_delta", event.Delta
				default:
					continue
				}
				deltas = append(deltas, kind+" "+string(sdkjson.AppendStringifyString(nil, delta)))
			}
			if got, want := strings.Join(deltas, "\n"), strings.Join(wantDeltas, "\n"); got != want {
				t.Errorf("the stream yields\n%s\nthe stream of Pi yields\n%s", got, want)
			}
			encoded, err := jsstring.MarshalJSON(stream.Result())
			if err != nil {
				t.Fatal(err)
			}
			if marks := strings.Join(bedrockSurrogateMarks.FindAllString(string(encoded), -1), " "); marks != wantMarks {
				t.Errorf("the message holds %q, the message of Pi holds %q\nPiG: %s\nPi:  %s", marks, wantMarks, encoded, want[i].Message)
			}
		})
	}
}
