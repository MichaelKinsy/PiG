package modelgen

// pi: packages/ai/catalog/sources/vercel-ai-gateway.ts

import (
	"bytes"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestGetAiGatewayCost(t *testing.T) {
	// upstream: packages/ai/test/model-cost-tiers.test.ts:68
	t.Run("turns bracket starts into tiers with the rates in effect there", func(t *testing.T) {
		requireJSON(t, GetAiGatewayCost(parseObj(t, `{"input":"0.000001","output":"0.000005","input_cache_read":"0.0000002",
			"input_tiers":[{"cost":"0.000001","min":0,"max":32001},{"cost":"0.0000018","min":32001,"max":128001},{"cost":"0.000003","min":128001}],
			"output_tiers":[{"cost":"0.000005","min":0,"max":32001},{"cost":"0.000009","min":32001,"max":128001},{"cost":"0.000015","min":128001}]}`)),
			`{"input":1,"output":5,"cacheRead":0.2,"cacheWrite":0,"tiers":[
				{"inputTokensAbove":32000,"input":1.8,"output":9,"cacheRead":0.2,"cacheWrite":0},
				{"inputTokensAbove":128000,"input":3,"output":15,"cacheRead":0.2,"cacheWrite":0}]}`)
	})
	// upstream: packages/ai/test/model-cost-tiers.test.ts:97
	t.Run("returns base rates without tiers", func(t *testing.T) {
		requireJSON(t, GetAiGatewayCost(parseObj(t, `{"input":"0.000003","output":0.000015}`)), `{"input":3,"output":15,"cacheRead":0,"cacheWrite":0}`)
	})
	// Source cases beyond the upstream test.
	t.Run("absent pricing is free", func(t *testing.T) {
		requireJSON(t, GetAiGatewayCost(nil), `{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}`)
	})
	// vercel-ai-gateway.ts sets inputTokensAbove to start - 1 as a number, so a fractional bracket start keeps its fraction.
	t.Run("keeps a fractional bracket start", func(t *testing.T) {
		requireJSON(t, GetAiGatewayCost(parseObj(t, `{"input":"0.000001","input_tiers":[{"cost":"0.000002","min":1000.5}]}`)).List("tiers"),
			`[{"inputTokensAbove":999.5,"input":2,"output":0,"cacheRead":0,"cacheWrite":0}]`)
	})
	t.Run("a bracket from zero adds no tier and a bracket without a cost keeps the base rate", func(t *testing.T) {
		requireJSON(t, GetAiGatewayCost(parseObj(t, `{"input":"0.000001","input_tiers":[{"cost":"0.000009","min":0,"max":10}],"output_tiers":[{"min":10}]}`)),
			`{"input":1,"output":0,"cacheRead":0,"cacheWrite":0,"tiers":[{"inputTokensAbove":9,"input":1,"output":0,"cacheRead":0,"cacheWrite":0}]}`)
	})
	t.Run("a non-numeric price is free", func(t *testing.T) {
		requireJSON(t, GetAiGatewayCost(parseObj(t, `{"input":"abc","output":"Infinity","input_cache_read":null,"input_cache_write":"0.0000000004"}`)),
			`{"input":0,"output":0,"cacheRead":0,"cacheWrite":0.0004}`)
	})
}

// fetchAiGatewayModels: a model needs the tool-use tag, vision adds image input, an evaluation model is a classifier on the TypeSafe endpoint, and the
// context window and max tokens default to 4096 when absent or zero. GetAiGatewayCost prices the chat model, including its prompt-length tiers.
func TestBuildAiGatewayCatalogConvertsChatAndEvaluationModels(t *testing.T) {
	catalog := BuildAiGatewayCatalog(parseObjs(t, `[
		{"id":"a/chat","name":"Chat","tags":["tool-use","vision","reasoning"],"context_window":200000,"max_tokens":8000,
		 "pricing":{"input":"0.000001","output":"0.000005","input_tiers":[{"cost":"0.000001","min":0,"max":32001},{"cost":"0.0000018","min":32001}]}},
		{"id":"a/untooled","tags":["vision"]},
		{"id":"a/notags"},
		{"id":"a/plain","tags":["tool-use"]},
		{"id":"t/jev","name":"Jev","type":"evaluation","context_window":0,"pricing":{"input":"0.000002","output":0.000004}}]`))
	if len(catalog.Chat) != 2 || len(catalog.Classifiers) != 1 {
		t.Fatalf("chat = %d, classifiers = %d, want 2 and 1", len(catalog.Chat), len(catalog.Classifiers))
	}
	requireJSON(t, catalog.Chat[0], `{"id":"a/chat","name":"Chat","api":"anthropic-messages","baseUrl":"https://ai-gateway.vercel.sh","provider":"vercel-ai-gateway","reasoning":true,"input":["text","image"],"compat":{"allowEmptySignature":true},
		"cost":{"input":1,"output":5,"cacheRead":0,"cacheWrite":0,"tiers":[{"inputTokensAbove":32000,"input":1.8,"output":5,"cacheRead":0,"cacheWrite":0}]},"contextWindow":200000,"maxTokens":8000}`)
	requireJSON(t, catalog.Chat[1], `{"id":"a/plain","name":"a/plain","api":"anthropic-messages","baseUrl":"https://ai-gateway.vercel.sh","provider":"vercel-ai-gateway","reasoning":false,"input":["text"],"compat":{"allowEmptySignature":true},
		"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":4096,"maxTokens":4096}`)
	requireJSON(t, catalog.Classifiers[0], `{"type":"classifier","id":"t/jev","name":"Jev","api":"typesafe-system-one","provider":"vercel-ai-gateway","baseUrl":"https://ai-gateway.vercel.sh/typesafe/v1","input":["text"],
		"cost":{"input":2,"output":4,"cacheRead":0,"cacheWrite":0},"contextWindow":4096}`)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

// serveDefaultTransport answers every request of http.DefaultClient with the given status and body for the test's duration.
func serveDefaultTransport(t *testing.T, status *int, body *string, wantURL string) {
	t.Helper()
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	http.DefaultTransport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if got := request.URL.String(); got != wantURL {
			t.Errorf("fetched %s, want %s", got, wantURL)
		}
		return &http.Response{StatusCode: *status, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader([]byte(*body))), Request: request}, nil
	})
}

// fetchAiGatewayModels: any 2xx is a listing (response.ok), anything else fails with the status, and a response whose data is not an array
// is an empty listing.
func TestFetchAiGatewayCatalog(t *testing.T) {
	var status int
	var body string
	serveDefaultTransport(t, &status, &body, "https://ai-gateway.vercel.sh/v1/models")

	status, body = http.StatusOK, `{"data":[{"id":"a/chat","tags":["tool-use"]}]}`
	if catalog, err := FetchAiGatewayCatalog(); err != nil || len(catalog.Chat) != 1 {
		t.Fatalf("listing: %d chat models, %v", len(catalog.Chat), err)
	}
	status, body = http.StatusOK, `{"data":{"not":"an array"}}`
	if catalog, err := FetchAiGatewayCatalog(); err != nil || len(catalog.Chat) != 0 || len(catalog.Classifiers) != 0 {
		t.Fatalf("non-array data: %+v, %v", catalog, err)
	}
	status, body = http.StatusCreated, `{"data":[{"id":"a/odd","tags":["tool-use"],"context_window":"large"},{"id":"a/chat","tags":["tool-use"]}]}`
	if catalog, err := FetchAiGatewayCatalog(); err != nil || len(catalog.Chat) != 2 || catalog.Chat[0].Num("contextWindow") != 4096 {
		t.Fatalf("2xx listing with a mistyped member: %d chat models, %v; want both listed (response.ok accepts any 2xx)", len(catalog.Chat), err)
	}
	status, body = http.StatusBadGateway, ``
	if _, err := FetchAiGatewayCatalog(); err == nil || err.Error() != "Vercel AI Gateway API returned 502" {
		t.Fatalf("failure = %v, want Vercel AI Gateway API returned 502", err)
	}
}

// BuildAiGatewayCatalog writes what vercel-ai-gateway.ts writes (`model.context_window || 4096`) only while the listing's windows
// are whole token counts. The snapshot is every evaluation or tool-use model of https://ai-gateway.vercel.sh/v1/models on 2026-10-13,
// trimmed to the members the source reads; the source copies a nonzero window unchanged.
func TestAiGatewayListingWindowsAreWholeTokenCounts(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "ai-gateway-listing-2026-10-13.json"))
	if err != nil {
		t.Fatal(err)
	}
	var items []*Obj
	for _, item := range parse(t, string(data)).(*Obj).List("data") {
		items = append(items, item.(*Obj))
	}
	catalog := BuildAiGatewayCatalog(items)
	if len(catalog.Chat) != 296 || len(catalog.Classifiers) != 7 {
		t.Fatalf("snapshot converts to %d chat and %d classifier models, want 296 and 7", len(catalog.Chat), len(catalog.Classifiers))
	}
	window := func(item *Obj, key string) float64 {
		value, present := item.Get(key)
		if !present {
			return 4096
		}
		count, isNumber := value.(float64)
		if !isNumber || count < 0 || count != math.Trunc(count) {
			t.Fatalf("%s %s = %v, not a whole token count", item.Str("id"), key, value)
		}
		if count == 0 {
			return 4096
		}
		return count
	}
	chat, classifiers := 0, 0
	for _, item := range items {
		if item.Str("type") == "evaluation" {
			if got, want := catalog.Classifiers[classifiers].Num("contextWindow"), window(item, "context_window"); got != want {
				t.Errorf("classifier %s contextWindow = %v, want %v", item.Str("id"), got, want)
			}
			classifiers++
			continue
		}
		model := catalog.Chat[chat]
		if got, want := [2]float64{model.Num("contextWindow"), model.Num("maxTokens")}, [2]float64{window(item, "context_window"), window(item, "max_tokens")}; got != want {
			t.Errorf("chat %s contextWindow/maxTokens = %v, want %v", item.Str("id"), got, want)
		}
		chat++
	}
}
