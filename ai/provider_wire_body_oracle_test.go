package ai_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// skipStrippedAPI skips the row of an API this build strips (a pig_strip_<api> tag): the code the row exercises is not in the binary, so the
// row has nothing to compare; the rows of every other API in the same table run.
func skipStrippedAPI(t *testing.T, api string) {
	t.Helper()
	if pigstrip.Has(pigstrip.ListAPIs, api) {
		t.Skipf("API %s is stripped from this build", api)
	}
}

// providerWireRow is one request of a wire-body table: the model, the stream options and the Context, as the JSON both sides read.
type providerWireRow struct {
	Name      string          `json:"name"`
	API       string          `json:"api"`
	ID        string          `json:"id"`
	Provider  string          `json:"provider"`
	BaseURL   string          `json:"baseUrl"`
	APIKey    string          `json:"apiKey"`
	Reasoning bool            `json:"reasoning"`
	Compat    json.RawMessage `json:"compat,omitempty"`
	Model     json.RawMessage `json:"model,omitempty"`
	Simple    bool            `json:"simple"`
	Options   json.RawMessage `json:"options"`
	Context   json.RawMessage `json:"context"`
}

// The pieces the wire-body tables build their conversations from.
const (
	wireSystem   = `"systemPrompt":"You are helpful."`
	wireReadTool = `{"name":"read","description":"Read a file","parameters":{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"number"}},"required":["path"]}}`
	wireFindTool = `{"name":"find","description":"Find files","parameters":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}}`
	wireTools    = `"tools":[` + wireReadTool + `,` + wireFindTool + `]`
	wireUsage    = `"usage":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}`
	wireText     = `{"type":"text","text":"ok"}`
	wireUserGo   = `{"role":"user","content":"go","timestamp":1}`
	wireUserNext = `{"role":"user","content":[{"type":"text","text":"next"}],"timestamp":5}`
	wireImage    = `{"type":"image","data":"` + payloadOraclePNG + `","mimeType":"image/png"}`
	wireLook     = `{"role":"user","content":[` + wireImage + `,{"type":"text","text":"look"}],"timestamp":1}`
	wireNextLook = `{"role":"user","content":[{"type":"text","text":"next"},` + wireImage + `],"timestamp":5}`
)

// wireCall is a call of the read tool.
func wireCall(id string) string {
	return `{"type":"toolCall","id":"` + id + `","name":"read","arguments":{"path":"a.txt","offset":2}}`
}

// wireAssistant is an assistant turn of the named model.
func wireAssistant(api, provider, model, stop, blocks string) string {
	failure := ""
	if stop == "error" {
		failure = `"errorMessage":"boom",`
	}
	return `{"role":"assistant","content":[` + blocks + `],"api":"` + api + `","provider":"` + provider + `","model":"` + model + `",` + wireUsage + `,"stopReason":"` + stop + `",` + failure + `"timestamp":2}`
}

// wireResult is the result of a call of the read tool.
func wireResult(id, content string, isError bool) string {
	return fmt.Sprintf(`{"role":"toolResult","toolCallId":"%s","toolName":"read","content":[%s],"isError":%v,"timestamp":3}`, id, content, isError)
}

// wireContext is a Context with the given members before its messages.
func wireContext(members string, messages ...string) json.RawMessage {
	if members != "" {
		members += ","
	}
	return json.RawMessage("{" + members + `"messages":[` + strings.Join(messages, ",") + "]}")
}

// wirePoisoned is a history another model wrote: a signed thinking block, a call whose id no other provider accepts, then a failed turn and an aborted one.
func wirePoisoned(api, provider, model string) json.RawMessage {
	const id = "toolu_01|fc_1/x+y=="
	return wireContext(wireSystem+","+wireTools, wireUserGo,
		wireAssistant("anthropic-messages", "anthropic", "claude-other", "toolUse", `{"type":"thinking","thinking":"plan","thinkingSignature":"EqQB"},{"type":"text","text":"I will read."},`+wireCall(id)),
		wireResult(id, wireText+","+wireImage, false),
		wireAssistant(api, provider, model, "error", ``),
		wireAssistant(api, provider, model, "aborted", `{"type":"text","text":"partial"},`+wireCall("call_9")),
		wireUserNext)
}

type wireStream = func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error)

// providerWireStreams are the stream and streamSimple of each API kind a table drives.
var providerWireStreams = map[string][2]wireStream{
	"openai-completions":     {ai.StreamOpenAICompletions, ai.StreamSimpleOpenAICompletions},
	"openai-responses":       {ai.StreamOpenAIResponses, ai.StreamSimpleOpenAIResponses},
	"azure-openai-responses": {ai.StreamAzureOpenAIResponses, ai.StreamSimpleAzureOpenAIResponses},
	"mistral-conversations":  {ai.StreamMistral, ai.StreamSimpleMistral},
	"google-generative-ai":   {ai.StreamGoogle, ai.StreamSimpleGoogle},
	"google-vertex":          {ai.StreamGoogleVertex, ai.StreamSimpleGoogleVertex},
}

// firstBody keeps the body of the first request it is given and answers 400, so the stream ends with that request.
type firstBody struct{ body []byte }

func (c *firstBody) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Body != nil {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		if c.body == nil {
			c.body = body
		}
	}
	reply := `{"error":{"type":"invalid_request_error","message":"captured","code":400,"status":"INVALID_ARGUMENT"}}`
	return &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(reply)), Request: request}, nil
}

// wireMemberOrder is the member order of the object under name in raw: what a caller that decodes sampling parameters from JSON records
// beside the map (ai.Model.SamplingParamsOrder, ai.StreamOptions.SamplingParamsOrder).
func wireMemberOrder(t *testing.T, raw json.RawMessage, name string) []string {
	t.Helper()
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		t.Fatal(err)
	}
	if !orderedjson.IsObject(members[name]) {
		return nil
	}
	object, err := orderedjson.Parse(members[name])
	if err != nil {
		t.Fatal(err)
	}
	return object.Keys()
}

// providerWireBody is the body PiG posts for a row, and the error message the stream ends with.
func providerWireBody(t *testing.T, row providerWireRow) (body, failure string) {
	t.Helper()
	model := &ai.Model{ID: row.ID, DisplayName: row.ID, Input: []string{"text", "image"}, Capabilities: ai.ModelCapabilities{ContextWindow: 200000, MaxOutputTokens: 16384},
		ProviderMeta: ai.ProviderMetadata{API: ai.API(row.API), ProviderID: row.Provider, BaseURL: row.BaseURL, Reasoning: row.Reasoning}}
	if row.Compat != nil {
		model.ProviderMeta.Compat = &ai.ModelCompat{}
		if err := json.Unmarshal(row.Compat, model.ProviderMeta.Compat); err != nil {
			t.Fatal(err)
		}
	}
	if row.Model != nil {
		var members struct {
			SamplingParams ai.SamplingParams `json:"samplingParams"`
		}
		if err := json.Unmarshal(row.Model, &members); err != nil {
			t.Fatal(err)
		}
		model.SamplingParams = members.SamplingParams
		model.SamplingParamsOrder = wireMemberOrder(t, row.Model, "samplingParams")
	}
	var given struct {
		MaxTokens        int               `json:"maxTokens"`
		Temperature      *float64          `json:"temperature"`
		CacheRetention   ai.CacheRetention `json:"cacheRetention"`
		SessionID        string            `json:"sessionId"`
		Reasoning        ai.ThinkingLevel  `json:"reasoning"`
		ReasoningEffort  string            `json:"reasoningEffort"`
		ReasoningSummary string            `json:"reasoningSummary"`
		TextVerbosity    string            `json:"textVerbosity"`
		ServiceTier      string            `json:"serviceTier"`
		PromptMode       string            `json:"promptMode"`
		ToolChoice       json.RawMessage   `json:"toolChoice"`
		SamplingParams   ai.SamplingParams `json:"samplingParams"`
		Thinking         *struct {
			Enabled      bool   `json:"enabled"`
			Level        string `json:"level"`
			BudgetTokens *int   `json:"budgetTokens"`
		} `json:"thinking"`
	}
	if err := json.Unmarshal(row.Options, &given); err != nil {
		t.Fatal(err)
	}
	capture := &firstBody{}
	options := ai.StreamOptions{APIKey: row.APIKey, MaxRetries: new(int), Fetch: &http.Client{Transport: capture}, MaxTokens: given.MaxTokens, CacheRetention: given.CacheRetention,
		SessionID: given.SessionID, Thinking: given.Reasoning, ReasoningEffort: given.ReasoningEffort, ReasoningSummary: given.ReasoningSummary, TextVerbosity: given.TextVerbosity,
		ServiceTier: given.ServiceTier, PromptMode: given.PromptMode, SamplingParams: given.SamplingParams, SamplingParamsOrder: wireMemberOrder(t, row.Options, "samplingParams")}
	if given.Temperature != nil {
		options.Temperature, options.TemperatureSet = *given.Temperature, true
	}
	if given.Thinking != nil {
		options.GoogleThinking = &ai.GoogleThinkingOptions{Enabled: given.Thinking.Enabled, Level: ai.GoogleThinkingLevel(given.Thinking.Level), BudgetTokens: given.Thinking.BudgetTokens}
	}
	// A tool choice is a name, or the caller's object as the caller wrote it.
	if name := ""; json.Unmarshal(given.ToolChoice, &name) == nil {
		options.ToolChoice = name
	} else if given.ToolChoice != nil {
		options.ToolChoice = given.ToolChoice
	}
	streams, ok := providerWireStreams[row.API]
	if !ok {
		t.Fatalf("no stream for %s", row.API)
	}
	run := streams[0]
	if row.Simple {
		run = streams[1]
	}
	stream, err := run(payloadContext(t), model, payloadTranscript(t, row.Context), options)
	if err != nil {
		return string(capture.body), err.Error()
	}
	if result := stream.Result(); result != nil {
		failure = result.ErrorMessage
	}
	return string(capture.body), failure
}

// checkProviderWireRows compares the body PiG posts for each row with the body Pi's client hands to fetch, byte for byte, and for a row
// that ends before any request the message it ends with. JSON.stringify writes
// the members of each object in the order Pi's code created them (a literal's members as written, then the members assigned later); an object
// a model's catalog entry or a caller supplies keeps the order it was given in.
func checkProviderWireRows(t *testing.T, rows []providerWireRow) {
	t.Helper()
	want := piPayloads(t, "testdata/provider_wire_body.mjs", rows)
	for i, row := range rows {
		t.Run(row.Name, func(t *testing.T) {
			skipStrippedAPI(t, row.API)
			got, failure := providerWireBody(t, row)
			if message, none := strings.CutPrefix(want[i], "no request: "); none {
				// Pi ended the stream before any request: PiG sends none either and ends with the same message.
				if got != "" || failure != message {
					t.Errorf("Pi sent no request and ended with %q; PiG ended with %q and posted %q", message, failure, got)
				}
				return
			}
			if difference := wireDifference(got, want[i]); difference != "" {
				t.Error(difference)
			}
		})
	}
}
