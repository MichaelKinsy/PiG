package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
	"unsafe"
)

// mutation changes a zero value of a type (settable, addressable) in one place the encoding can read, and base makes the value the change is made in: the same containers with nothing changed inside them, so the record of the changed value must differ from the record of the base.
type mutation struct{ apply, base func(reflect.Value) }

func noop(reflect.Value) {}

// mutations returns the mutations of type t: each field (the unexported ones too), a pointer's presence, an element, a nil slice becoming an empty one. Interface fields get each shape the converters and payload hooks put in one.
func mutations(t reflect.Type) []mutation { return mutationsAt(t, 0) }

// mutationsAt is mutations inside depth interfaces that hold a slice of blocks or parts, whose own fields are changed down to two levels.
func mutationsAt(t reflect.Type, depth int) []mutation {
	var out []mutation
	leaf := func(apply func(reflect.Value)) { out = append(out, mutation{apply, noop}) }
	switch t.Kind() {
	case reflect.String:
		leaf(func(v reflect.Value) { v.SetString("x") })
	case reflect.Bool:
		leaf(func(v reflect.Value) { v.SetBool(true) })
	case reflect.Int, reflect.Int64:
		leaf(func(v reflect.Value) { v.SetInt(7) })
	case reflect.Float64:
		leaf(func(v reflect.Value) { v.SetFloat(1.5) })
	case reflect.Interface:
		for _, candidate := range []any{"x", true, 1.5, 7, json.RawMessage(`{"a":1}`), map[string]any{"a": "b"}, []any{"x"}, []anthContentBlock{{Type: "x"}}, []anthContentBlock{}, []oaiContentPart{{Type: "x"}}, []oaiContentPart{}} {
			leaf(func(v reflect.Value) { v.Set(reflect.ValueOf(candidate)) })
		}
		if depth < 2 {
			for _, element := range []reflect.Type{reflect.TypeFor[anthContentBlock](), reflect.TypeFor[oaiContentPart]()} {
				for _, inner := range mutationsAt(element, depth+1) {
					wrap := func(change func(reflect.Value)) func(reflect.Value) {
						return func(v reflect.Value) {
							blocks := reflect.MakeSlice(reflect.SliceOf(element), 1, 1)
							change(blocks.Index(0))
							v.Set(blocks)
						}
					}
					out = append(out, mutation{wrap(inner.apply), wrap(inner.base)})
				}
			}
		}
	case reflect.Pointer:
		leaf(func(v reflect.Value) { v.Set(reflect.New(t.Elem())) })
		for _, inner := range mutationsAt(t.Elem(), depth) {
			wrap := func(change func(reflect.Value)) func(reflect.Value) {
				return func(v reflect.Value) {
					pointer := reflect.New(t.Elem())
					change(pointer.Elem())
					v.Set(pointer)
				}
			}
			out = append(out, mutation{wrap(inner.apply), wrap(inner.base)})
		}
	case reflect.Slice:
		leaf(func(v reflect.Value) { v.Set(reflect.MakeSlice(t, 0, 0)) })
		for _, inner := range mutationsAt(t.Elem(), depth) {
			wrap := func(change func(reflect.Value)) func(reflect.Value) {
				return func(v reflect.Value) {
					v.Set(reflect.MakeSlice(t, 1, 1))
					change(v.Index(0))
				}
			}
			out = append(out, mutation{wrap(inner.apply), wrap(inner.base)})
		}
		if t.Elem().Kind() == reflect.Uint8 {
			leaf(func(v reflect.Value) { v.SetBytes([]byte("x")) })
		}
	case reflect.Map:
		leaf(func(v reflect.Value) { v.Set(reflect.MakeMap(t)) })
		for _, inner := range mutationsAt(t.Elem(), depth) {
			wrap := func(change func(reflect.Value)) func(reflect.Value) {
				return func(v reflect.Value) {
					v.Set(reflect.MakeMap(t))
					element := reflect.New(t.Elem()).Elem()
					change(element)
					v.SetMapIndex(reflect.ValueOf("k").Convert(t.Key()), element)
				}
			}
			out = append(out, mutation{wrap(inner.apply), wrap(inner.base)})
		}
	case reflect.Struct:
		for i := range t.NumField() {
			for _, inner := range mutationsAt(t.Field(i).Type, depth) {
				wrap := func(change func(reflect.Value)) func(reflect.Value) {
					return func(v reflect.Value) {
						// An unexported field is reached through its address.
						field := v.Field(i)
						change(reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem()) //nolint:gosec // G103: a test reaches an unexported field of a value it owns
					}
				}
				out = append(out, mutation{wrap(inner.apply), wrap(inner.base)})
			}
		}
	}
	return out
}

// A cached encoding must not be served to a message that differs in a value the encoding reads. For every way a message differs from the empty one, in every field (the unexported ones too) and every shape of an interface field, the record must differ or the message must be one the record refuses.
func TestMessageKeysReadEveryField(t *testing.T) {
	check := func(t *testing.T, name string, typ reflect.Type, key func(*messageKey, any) bool) {
		record := func(build func(reflect.Value)) ([]byte, bool) {
			value := reflect.New(typ).Elem()
			build(value)
			var k messageKey
			ok := key(&k, value.Addr().Interface())
			return k.buf, ok
		}
		if _, ok := record(noop); !ok {
			t.Fatalf("%s: the empty message has no record", name)
		}
		for n, mutate := range mutations(typ) {
			changed, ok := record(mutate.apply)
			if !ok {
				continue
			}
			base, _ := record(mutate.base)
			if bytes.Equal(changed, base) {
				value := reflect.New(typ).Elem()
				mutate.apply(value)
				t.Errorf("%s: mutation %d (%+v) has the record of the message without it", name, n, value.Interface())
			}
		}
	}
	t.Run("anthropic", func(t *testing.T) {
		check(t, "anthMessage", reflect.TypeFor[anthMessage](), func(k *messageKey, m any) bool { return anthMessageKey(k, m.(*anthMessage)) })
	})
	t.Run("completions", func(t *testing.T) {
		check(t, "oaiMessage", reflect.TypeFor[oaiMessage](), func(k *messageKey, m any) bool { return oaiMessageKey(k, m.(*oaiMessage)) })
	})
	// The records of two different messages must differ where the text moves from one field to the next, for fields written one after the other with nothing between them.
	var a, b messageKey
	block := func(text, id string) []anthContentBlock {
		return []anthContentBlock{{Type: "text", Text: text, ID: id}}
	}
	anthMessageKey(&a, &anthMessage{Role: "user", Content: block("ab", "c")})
	anthMessageKey(&b, &anthMessage{Role: "user", Content: block("a", "bc")})
	if bytes.Equal(a.buf, b.buf) {
		t.Error("the anthropic record does not tell where a field ends")
	}
	a.reset()
	b.reset()
	oaiMessageKey(&a, &oaiMessage{Role: "tool", ToolCallID: "ab", ToolName: "c"})
	oaiMessageKey(&b, &oaiMessage{Role: "tool", ToolCallID: "a", ToolName: "bc"})
	if bytes.Equal(a.buf, b.buf) {
		t.Error("the completions record does not tell where a field ends")
	}
}

// The same history sent again is not encoded again, and a message that changed, however slightly, is.
func TestTheMessageCacheServesOnlyTheMessageItEncoded(t *testing.T) {
	cache := newQuotedStringCache(1 << 30)
	send := func(request any) []byte {
		t.Helper()
		body, err := marshalSpliced(request, cache, marshalRequestBody)
		if err != nil {
			t.Fatal(err)
		}
		want, err := marshalRequestBody(request)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(body.Bytes(), want) {
			t.Fatalf("body differs from marshalRequestBody\n got %.300q\nwant %.300q", body.Bytes(), want)
		}
		return body.Bytes()
	}
	request := anthHistory(20)
	messages := len(request.Messages)
	send(request)
	if cache.messages.misses != messages || cache.messages.hits != 0 {
		t.Fatalf("first build: %d misses, %d hits, want %d misses", cache.messages.misses, cache.messages.hits, messages)
	}
	send(request)
	if cache.messages.misses != messages || cache.messages.hits != messages {
		t.Fatalf("second build: %d misses, %d hits, want %d hits and no new miss", cache.messages.misses, cache.messages.hits, messages)
	}
	// The conversion changed one message in place of a cached one (a cache_control moved, another text, another tool input).
	changed := anthHistory(20)
	changed.Messages[7].Content.([]anthContentBlock)[0].CacheControl = &anthCacheControl{Type: "ephemeral"}
	changed.Messages[31].Content.([]anthContentBlock)[1].Input = json.RawMessage(`{"path":"/other"}`)
	send(changed)
	if cache.messages.misses != messages+2 {
		t.Fatalf("a changed message was not encoded: %d misses, want %d", cache.messages.misses, messages+2)
	}
	completions := oaiHistory(20)
	send(completions)
	send(completions)
	completions.Messages[5].Content = "another"
	send(completions)
}

// A cache that holds the history of many requests, each one message different from the last, serves every request the body it would be without the cache: for every kind of block and message, with texts that are large, empty, not UTF-8 or look like tokens.
func TestMessageCacheBodiesAreIdenticalOverAChangingHistory(t *testing.T) {
	random := rand.New(rand.NewPCG(3, 5))
	clean := anthSpliceStrings(random)
	cache := newQuotedStringCache(1 << 30)
	send := func(request any) {
		t.Helper()
		body, err := marshalSpliced(request, cache, marshalRequestBody)
		if err != nil {
			t.Fatal(err)
		}
		want, err := marshalRequestBody(request)
		if err != nil {
			t.Fatal(err)
		}
		if joined := body.Bytes(); !bytes.Equal(joined, want) || body.size != len(want) {
			t.Fatalf("body differs from marshalRequestBody\n got %.300q\nwant %.300q", joined, want)
		}
	}
	for trial := range 60 {
		pool := clean
		if trial%5 == 4 {
			pool = append(append([]string{}, clean...), anthPoisonedStrings...)
		}
		request := anthSpliceRequest(random, pool)
		request.Betas = nil
		// A long history, which later requests extend, reorder and edit.
		for range 30 {
			request.Messages = append(request.Messages, anthSpliceRequest(random, pool).Messages...)
		}
		for step := range 12 {
			send(request)
			switch step % 4 {
			case 0: // another model, tool, or message appended
				request.Messages = append(request.Messages, anthSpliceRequest(random, pool).Messages[0])
			case 1: // an edit in place of one message
				request.Messages[random.IntN(len(request.Messages))] = anthSpliceRequest(random, pool).Messages[0]
			case 2: // a compaction: the head is replaced
				request.Messages = append([]anthMessage{anthSpliceRequest(random, pool).Messages[0]}, request.Messages[len(request.Messages)/2:]...)
			default: // a branch: the tail is dropped
				request.Messages = request.Messages[:len(request.Messages)*3/4]
			}
		}
	}
	completions := func() oaiRequest {
		request := oaiRequest{Model: "m"}
		for range 1 + random.IntN(20) {
			text := clean[random.IntN(len(clean))]
			switch random.IntN(6) {
			case 0:
				request.Messages = append(request.Messages, oaiMessage{Role: "tool", ToolCallID: "c", Content: text})
			case 1:
				request.Messages = append(request.Messages, oaiMessage{Role: "user", Content: []oaiContentPart{{Type: "text", Text: &text}, {Type: "image_url", ImageURL: &struct {
					URL string `json:"url"`
				}{URL: text}, CacheControl: &oaiCacheControl{Type: "ephemeral"}}}})
			case 2:
				request.Messages = append(request.Messages, oaiMessage{Role: "assistant", Content: text, Reasoning: text, ReasoningContent: &text, ToolCalls: []oaiRequestToolCall{{ID: "i", Type: "function", Function: &oaiRequestToolCallFunction{Name: "n", Arguments: text}}, {ID: "j", Type: "custom", Custom: &oaiRequestToolCallCustom{Name: "g", Input: text}}}})
			case 3:
				request.Messages = append(request.Messages, oaiMessage{Role: "system", Content: text, Tools: []oaiTool{{Type: "function", Function: &oaiToolFunction{Name: "f", Description: text, Parameters: map[string]any{"type": "object", "x": []any{1.5, "y", nil, true}}, parameterOrder: schemaObjectOrder{"": {"x", "type"}}}}, {Type: "custom", Custom: &oaiCustomTool{Name: "g", Description: text}}}})
			case 4:
				request.Messages = append(request.Messages, oaiMessage{Role: "assistant", ReasoningDetails: []json.RawMessage{json.RawMessage(`{"a":"<b>"}`), nil}})
			default:
				request.Messages = append(request.Messages, oaiMessage{Role: "user", Content: text})
			}
		}
		return request
	}
	request := completions()
	for step := range 200 {
		send(request)
		if step%10 == 9 {
			request = completions()
		} else {
			request.Messages[random.IntN(len(request.Messages))] = completions().Messages[0]
		}
	}
}

// A message that holds a large string, or that is not UTF-8 clean, is not cached whole: the string cache keys the string, and the whole body deals with a lone surrogate.
func TestTheMessageCacheLeavesLargeAndPoisonedMessages(t *testing.T) {
	cache := newQuotedStringCache(1 << 30)
	large := strings.Repeat("big <&>\n", requestSpliceMinBytes/4)
	request := anthRequest{Model: "m", Messages: []anthMessage{
		{Role: "user", Content: large},
		{Role: "user", Content: "\xed\xa0\x80 lone"},
		{Role: "user", Content: "\xff not utf-8"},
		{Role: "user", Content: "small"},
	}}
	for range 2 {
		body, err := marshalSpliced(request, cache, marshalRequestBody)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := marshalRequestBody(request)
		if !bytes.Equal(body.Bytes(), want) {
			t.Fatalf("body differs\n got %.200q\nwant %.200q", body.Bytes(), want)
		}
	}
	if cache.messages.recency.Len() != 1 {
		t.Fatalf("%d messages cached, want only the small one", cache.messages.recency.Len())
	}
}

// The cache holds no more than its limit, forgets the least recently used message first, and lets go of what the last requests did not send.
func TestTheMessageCacheIsBounded(t *testing.T) {
	cache := newEncodedMessageCache(1000)
	for i := range 100 {
		key := fmt.Appendf(nil, "key-%03d", i)
		cache.store(uint64(i), key, bytes.Repeat([]byte{'x'}, 90))
		if cache.held > cache.limit {
			t.Fatalf("held %d bytes past the limit %d", cache.held, cache.limit)
		}
	}
	if cache.lookup(99, []byte("key-099")) == nil || cache.lookup(0, []byte("key-000")) != nil {
		t.Fatal("the least recently used message was kept over the most recent")
	}
	if cache.lookup(99, []byte("key-000")) != nil {
		t.Fatal("an entry was served for a key that is not its own")
	}
	for range requestMessageKeepRequests + 1 {
		cache.beginRequest()
	}
	if cache.held != 0 || cache.recency.Len() != 0 {
		t.Fatalf("%d bytes kept after %d builds that sent nothing", cache.held, requestMessageKeepRequests+1)
	}
}

// release forgets every large string and every message the caches hold; the next request's body is the same, and the caches fill again from it.
func TestReleasedRequestCachesHoldNothingAndBuildTheSameBody(t *testing.T) {
	cache := newQuotedStringCache(1 << 30)
	large := strings.Repeat("big <&>\n", requestSpliceMinBytes/4)
	request := anthRequest{Model: "m", Messages: []anthMessage{{Role: "user", Content: large}, {Role: "user", Content: "small"}}}
	want, err := marshalRequestBody(request)
	if err != nil {
		t.Fatal(err)
	}
	build := func() {
		t.Helper()
		body, err := marshalSpliced(request, cache, marshalRequestBody)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(body.Bytes(), want) {
			t.Fatalf("body differs\n got %.200q\nwant %.200q", body.Bytes(), want)
		}
	}
	build()
	if cache.recency.Len() == 0 || cache.messages.recency.Len() == 0 {
		t.Fatalf("before release: %d strings, %d messages cached; want both", cache.recency.Len(), cache.messages.recency.Len())
	}
	cache.release()
	if cache.held != 0 || cache.recency.Len() != 0 || len(cache.entries) != 0 || len(cache.bySource) != 0 || len(cache.byData) != 0 {
		t.Fatalf("after release the string cache holds %d bytes in %d entries", cache.held, cache.recency.Len())
	}
	if cache.messages.held != 0 || cache.messages.recency.Len() != 0 || len(cache.messages.entries) != 0 {
		t.Fatalf("after release the message cache holds %d bytes in %d entries", cache.messages.held, cache.messages.recency.Len())
	}
	build()
	if cache.recency.Len() == 0 || cache.messages.recency.Len() == 0 {
		t.Fatalf("after a build the caches hold %d strings and %d messages; want both again", cache.recency.Len(), cache.messages.recency.Len())
	}
}

// A message that the cache serves costs the build no allocation of its own: a resent history of 200 turns is built with a few dozen allocations, not one or more for each of its 600 messages. Without the message cache the body is the same.
func TestTheMessageCacheServesAMessageWithoutAllocating(t *testing.T) {
	for name, request := range map[string]any{"anthropic": anthHistory(200), "completions": oaiHistory(200)} {
		t.Run(name, func(t *testing.T) {
			cache := newQuotedStringCache(1 << 30)
			build := func() *requestBody {
				body, err := marshalSpliced(request, cache, marshalRequestBody)
				if err != nil {
					t.Fatal(err)
				}
				return body
			}
			cached := bytes.Clone(build().Bytes())
			if allocs := testing.AllocsPerRun(5, func() { build() }); allocs > 200 {
				t.Errorf("a build of a cached history made %.0f allocations, want at most 200 for 600 messages", allocs)
			}
			cache.messages = nil
			if plain := build().Bytes(); !bytes.Equal(plain, cached) {
				t.Errorf("the body without the message cache differs from the one with it")
			}
		})
	}
}

// A short request is one segment, so net/http sends it in the write of its headers (requestBody.reader). Messages served from the cache once left it in several, which a server that answers without reading the body saw as unread bytes and closed with a reset.
func TestShortCachedRequestBodyIsOneSegment(t *testing.T) {
	cache := newQuotedStringCache(1 << 30)
	request := oaiHistory(3)
	for range 2 {
		body, err := marshalSpliced(request, cache, marshalRequestBody)
		if err != nil {
			t.Fatal(err)
		}
		if body.size > requestSpliceMinBytes {
			t.Fatalf("fixture body is %d bytes, want a short one", body.size)
		}
		if len(body.segments) != 1 {
			t.Fatalf("a %d-byte body has %d segments, want 1", body.size, len(body.segments))
		}
	}
}
