package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// anthSpliceStrings are texts around requestSpliceMinBytes that the encoder must carry through unchanged: markup and line separators JSON.stringify leaves alone, bytes that are not UTF-8, a lone surrogate, and text that looks like a token.
func anthSpliceStrings(random *rand.Rand) []string {
	atoms := []string{"func main() {\n\tfmt.Println(\"<hi> & bye\")\n}\n", "\u2028\u2029", "\x00\x01\x1f", "😀 café 漢字", "\\\"", "pig-splice-0-", `"pig-splice-deadbeef-0"`}
	pool := []string{"", "x", "short text"}
	for _, size := range []int{requestSpliceMinBytes - 1, requestSpliceMinBytes, requestSpliceMinBytes + 1, 3 * requestSpliceMinBytes} {
		var builder strings.Builder
		for builder.Len() < size {
			builder.WriteString(atoms[random.IntN(len(atoms))])
		}
		pool = append(pool, builder.String())
	}
	return pool
}

// anthPoisonedStrings are texts the body cannot hold as they are: a byte that is not UTF-8 (the cache declines it) and a lone surrogate, which the encoder writes as JSON.stringify does for the whole body.
var anthPoisonedStrings = []string{"\xff\xfe", "\xed\xa0\x80", strings.Repeat("a", requestSpliceMinBytes) + "\xff", strings.Repeat("b", requestSpliceMinBytes) + "\xed\xa0\x80"}

// anthSpliceRequest builds a request of the shapes the converter writes and the payload hook can return: every block kind, string and block tool results, images, cache_control, empty text, tools with recorded schema order, and betas.
func anthSpliceRequest(random *rand.Rand, pool []string) anthRequest {
	text := func() string { return pool[random.IntN(len(pool))] }
	head := func(n int) string { s := text(); return s[:min(n, len(s))] }
	cache := func() *anthCacheControl {
		switch random.IntN(3) {
		case 0:
			return &anthCacheControl{Type: "ephemeral"}
		case 1:
			return &anthCacheControl{Type: "ephemeral", TTL: "1h"}
		}
		return nil
	}
	tool := func() anthTool {
		return anthTool{
			Name:         "tool_" + head(3),
			Description:  text(),
			InputSchema:  map[string]any{"type": "object", "properties": map[string]any{"b": map[string]any{"type": "string", "description": text()}, "a": map[string]any{"type": "number"}}, "required": []any{"b"}},
			Strict:       map[int]*bool{0: new(true), 1: new(false), 2: nil}[random.IntN(3)],
			DeferLoading: random.IntN(4) == 0, CacheControl: cache(),
			inputSchemaOrder: []schemaObjectOrder{nil, {"": []string{"type", "properties", "required"}}}[random.IntN(2)],
		}
	}
	block := func() anthContentBlock {
		switch random.IntN(9) {
		case 0:
			return anthContentBlock{Type: "text", Text: text(), CacheControl: cache()}
		case 1:
			return anthContentBlock{Type: "thinking", Thinking: text(), Signature: text(), CacheControl: cache()}
		case 2:
			return anthContentBlock{Type: "redacted_thinking", Data: text()}
		case 3:
			return anthContentBlock{Type: "tool_use", ID: "toolu_1", Name: "bash", Input: map[string]any{"command": text(), "n": 1.5, "list": []any{text(), map[string]any{text(): text()}}}}
		case 4:
			return anthContentBlock{Type: "tool_result", ToolUseID: "toolu_1", Content: text(), IsError: map[int]*bool{0: new(true), 1: nil}[random.IntN(2)]}
		case 5:
			return anthContentBlock{Type: "tool_result", ToolUseID: "toolu_2", Content: []anthContentBlock{{Type: "text", Text: text()}, {Type: "image", Source: map[string]any{"type": "base64", "media_type": "image/png", "data": text()}}}}
		case 6:
			return anthContentBlock{Type: "image", Source: map[string]any{"type": "base64", "media_type": "image/png", "data": text()}, CacheControl: cache()}
		case 7:
			definition := tool()
			return anthContentBlock{Type: "tool_addition", Tool: &anthToolChange{Type: "tool_definition", Name: "extra", Definition: &definition}}
		}
		return anthContentBlock{Type: "tool_removal", Tool: &anthToolChange{Type: "tool_reference", Name: "extra"}}
	}
	request := anthRequest{Model: "claude", MaxTokens: 1 + random.IntN(4096)}
	for range 1 + random.IntN(6) {
		message := anthMessage{Role: []string{"user", "assistant"}[random.IntN(2)]}
		if random.IntN(3) == 0 {
			message.Content = text()
		} else {
			blocks := make([]anthContentBlock, 1+random.IntN(3))
			for i := range blocks {
				blocks[i] = block()
			}
			message.Content = blocks
		}
		if random.IntN(5) == 0 {
			message.OutputConfig = &anthOutputConfig{Effort: "high"}
		}
		request.Messages = append(request.Messages, message)
	}
	for range random.IntN(3) {
		request.System = append(request.System, anthSystemBlock{Type: "text", Text: text(), CacheControl: cache()})
	}
	for range random.IntN(3) {
		request.Tools = append(request.Tools, tool())
	}
	if random.IntN(2) == 0 {
		request.ToolChoice = map[string]any{"type": "auto"}
	}
	if random.IntN(2) == 0 {
		request.Metadata = &anthMetadata{UserID: text()}
	}
	if random.IntN(2) == 0 {
		request.Thinking = &anthThinking{Type: "enabled", BudgetTokens: 1024, Display: "summarized", BlockBinding: &anthBlockBinding{PrefixMismatchBehavior: "x"}}
	}
	if random.IntN(2) == 0 {
		request.OutputConfig = &anthOutputConfig{Effort: "medium"}
	}
	if random.IntN(2) == 0 {
		request.Temperature = new(0.5)
	}
	if random.IntN(3) == 0 {
		request.Fallbacks = []anthFallback{{Model: "other"}}
	}
	request.Stream = random.IntN(2) == 0
	switch random.IntN(4) {
	case 0:
		request.Betas = []string{}
	case 1:
		request.Betas = []string{"context-1m-2025-08-07", head(5)}
	}
	return request
}

// The request the provider builds is spliced by the encoder and must be the body anthropicWireBody writes for it, with the same anthropic-beta header, whether or not the cache holds its strings.
func TestSplicedAnthropicRequestIsIdenticalToTheWireBody(t *testing.T) {
	random := rand.New(rand.NewPCG(7, 11))
	clean := anthSpliceStrings(random)
	spliced := 0
	for trial := range 400 {
		pool := clean
		if trial%4 == 3 {
			pool = append(slices.Clone(clean), anthPoisonedStrings...)
		}
		request := anthSpliceRequest(random, pool)
		// The reference, written out: streaming on, betas out of the body and joined into the header, the body as JSON.stringify writes it.
		plain := request
		plain.Stream, plain.Betas = true, nil
		want, err := marshalRequestBody(plain)
		if err != nil {
			t.Fatal(err)
		}
		var wantBetas *string
		if request.Betas != nil {
			wantBetas = new(strings.Join(request.Betas, ","))
		}
		if viaWireBody, headerOfWireBody, err := anthropicWireBody(request); err != nil || !bytes.Equal(viaWireBody, want) || !reflect.DeepEqual(headerOfWireBody, wantBetas) {
			t.Fatalf("trial %d: anthropicWireBody differs from the reference: %v", trial, err)
		}
		wire, betas := request.wire()
		if !reflect.DeepEqual(betas, wantBetas) {
			t.Fatalf("trial %d: betas %v, want %v", trial, betas, wantBetas)
		}
		cache := newQuotedStringCache(1 << 30)
		for pass := range 2 {
			got, err := marshalSpliced(wire, cache, marshalRequestBody)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.segments) > 1 {
				spliced++
			}
			if joined := got.Bytes(); !bytes.Equal(joined, want) || got.size != len(want) {
				t.Fatalf("trial %d pass %d: spliced body differs from the wire body\n got %.300q\nwant %.300q", trial, pass, joined, want)
			}
		}
		if !json.Valid(want) {
			t.Fatalf("trial %d: not JSON", trial)
		}
	}
	// Trials that never splice would compare the fallback with itself.
	if spliced < 200 {
		t.Fatalf("only %d of 800 bodies took a cached encoding", spliced)
	}
}

// The encoder, not a copy of the request that holds tokens, writes the request: it takes the large strings of every block kind, in a request with a short history as in one with a long one.
func TestTheAnthropicRequestIsSplicedByTheEncoder(t *testing.T) {
	large := strings.Repeat("tool output <&>\n", 1000)
	request := anthRequest{Model: "m", MaxTokens: 1, Messages: []anthMessage{
		{Role: "user", Content: []anthContentBlock{
			{Type: "text", Text: large},
			{Type: "thinking", Thinking: large + "t", Signature: large + "s"},
			{Type: "redacted_thinking", Data: large + "r"},
			{Type: "tool_result", ToolUseID: "t", Content: large + "c"},
			{Type: "tool_use", ID: "u", Name: "n", Input: map[string]any{"k": large + "i"}},
		}},
		{Role: "user", Content: large + "m"},
	}, System: []anthSystemBlock{{Type: "text", Text: large + "y"}}, Tools: []anthTool{{Name: "n", Description: large + "d", InputSchema: map[string]any{"type": "object"}}}}
	wire, _ := request.wire()
	splice := &stringSplice{cache: newQuotedStringCache(1 << 30), nonce: newSpliceNonce()}
	skeleton, tokened, err := splice.encode(wire, marshalRequestBody)
	if err != nil || !tokened {
		t.Fatalf("the encoder did not take the request: tokened=%v err=%v", tokened, err)
	}
	if len(splice.quoted) != 9 || bytes.Contains(skeleton, []byte("tool output")) {
		t.Fatalf("%d of 9 large strings spliced; skeleton still holds text: %v", len(splice.quoted), bytes.Contains(skeleton, []byte("tool output")))
	}
}

// prepareRequest must not copy the request with tokens in it (the reflection walk): the history it sends is small strings only, which the walk finds nothing in, and on every turn it walked all of it.
func TestPrepareRequestDoesNotWalkTheRequest(t *testing.T) {
	provider := &anthropicProvider{cfg: AnthropicConfig{ProviderID: "anthropic"}}
	request := anthRequest{Model: "m", MaxTokens: 1, Betas: []string{"b1", "b2"}, Messages: []anthMessage{{Role: "user", Content: []anthContentBlock{{Type: "text", Text: strings.Repeat("x", requestSpliceMinBytes)}}}}}
	walkPlans.Delete(reflect.TypeFor[anthRequest]())
	out, err := provider.prepareRequest(context.Background(), "https://example.invalid", anthropicClient{apiKey: "k"}, request, &Model{}, StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, walked := walkPlans.Load(reflect.TypeFor[anthRequest]()); walked {
		t.Fatal("prepareRequest walked the request by reflection")
	}
	want, wantBetas, err := anthropicWireBody(request)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.body.Bytes(), want) || out.betas == nil || *out.betas != *wantBetas || *wantBetas != "b1,b2" {
		t.Fatalf("body or betas differ: betas=%v\n got %.200q\nwant %.200q", out.betas, out.body.Bytes(), want)
	}
}

// A replacement payload keeps the path it had: it is read as parameters (betas, user_profile_id and workspace_id leave the body), and is not spliced.
func TestPrepareRequestKeepsAReplacementPayloadsParameters(t *testing.T) {
	provider := &anthropicProvider{cfg: AnthropicConfig{ProviderID: "anthropic"}}
	large := strings.Repeat("big <&>", 1000)
	replacements := map[string]any{
		"map":      map[string]any{"model": "m", "stream": false, "betas": []any{"b1", "b2"}, "user_profile_id": "u", "workspace_id": "w", "messages": []any{map[string]any{"role": "user", "content": large}}},
		"raw":      json.RawMessage(`{"model":"m","betas":"one","messages":[]}`),
		"nobetas":  map[string]any{"model": "m", "messages": []any{}},
		"anthcopy": anthRequest{Model: "m", MaxTokens: 1, Betas: []string{"x"}, Messages: []anthMessage{{Role: "user", Content: large}}},
	}
	for name, replacement := range replacements {
		t.Run(name, func(t *testing.T) {
			out, err := provider.prepareRequest(context.Background(), "https://example.invalid", anthropicClient{apiKey: "k"}, anthRequest{Model: "orig"}, &Model{}, StreamOptions{OnPayload: func(any, *Model) (any, error) { return replacement, nil }})
			if err != nil {
				t.Fatal(err)
			}
			want, wantBetas, err := anthropicWireBody(replacement)
			if err != nil {
				t.Fatal(err)
			}
			var wantParams anthropicParamHeaders
			if _, own := replacement.(anthRequest); !own {
				if want, wantParams, err = anthropicSDKParams(want); err != nil {
					t.Fatal(err)
				}
			}
			if !bytes.Equal(out.body.Bytes(), want) || !reflect.DeepEqual(out.betas, wantBetas) || !reflect.DeepEqual(out.params, wantParams) {
				t.Fatalf("got betas=%v params=%v\n got %.200q\nwant %.200q", out.betas, out.params, out.body.Bytes(), want)
			}
		})
	}
}

// A payload hook that edits the request's messages in place, after the cache has seen them, gets the body of the edited request.
func TestPrepareRequestAfterAHookEditsCachedMessages(t *testing.T) {
	provider := &anthropicProvider{cfg: AnthropicConfig{ProviderID: "anthropic"}}
	request := anthHistory(10)
	send := func(opts StreamOptions) {
		t.Helper()
		out, err := provider.prepareRequest(context.Background(), "https://example.invalid", anthropicClient{apiKey: "k"}, request, &Model{}, opts)
		if err != nil {
			t.Fatal(err)
		}
		want, _, err := anthropicWireBody(request)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(out.body.Bytes(), want) {
			t.Fatalf("body differs from the wire body of the request as the hook left it\n got %.300q\nwant %.300q", out.body.Bytes(), want)
		}
	}
	send(StreamOptions{})
	send(StreamOptions{})
	send(StreamOptions{OnPayload: func(value any, _ *Model) (any, error) {
		blocks := value.(anthRequest).Messages[3].Content.([]anthContentBlock)
		blocks[0].Text = "edited by a hook"
		blocks[0].CacheControl = &anthCacheControl{Type: "ephemeral"}
		return nil, nil
	}})
	send(StreamOptions{})
}
