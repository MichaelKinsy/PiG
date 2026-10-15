//go:build !race

package ai

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

type walkMessage struct {
	Role      string         `json:"role"`
	Content   string         `json:"content"`
	Weight    float64        `json:"weight"`
	Index     int            `json:"index"`
	Tools     []walkToolCall `json:"tools,omitempty"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type walkToolCall struct {
	ID    string `json:"id"`
	Count int    `json:"count"`
}

func walkPayload(messages int, large string) []walkMessage {
	payload := make([]walkMessage, messages)
	for i := range payload {
		payload[i] = walkMessage{Role: "user", Content: "short", Tools: []walkToolCall{{ID: "call", Count: i}}}
	}
	if large != "" {
		payload[len(payload)-1].Content = large
	}
	return payload
}

// Pi serializes the whole request on every send, so the walk that finds a request's large strings runs once per message of the conversation. A payload with nothing to splice is walked without an allocation, and a payload with one large string costs what that string costs however many messages surround it.
func TestTheSpliceWalkAllocatesNothingPerMessage(t *testing.T) {
	cache := newQuotedStringCache(1 << 30)
	large := strings.Repeat("walk ", requestSpliceMinBytes)
	measure := func(messages int, text string) float64 {
		payload := walkPayload(messages, text)
		splice := &stringSplice{cache: cache, nonce: "nonce"}
		splice.tokens(payload)
		return testing.AllocsPerRun(20, func() {
			splice := &stringSplice{cache: cache, nonce: "nonce"}
			splice.tokens(payload)
		})
	}
	if few, many := measure(10, ""), measure(400, ""); few != many || many > 2 {
		t.Fatalf("a payload with no large string allocated %v times for 10 messages and %v for 400", few, many)
	}
	if few, many := measure(10, large), measure(400, large); many-few > 1 {
		t.Fatalf("a payload with one large string allocated %v times for 10 messages and %v for 400", few, many)
	}
}

func BenchmarkSpliceWalk(b *testing.B) {
	cache := newQuotedStringCache(1 << 30)
	payload := walkPayload(400, strings.Repeat("walk ", requestSpliceMinBytes))
	b.ReportAllocs()
	for b.Loop() {
		splice := &stringSplice{cache: cache, nonce: "nonce"}
		splice.tokens(payload)
	}
}

type walkCycleA struct {
	Next *walkCycleB
}

type walkCycleB struct {
	Back *walkCycleA
	Text string
}

type walkMarshalerField struct {
	Writer json.Marshaler `json:"writer"`
	Note   string         `json:"note"`
}

type walkReadField struct {
	Beta string `requestsplice:"read"`
	Text string
}

// The walk visits only what can hold a string it replaces: a field of numbers is not read, a type that reaches a string, a raw message or an interface only through a cycle is still visited, and a struct lists its fields once.
func TestTheSpliceWalkVisitsExactlyTheTypesThatCanHoldAString(t *testing.T) {
	skipped := []reflect.Type{
		reflect.TypeFor[int](), reflect.TypeFor[[]float64](), reflect.TypeFor[map[string]int](), reflect.TypeFor[*bool](),
		reflect.TypeFor[struct{ A, B int }](), reflect.TypeFor[[]struct{ A []int }](), reflect.TypeFor[[]byte](), reflect.TypeFor[StopReason](),
		reflect.TypeFor[shout](),
	}
	for _, typ := range skipped {
		if !planOf(typ).skip {
			t.Errorf("%v holds no string the walk replaces and is visited", typ)
		}
	}
	visited := []reflect.Type{
		reflect.TypeFor[string](), reflect.TypeFor[any](), reflect.TypeFor[json.RawMessage](), reflect.TypeFor[[]any](), reflect.TypeFor[map[string]string](),
		reflect.TypeFor[walkToolCall](), reflect.TypeFor[*walkCycleA](), reflect.TypeFor[walkCycleA](), reflect.TypeFor[json.Marshaler](),
		reflect.TypeFor[walkMarshalerField](), reflect.TypeFor[[]carried](), reflect.TypeFor[[2]string](), reflect.TypeFor[[]json.RawMessage](),
	}
	for _, typ := range visited {
		if plan := planOf(typ); plan.skip && typ.Kind() != reflect.Interface {
			t.Errorf("%v can hold a string the walk replaces and is skipped", typ)
		}
	}
	if got := planOf(reflect.TypeFor[walkMessage]()).fields; !slices.Equal(got, []int{0, 1, 4, 5}) {
		t.Errorf("walkMessage fields visited = %v; want role, content, tools, arguments", got)
	}
	if got := planOf(reflect.TypeFor[walkReadField]()).fields; !slices.Equal(got, []int{1}) {
		t.Errorf("a field the provider reads is visited: %v", got)
	}
	if got := planOf(reflect.TypeFor[spliceLeaf]()).fields; !slices.Equal(got, []int{0, 1}) {
		t.Errorf("an unexported field is visited: %v", got)
	}
}

// A string behind an interface-typed field, a type cycle or a skipped sibling is still replaced, and the body is the one marshalRequestBody gives the payload.
func TestTheSpliceWalkStillFindsStringsBehindInterfacesAndCycles(t *testing.T) {
	large := strings.Repeat("deep<", requestSpliceMinBytes)
	cases := map[string]any{
		"carrier behind a marshaler interface": walkMarshalerField{Writer: carried{Text: large}, Note: "n"},
		"string behind a type cycle":           walkCycleA{Next: &walkCycleB{Text: large}},
		"read field next to a string":          walkReadField{Beta: large, Text: large},
		"numbers around a string":              []walkMessage{{Weight: 1, Index: 2}, {Content: large, Weight: 3}},
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			cache := newQuotedStringCache(1 << 30)
			want, err := marshalRequestBody(payload)
			if err != nil {
				t.Fatal(err)
			}
			body, err := marshalSpliced(payload, cache, marshalRequestBody)
			if err != nil {
				t.Fatal(err)
			}
			var got []byte
			for _, segment := range body.segments {
				got = append(got, segment...)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("body differs from marshalRequestBody")
			}
			if cache.misses == 0 {
				t.Fatal("the string behind it was not encoded through the cache")
			}
		})
	}
}

// A raw message the cache wrote is replaced by its token wherever it sits: in a field, behind an interface, or as an element of a slice of raw messages.
func TestTheSpliceWalkReplacesACachedRawMessageInASlice(t *testing.T) {
	cache := newQuotedStringCache(1 << 30)
	raw := json.RawMessage(cache.quote(strings.Repeat("raw<", requestSpliceMinBytes)))
	type rawPayload struct {
		Items []json.RawMessage
		One   json.RawMessage
		Any   []any
	}
	for name, payload := range map[string]rawPayload{"slice element": {Items: []json.RawMessage{raw}}, "field": {One: raw}, "interface element": {Any: []any{raw}}} {
		splice := &stringSplice{cache: cache, nonce: newSpliceNonce()}
		if _, changed := splice.tokens(payload); !changed {
			t.Errorf("%s: the cached raw message was not replaced", name)
		}
		want, err := marshalRequestBody(payload)
		if err != nil {
			t.Fatal(err)
		}
		body, err := marshalSpliced(payload, cache, marshalRequestBody)
		if err != nil {
			t.Fatal(err)
		}
		if got := body.Bytes(); !bytes.Equal(got, want) {
			t.Errorf("%s: body differs from marshalRequestBody", name)
		}
	}
}
