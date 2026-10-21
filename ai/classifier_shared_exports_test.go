package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
)

// packages/ai/src/api/classifier-shared.ts isRecord: a non-null, non-array object.
func TestIsRecordIsAnObjectButNotNullOrAnArray(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{`{}`, true},
		{`{"a":1}`, true},
		{`null`, false},
		{`[]`, false},
		{`[{"a":1}]`, false},
		{`"text"`, false},
		{`1`, false},
		{`true`, false},
	} {
		if got := IsRecord(json.RawMessage(tc.value)); got != tc.want {
			t.Errorf("IsRecord(%s) = %v, want %v", tc.value, got, tc.want)
		}
	}
}

// packages/ai/src/api/system-one-shared.ts requiredNumber: a finite JSON number, else "<label> returned an invalid <field>".
func TestRequiredNumberAcceptsOnlyAFiniteNumber(t *testing.T) {
	for _, value := range []string{`0`, `-2.5`, `0.95`, `1e3`} {
		got, err := RequiredNumber("System One API", json.RawMessage(value), "score for q")
		if err != nil {
			t.Errorf("RequiredNumber(%s): %v", value, err)
			continue
		}
		var want float64
		if err := json.Unmarshal([]byte(value), &want); err != nil || got != want {
			t.Errorf("RequiredNumber(%s) = %v, want %v", value, got, want)
		}
	}
	for _, value := range []string{`"5"`, `null`, `true`, `[1]`, `{}`, ``} {
		_, err := RequiredNumber("Label X", json.RawMessage(value), "confidence for q")
		if err == nil || err.Error() != "Label X returned an invalid confidence for q" {
			t.Errorf("RequiredNumber(%q) error = %v, want %q", value, err, "Label X returned an invalid confidence for q")
		}
	}
}

// packages/ai/src/api/classifier-shared.ts parseClassifierUsage: { input_tokens, output_tokens } priced from the model; a missing
// or malformed object has no usage; a negative, non-finite or non-numeric count is zero.
func TestParseClassifierUsagePricesTheModelCatalog(t *testing.T) {
	model := ClassifierModel{ID: "m", API: "typesafe-system-one", Provider: "typesafe"}
	model.Cost.Input, model.Cost.Output = 1_000_000, 2_000_000
	for _, tc := range []struct {
		name, raw string
		want      *[3]int
	}{
		{"absent", ``, nil},
		{"null", `null`, nil},
		{"an array", `[1]`, nil},
		{"neither count", `{"cost":1}`, nil},
		{"input only", `{"input_tokens":5}`, &[3]int{5, 0, 5}},
		{"output only", `{"output_tokens":7}`, &[3]int{0, 7, 7}},
		{"both", `{"input_tokens":5,"output_tokens":7}`, &[3]int{5, 7, 12}},
		{"negative and non-numeric are zero", `{"input_tokens":-3,"output_tokens":"7"}`, &[3]int{0, 0, 0}},
		{"explicit null counts are zero", `{"input_tokens":null,"output_tokens":4}`, &[3]int{0, 4, 4}},
		// tokenCount keeps the number: a count past int32 is the count, and every integer a JS number holds exactly is kept exactly.
		{"a count above 2^31-1", `{"input_tokens":3000000000,"output_tokens":7}`, &[3]int{3_000_000_000, 7, 3_000_000_007}},
		{"Number.MAX_SAFE_INTEGER", `{"input_tokens":9007199254740991,"output_tokens":0}`, &[3]int{9_007_199_254_740_991, 0, 9_007_199_254_740_991}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usage := ParseClassifierUsage(json.RawMessage(tc.raw), model)
			if tc.want == nil {
				if usage != nil {
					t.Fatalf("usage = %+v, want none", usage)
				}
				return
			}
			if usage == nil || [3]int{usage.Input, usage.Output, usage.TotalTokens} != *tc.want {
				t.Fatalf("usage = %+v, want %v", usage, *tc.want)
			}
			if usage.CacheRead != 0 || usage.CacheWrite != 0 {
				t.Fatalf("cache usage = %d/%d, want 0/0", usage.CacheRead, usage.CacheWrite)
			}
			if want := float64(tc.want[0]) + 2*float64(tc.want[1]); usage.Cost.Total != want {
				t.Fatalf("cost = %v, want %v", usage.Cost.Total, want)
			}
		})
	}
}

// packages/ai/src/api/classifier-shared.ts postClassifierRequest.
func TestPostClassifierRequestSendsBearerAuthAndTheJSONPayload(t *testing.T) {
	model := ClassifierModel{ID: "m", Provider: "acme", Headers: map[string]string{"X-Model": "m1", "Authorization": "model-auth"}}
	var seen *http.Request
	var sent string
	body, err := PostClassifierRequest(t.Context(), "Acme", "https://classify.test/run", model, map[string]any{"q": 1}, ClassifierOptions{
		APIKey:  "secret",
		Headers: ProviderHeaders{"X-Call": func() *string { s := "c1"; return &s }()},
		Fetch: clsClient(func(request *http.Request) (*http.Response, error) {
			seen = request
			data, _ := io.ReadAll(request.Body)
			sent = string(data)
			return clsJSON(200, `{"ok":true}`), nil
		}),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("body = %s", body)
	}
	if seen.Method != http.MethodPost || seen.URL.String() != "https://classify.test/run" {
		t.Fatalf("request = %s %s", seen.Method, seen.URL)
	}
	if sent != `{"q":1}` {
		t.Fatalf("sent %s, want {\"q\":1}", sent)
	}
	// Bearer auth, then model headers, then call headers; a later source replaces an earlier one case-insensitively.
	if got := seen.Header.Get("Authorization"); got != "model-auth" {
		t.Errorf("authorization = %q, want the model header that replaces the bearer", got)
	}
	if got := seen.Header.Get("X-Model"); got != "m1" {
		t.Errorf("x-model = %q", got)
	}
	if got := seen.Header.Get("X-Call"); got != "c1" {
		t.Errorf("x-call = %q", got)
	}
	if got := seen.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("content-type = %q", got)
	}
}

func TestPostClassifierRequestAuthorizesWithTheBearerKeyUnlessTheModelOverridesIt(t *testing.T) {
	var authorization string
	options := ClassifierOptions{APIKey: "secret", Fetch: clsClient(func(request *http.Request) (*http.Response, error) {
		authorization = request.Header.Get("Authorization")
		return clsJSON(200, `{}`), nil
	})}
	if _, err := PostClassifierRequest(t.Context(), "Acme", "https://classify.test/run", ClassifierModel{Provider: "acme"}, map[string]any{}, options, nil); err != nil {
		t.Fatal(err)
	}
	if authorization != "Bearer secret" {
		t.Fatalf("authorization = %q, want %q", authorization, "Bearer secret")
	}
}

func TestPostClassifierRequestWithoutAnAPIKeyFailsBeforeSending(t *testing.T) {
	called := false
	_, err := PostClassifierRequest(t.Context(), "Acme", "https://classify.test/run", ClassifierModel{Provider: "acme"}, map[string]any{}, ClassifierOptions{
		Fetch: clsClient(func(*http.Request) (*http.Response, error) { called = true; return clsJSON(200, `{}`), nil }),
	}, nil)
	if err == nil || err.Error() != "No API key for provider: acme" {
		t.Fatalf("error = %v, want %q", err, "No API key for provider: acme")
	}
	if called {
		t.Fatal("a request was sent without an API key")
	}
}

func TestPostClassifierRequestNoRetryStatusFailsAtOnceAndOtherStatusesRetry(t *testing.T) {
	retries := 3
	delay := 0
	options := func(calls *int, status int) ClassifierOptions {
		return ClassifierOptions{APIKey: "k", MaxRetries: &retries, MaxRetryDelayMs: &delay, Fetch: clsClient(func(*http.Request) (*http.Response, error) {
			*calls++
			return clsText(status, "nope", nil), nil
		})}
	}
	var noRetry, retried int
	_, err := PostClassifierRequest(t.Context(), "Acme", "https://classify.test/run", ClassifierModel{Provider: "acme"}, map[string]any{}, options(&noRetry, 504), []int{504})
	if err == nil || !strings.Contains(err.Error(), "Acme returned 504") || noRetry != 1 {
		t.Fatalf("504 in noRetryStatuses: calls=%d error=%v, want one call failing with %q", noRetry, err, "Acme returned 504")
	}
	_, err = PostClassifierRequest(t.Context(), "Acme", "https://classify.test/run", ClassifierModel{Provider: "acme"}, map[string]any{}, options(&retried, 503), []int{504})
	if err == nil || !strings.Contains(err.Error(), "Acme returned 503") || retried != retries+1 {
		t.Fatalf("503 outside noRetryStatuses: calls=%d error=%v, want %d calls", retried, err, retries+1)
	}
}

func TestPostClassifierRequestRejectsABodyThatIsNotJSONWithV8sSyntaxError(t *testing.T) {
	_, err := PostClassifierRequest(t.Context(), "Acme", "https://classify.test/run", ClassifierModel{Provider: "acme"}, map[string]any{}, ClassifierOptions{
		APIKey: "k", MaxRetries: new(int),
		Fetch: clsClient(func(*http.Request) (*http.Response, error) { return clsJSON(200, `not json`), nil }),
	}, nil)
	const want = `Unexpected token 'o', "not json" is not valid JSON`
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

// `await response.json()` drops a UTF-8 byte order mark before parsing.
func TestPostClassifierRequestParsesABodyThatStartsWithAByteOrderMark(t *testing.T) {
	body, err := PostClassifierRequest(t.Context(), "Acme", "https://classify.test/run", ClassifierModel{Provider: "acme"}, map[string]any{}, ClassifierOptions{
		APIKey: "k", MaxRetries: new(int),
		Fetch: clsClient(func(*http.Request) (*http.Response, error) { return clsJSON(200, "\xef\xbb\xbf{\"ok\":1}"), nil }),
	}, nil)
	if err != nil || string(body) != `{"ok":1}` {
		t.Fatalf("body = %q error = %v, want {\"ok\":1}", body, err)
	}
}

// packages/ai/src/api/system-one-shared.ts classifySystemOne over a caller-supplied transport.
func TestClassifySystemOneRunsTheTransportEnvelope(t *testing.T) {
	model := typesafeTestModel()
	model.API = "acme-system-one"
	var url, sent string
	transport := SystemOneTransport{
		API:   "acme-system-one",
		Label: "Acme One",
		URL:   func(m ClassifierModel) string { return "https://acme.test/" + m.ID },
		Payload: func(m ClassifierModel, r SystemOneWireRequest) map[string]any {
			return map[string]any{"wrapped": map[string]any{"model": m.ID, "state": r.State, "questions": r.Questions}}
		},
		Output: func(body json.RawMessage) (map[string]json.RawMessage, error) {
			var envelope struct {
				Result map[string]json.RawMessage `json:"result"`
			}
			return envelope.Result, json.Unmarshal(body, &envelope)
		},
	}
	result := ClassifySystemOne(t.Context(), transport, model, typesafeTestContext(), ClassifierOptions{APIKey: "k", Fetch: clsClient(func(request *http.Request) (*http.Response, error) {
		url = request.URL.String()
		data, _ := io.ReadAll(request.Body)
		sent = string(data)
		return clsJSON(200, `{"result":{"answers":`+typesafeWireAnswers+`,"usage":{"input_tokens":3,"output_tokens":4}}}`), nil
	})})
	if result.StopReason != "stop" || result.ErrorMessage != "" {
		t.Fatalf("result = %+v", result)
	}
	if url != "https://acme.test/jev-latest" {
		t.Fatalf("url = %s", url)
	}
	if !strings.HasPrefix(sent, `{"wrapped":{"model":"jev-latest"`) {
		t.Fatalf("payload = %s, want the transport envelope", sent)
	}
	if result.Usage == nil || result.Usage.Input != 3 || result.Usage.Output != 4 {
		t.Fatalf("usage = %+v", result.Usage)
	}
	if len(result.Answers) != 3 {
		t.Fatalf("answers = %+v, want the three question answers", result.Answers)
	}
}

func TestClassifySystemOneRejectsAnotherAPIAndImagesWithoutSending(t *testing.T) {
	transport := typesafeSystemOneTransport
	transport.Label = "Acme One"
	called := false
	options := ClassifierOptions{APIKey: "k", Fetch: clsClient(func(*http.Request) (*http.Response, error) { called = true; return clsJSON(200, `{}`), nil })}

	other := typesafeTestModel()
	other.API = "cloudflare-workers-ai-system-one"
	result := ClassifySystemOne(t.Context(), transport, other, typesafeTestContext(), options)
	if result.StopReason != "error" || result.ErrorMessage != "Unsupported classifier API: cloudflare-workers-ai-system-one" {
		t.Fatalf("result = %+v, want the unsupported API error", result)
	}

	withImages := typesafeTestContext()
	withImages.Images = []ImageContent{{Data: "aGk=", MimeType: "image/png"}}
	result = ClassifySystemOne(t.Context(), transport, typesafeTestModel(), withImages, options)
	if result.StopReason != "error" || result.ErrorMessage != "Acme One does not support image input for jev-latest" {
		t.Fatalf("result = %+v, want the image error", result)
	}
	if called {
		t.Fatal("a rejected request was sent")
	}
}

func TestClassifySystemOneReportsTheBilledUsageOfMalformedAnswers(t *testing.T) {
	model := typesafeTestModel()
	model.Cost.Input = 1_000_000
	result := ClassifySystemOne(t.Context(), typesafeSystemOneTransport, model, typesafeTestContext(), ClassifierOptions{APIKey: "k", Fetch: clsClient(func(*http.Request) (*http.Response, error) {
		return clsJSON(200, `{"answers":{},"usage":{"input_tokens":9}}`), nil
	})})
	if result.StopReason != "error" || !strings.Contains(result.ErrorMessage, "did not return an answer for category") {
		t.Fatalf("result = %+v", result)
	}
	if result.Usage == nil || result.Usage.Input != 9 || result.Usage.Cost.Total != 9 {
		t.Fatalf("usage = %+v, want the billed usage kept on the failed result", result.Usage)
	}
}

// The lazy API modules (api/*.lazy.ts) each hand classify to their own implementation: the first request each one makes
// goes to that service's endpoint, so a module wired to another classifier would hit another path.
func TestClassifierAPIModulesDispatchToTheirOwnClassifier(t *testing.T) {
	for _, tc := range []struct {
		name   string
		module *ProviderClassifier
		api    ClassifierAPI
		suffix string
	}{
		{"OpenAIDecisionsAPI", OpenAIDecisionsAPI(), ClassifierAPIOpenAIDecisions, "/decisions"},
		{"TypesafeSystemOneAPI", TypesafeSystemOneAPI(), ClassifierAPITypesafeSystemOne, "/systemone"},
		{"CloudflareWorkersAISystemOneAPI", CloudflareWorkersAISystemOneAPI(), ClassifierAPICloudflareWorkersAISystemOne, "/run"},
		{"LlamaCppClassifyAPI", LlamaCppClassifyAPI(), ClassifierAPILlamaCppClassify, "/apply-template"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := typesafeTestModel()
			model.API = tc.api
			model.BaseURL = "https://classify.test/v1"
			// The llama.cpp classifier issues its tokenize and apply-template requests concurrently (Promise.all), so the paths are collected under a lock and any one of them may come first.
			var mu sync.Mutex
			var paths []string
			result, err := tc.module.Classify(t.Context(), &model, typesafeTestContext(), ClassifierOptions{APIKey: "k", MaxRetries: new(int), Fetch: clsClient(func(request *http.Request) (*http.Response, error) {
				mu.Lock()
				paths = append(paths, request.URL.Path)
				mu.Unlock()
				return clsText(500, "boom", nil), nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if !slices.ContainsFunc(paths, func(path string) bool { return strings.HasSuffix(path, tc.suffix) }) {
				t.Fatalf("request paths = %q, want one to end in %q", paths, tc.suffix)
			}
			if result.StopReason != "error" || result.API != tc.api || result.Model != model.ID {
				t.Fatalf("result = %+v, want a failed %s result for %s", result, tc.api, model.ID)
			}
		})
	}
}

// types.ts ClassifierFunction: (model, context, options) => Promise<ClassifierResult>. A ProviderClassifier built from one hands
// it the caller's model, context and options unchanged, receives the result it returns, and the function cannot rewrite the caller's model.
func TestClassifierFunctionReceivesTheCallersArgumentsAndItsResultIsReturned(t *testing.T) {
	type contextKey struct{}
	ctx := context.WithValue(t.Context(), contextKey{}, "caller")
	model := typesafeTestModel()
	request := typesafeTestContext()
	temperature := 0.5
	options := ClassifierOptions{APIKey: "key-1", Temperature: &temperature}

	var seenModel ClassifierModel
	var seenRequest ClassifierContext
	var seenOptions ClassifierOptions
	var seenCaller any
	var classify ClassifierFunction = func(ctx context.Context, m ClassifierModel, r ClassifierContext, o ClassifierOptions) ClassifierResult {
		seenCaller, seenModel, seenRequest, seenOptions = ctx.Value(contextKey{}), m, r, o
		m.ID = "rewritten"
		return ClassifierResult{API: m.API, Provider: m.Provider, Model: "result-model", StopReason: ClassifierStopReasonStop}
	}
	result, err := classifierModule(classify).Classify(ctx, &model, request, options)
	if err != nil {
		t.Fatal(err)
	}
	if seenCaller != "caller" {
		t.Errorf("function context value = %v, want the caller's context", seenCaller)
	}
	if seenModel.ID != "jev-latest" || seenModel.Provider != "typesafe" {
		t.Errorf("function model = %+v, want the caller's model", seenModel)
	}
	if len(seenRequest.Questions) != len(request.Questions) || seenRequest.State["text"] != request.State["text"] {
		t.Errorf("function request = %+v, want the caller's context", seenRequest)
	}
	if seenOptions.APIKey != "key-1" || seenOptions.Temperature == nil || *seenOptions.Temperature != 0.5 {
		t.Errorf("function options = %+v, want the caller's options", seenOptions)
	}
	if result.Model != "result-model" || result.StopReason != ClassifierStopReasonStop {
		t.Errorf("result = %+v, want the function's result returned as is", result)
	}
	if model.ID != "jev-latest" {
		t.Errorf("caller model ID = %q, want the function's rewrite not to reach the caller", model.ID)
	}
}
