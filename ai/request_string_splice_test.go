package ai

import (
	"bytes"
	"encoding/json"
	"math/rand/v2"
	"strings"
	"testing"
)

// shout is a marshaler that reads the string it carries, as no payload type of a provider does; it does not carry its strings through, so its text is left to it.
type shout struct{ Text string }

func (s shout) MarshalJSON() ([]byte, error) { return json.Marshal(strings.ToUpper(s.Text)) }

// carried writes its string unread.
type carried struct{ Text string }

func (c carried) carriesRequestStrings() {}
func (c carried) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Kind string `json:"kind"`
		Text string `json:"text"`
	}{"carried", c.Text})
}

// halve is a carrier that breaks its contract: its token is not a whole JSON string.
type halve struct{ Text string }

func (h halve) carriesRequestStrings()       {}
func (h halve) MarshalJSON() ([]byte, error) { return json.Marshal("prefix:" + h.Text) }

type spliceLeaf struct {
	Text   string `json:"text"`
	Empty  string `json:"empty,omitempty"`
	hidden string
}

type spliceNode struct {
	Name     string            `json:"name"`
	Any      any               `json:"any,omitempty"`
	Pointer  *spliceLeaf       `json:"pointer,omitempty"`
	List     []spliceLeaf      `json:"list,omitempty"`
	Array    [2]string         `json:"array"`
	Map      map[string]any    `json:"map,omitempty"`
	Strings  map[string]string `json:"strings,omitempty"`
	Object   JsonObject        `json:"object,omitempty"`
	Raw      json.RawMessage   `json:"raw,omitempty"`
	Bytes    []byte            `json:"bytes,omitempty"`
	Kind     StopReason        `json:"kind,omitempty"`
	Shouted  *shout            `json:"shouted,omitempty"`
	Carried  []carried         `json:"carried,omitempty"`
	Children []*spliceNode     `json:"children,omitempty"`
}

// A payload through marshalSpliced is the bytes marshalRequestBody gives it, for every shape a provider's payload takes, and the payload is not written to.
func TestMarshalSplicedIsByteIdenticalToMarshalRequestBody(t *testing.T) {
	random := rand.New(rand.NewPCG(5, 6))
	atoms := []string{"func main() {\n\tfmt.Println(\"<hi> & bye\")\n}\n", "\u2028\u2029", "\x00\x01\x1f", "😀 café 漢字", "\\\"", "pig-splice-0-", `"pig-splice-deadbeef-0"`}
	var pool []string
	for _, size := range []int{0, 1, requestSpliceMinBytes - 1, requestSpliceMinBytes, 3 * requestSpliceMinBytes} {
		var builder strings.Builder
		for builder.Len() < size {
			builder.WriteString(atoms[random.IntN(len(atoms))])
		}
		pool = append(pool, builder.String(), builder.String()+"\xff")
	}
	pick := func() string { return pool[random.IntN(len(pool))] }
	for trial := range 300 {
		node := &spliceNode{Name: "n", Array: [2]string{pick(), "x"}, Kind: StopReason("stop")}
		node.Any = pool[8]
		node.Pointer = &spliceLeaf{Text: pick(), hidden: pick()}
		node.List = []spliceLeaf{{Text: pick()}, {Text: "short", Empty: pick()}}
		node.Map = map[string]any{"a": pick(), "b": []any{pick(), map[string]any{"c": pick()}}, "d": 3}
		node.Strings = map[string]string{"k" + pick(): pick()}
		node.Object = JsonObject{"path": "a", "content": pick()}
		node.Raw = json.RawMessage(`"` + strings.Repeat("r", requestSpliceMinBytes) + `"`)
		node.Bytes = bytes.Repeat([]byte("b"), requestSpliceMinBytes)
		node.Shouted = &shout{Text: pick()}
		node.Carried = []carried{{Text: pick()}, {Text: pick()}}
		node.Children = []*spliceNode{{Name: pick(), Array: [2]string{pick(), pick()}}, nil}
		payload := any(node)
		if random.IntN(2) == 0 {
			payload = *node
		}
		before, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		want, err := marshalRequestBody(payload)
		if err != nil {
			t.Fatal(err)
		}
		cache := newQuotedStringCache(1 << 30)
		for pass := range 2 {
			got, err := marshalSpliced(payload, cache, marshalRequestBody)
			if err != nil {
				t.Fatal(err)
			}
			if joined := got.Bytes(); !bytes.Equal(joined, want) || got.size != len(want) {
				t.Fatalf("trial %d pass %d: spliced body differs from marshalRequestBody\n got %.300q\nwant %.300q", trial, pass, got.Bytes(), want)
			}
		}
		if after, _ := json.Marshal(payload); !bytes.Equal(before, after) {
			t.Fatalf("trial %d: the payload was written to", trial)
		}
		if cache.misses == 0 {
			t.Fatalf("trial %d: no string was spliced", trial)
		}
	}
}

// A carrier that breaks its contract must cost the cache, never the body: its token is not a whole JSON string of the skeleton, so the payload is marshalled as it is. A marshaler that is not a carrier is never given a token.
func TestMarshalSplicedFallsBackWhenACarrierRewritesAString(t *testing.T) {
	large := strings.Repeat("abc <&> \n", requestSpliceMinBytes/4)
	for name, payload := range map[string]any{
		"halved": map[string]any{"a": halve{Text: large}},
		"quoted": struct {
			A string `json:"a,string"`
		}{large},
		"unmarked":      map[string]any{"a": shout{Text: large}},
		"carried inner": map[string]any{"a": carried{Text: large}, "b": halve{Text: large + "x"}},
	} {
		want, err := marshalRequestBody(payload)
		if err != nil {
			t.Fatal(err)
		}
		got, err := marshalSpliced(payload, newQuotedStringCache(1<<30), marshalRequestBody)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got.Bytes(), want) {
			t.Fatalf("%s: body differs\n got %.120q\nwant %.120q", name, got.Bytes(), want)
		}
		if len(got.segments) != 1 {
			t.Fatalf("%s: a body with a rewritten token was spliced (%d segments)", name, len(got.segments))
		}
	}
}

// A string that is not valid UTF-8 is written by the payload's own marshaler, which replaces what the cache would keep.
func TestMarshalSplicedLeavesAStringThatIsNotUTF8ToTheMarshaler(t *testing.T) {
	bad := strings.Repeat("ab\xff", requestSpliceMinBytes)
	cache := newQuotedStringCache(1 << 30)
	got, err := marshalSpliced(map[string]string{"a": bad}, cache, json.Marshal)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(map[string]string{"a": bad})
	if !bytes.Equal(got.Bytes(), want) || cache.misses != 0 {
		t.Fatalf("a string that is not UTF-8 was cached (%d misses)", cache.misses)
	}
}

// Marshalling keeps the encoded strings between requests: the second send finds both in the cache.
func TestMarshalSplicedEncodesAStringOncePerCache(t *testing.T) {
	a, b := strings.Repeat("a<", requestSpliceMinBytes), strings.Repeat("b&", requestSpliceMinBytes)
	cache := newQuotedStringCache(1 << 30)
	payload := map[string]any{"x": []any{a, b}, "y": a}
	for range 3 {
		if _, err := marshalSpliced(payload, cache, marshalRequestBody); err != nil {
			t.Fatal(err)
		}
	}
	if cache.misses != 2 {
		t.Fatalf("two strings were encoded %d times over three requests", cache.misses)
	}
}

type spliceCycle struct {
	Text string
	Next *spliceCycle
}

// A payload that refers to itself fails as marshalRequestBody fails on it, with the marshaler's cycle error, instead of the walk exhausting the stack (JSON.stringify throws on a cycle too).
func TestMarshalSplicedReportsACyclicPayloadAsTheMarshalerDoes(t *testing.T) {
	node := &spliceCycle{Text: strings.Repeat("cycle ", requestSpliceMinBytes)}
	node.Next = node
	_, want := marshalRequestBody(node)
	if want == nil {
		t.Fatal("marshalRequestBody accepted a cyclic payload")
	}
	_, err := marshalSpliced(node, newQuotedStringCache(1<<30), marshalRequestBody)
	if err == nil || err.Error() != want.Error() {
		t.Fatalf("marshalSpliced = %v; want %v", err, want)
	}
}

// The walk finds a cycle by the pointer, map or slice it is inside, not by a depth count: a map or slice that holds itself fails as the marshaler fails, and a chain of 5,000 pointers (deeper than encoding/json's
// own cycle check starts) ends in a string that is still spliced.
func TestMarshalSplicedWalksAnyDepthAndReportsEveryKindOfCycle(t *testing.T) {
	big := strings.Repeat("deep ", requestSpliceMinBytes)
	selfMap := map[string]any{"text": big}
	selfMap["self"] = selfMap
	selfSlice := []any{big, nil}
	selfSlice[1] = selfSlice
	for name, payload := range map[string]any{"map": selfMap, "slice": selfSlice} {
		_, want := marshalRequestBody(payload)
		if want == nil {
			t.Fatalf("%s: marshalRequestBody accepted a cyclic payload", name)
		}
		if _, err := marshalSpliced(payload, newQuotedStringCache(1<<30), marshalRequestBody); err == nil || err.Error() != want.Error() {
			t.Fatalf("%s: marshalSpliced = %v; want %v", name, err, want)
		}
	}

	chain := &spliceCycle{Text: big}
	for range 5000 {
		chain = &spliceCycle{Next: chain}
	}
	cache := newQuotedStringCache(1 << 30)
	got, err := marshalSpliced(chain, cache, marshalRequestBody)
	if err != nil {
		t.Fatal(err)
	}
	want, err := marshalRequestBody(chain)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatal("the spliced body of a deep chain differs from the marshaled body")
	}
	if cache.misses != 1 {
		t.Fatalf("the string at the bottom of the chain was encoded %d times, want 1 (spliced)", cache.misses)
	}
}
