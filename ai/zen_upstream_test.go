package ai

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
)

type openCodeModelCase struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
}

// The case denominator is the reviewed snapshot of Pi's MODELS, not PiG's catalog. Regenerate from the pinned package with: node test/parity/testdata/generate-zen-models.mjs > ai/testdata/zen-models.json
func upstreamOpenCodeCases(t *testing.T) []openCodeModelCase {
	t.Helper()
	data, err := os.ReadFile("testdata/zen-models.json")
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := exec.CommandContext(t.Context(), "node", "../test/parity/testdata/generate-zen-models.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("read pinned Pi case denominator: %v\n%s", err, oracle)
	}
	if !bytes.Equal(data, oracle) {
		t.Fatal("zen-models snapshot differs from pinned Pi; regenerate it with the command above")
	}
	var cases []openCodeModelCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("empty upstream case denominator")
	}
	return cases
}

func upstreamOpenCodeRequestPayloads(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	data, err := exec.CommandContext(t.Context(), "node", "../test/parity/testdata/zen-request-probe.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("capture pinned Pi requests: %v\n%s", err, data)
	}
	var requests map[string]json.RawMessage
	if err := json.Unmarshal(data, &requests); err != nil {
		t.Fatal(err)
	}
	return requests
}

// .upstream/v0.87.1/packages/ai/test/zen.test.ts:15 (every model in both catalog tables)
func TestOpenCodeModelsSmokeUpstream(t *testing.T) {
	requests := upstreamOpenCodeRequestPayloads(t)
	for _, tc := range upstreamOpenCodeCases(t) {
		label := "OpenCode Zen"
		if tc.Provider == "opencode-go" {
			label = "OpenCode Go"
		}
		t.Run(label+": "+tc.ID, func(t *testing.T) {
			model, ok := LookupModelExact(tc.Provider + "/" + tc.ID)
			if !ok {
				t.Fatal("upstream catalog model is missing")
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if requested, present := body["model"]; present {
					if requested != model.ID {
						t.Errorf("model = %#v, want %q", requested, model.ID)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
				} else if model.API == APIGoogleGenerativeAI || model.API == APIGoogleVertex {
					if !strings.Contains(r.URL.Path, "/models/"+model.ID+":streamGenerateContent") {
						t.Errorf("request path %q does not identify model %q", r.URL.Path, model.ID)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
				} else {
					t.Errorf("request omitted model %q", model.ID)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				encoded, err := json.Marshal(body)
				if err != nil {
					t.Error(err)
					return
				}
				if !strings.Contains(string(encoded), "Say hello.") {
					t.Error("missing original user prompt")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				writeMatrixUsageResponse(t, w, model.API, "Hello.", false)
			}))
			defer server.Close()
			provider := newMatrixProvider(t, model, server.URL, false)
			defer func() {
				if err := provider.Close(); err != nil {
					t.Error(err)
				}
			}()
			var captured []byte
			stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Say hello."), Timestamp: 1}}}), StreamOptions{IsReasoning: model.Reasoning, ModelCost: model.ToModel().CostRates(), Transport: TransportSSE, OnPayload: func(value any, _ *Model) (any, error) {
				var err error
				captured, err = json.Marshal(value)
				return nil, err
			}})
			if err != nil {
				t.Fatal(err)
			}
			response := stream.Result()
			want, ok := requests[tc.Provider+"/"+tc.ID]
			if !ok {
				t.Fatal("pinned Pi request capture is missing for this model")
			}
			assertShapeJSON(t, captured, string(want))
			if response.Content == nil {
				t.Fatal("missing response content")
			}
			if response.StopReason != StopReasonStop {
				t.Fatalf("stopReason = %s; error = %s", response.StopReason, response.ErrorMessage)
			}
		})
	}
}
