package json

import (
	"reflect"
	"strings"
	"sync"
	"testing"
)

type poolProbe struct {
	Name   string         `json:"name"`
	Count  int            `json:"count"`
	Nested *poolProbe     `json:"nested"`
	Raw    RawMessage     `json:"raw"`
	Extra  map[string]any `json:"extra"`
}

// The decode state is pooled: a decode must not leak an earlier decode's error,
// input or parse state into the next one, whatever order they run in.
func TestUnmarshalPoolDoesNotLeakBetweenDecodes(t *testing.T) {
	var broken poolProbe
	if err := Unmarshal([]byte(`{"name":"x","count":"not a number","nested":{"count":1}}`), &broken); err == nil {
		t.Fatal("a string for an int must fail")
	}
	if err := Unmarshal([]byte(`{"name":`), &broken); err == nil {
		t.Fatal("truncated input must fail")
	}
	var clean poolProbe
	if err := Unmarshal([]byte(`{"name":"ok","count":2}`), &clean); err != nil {
		t.Fatalf("a valid decode after failed ones: %v", err)
	}
	if clean.Name != "ok" || clean.Count != 2 || clean.Nested != nil {
		t.Fatalf("decoded %+v", clean)
	}
}

// A value a decode returned must not change when a later decode reuses the state.
func TestUnmarshalPoolResultsDoNotAliasTheInput(t *testing.T) {
	input := []byte(`{"name":"first","raw":{"k":[1,2,3]},"extra":{"a":"b"}}`)
	var first poolProbe
	if err := Unmarshal(input, &first); err != nil {
		t.Fatal(err)
	}
	for i := range input {
		input[i] = 'x'
	}
	for range 50 {
		var other poolProbe
		if err := Unmarshal([]byte(`{"name":"other","raw":[9,9,9],"extra":{"z":1}}`), &other); err != nil {
			t.Fatal(err)
		}
	}
	if first.Name != "first" || string(first.Raw) != `{"k":[1,2,3]}` || first.Extra["a"] != "b" {
		t.Fatalf("first decode changed: %+v raw=%s", first, first.Raw)
	}
}

// Deep documents grow the parse-state stack; the pool drops an oversized one
// and later decodes still work.
func TestUnmarshalPoolSurvivesDeepDocuments(t *testing.T) {
	deep := strings.Repeat("[", 5000) + strings.Repeat("]", 5000)
	var v any
	_ = Unmarshal([]byte(deep), &v) // exceeds the nesting limit: the outcome is not the contract
	var clean poolProbe
	if err := Unmarshal([]byte(`{"count":7}`), &clean); err != nil || clean.Count != 7 {
		t.Fatalf("decode after a deep document: %+v, %v", clean, err)
	}
}

func TestUnmarshalPoolIsSafeUnderConcurrency(t *testing.T) {
	var wg sync.WaitGroup
	for worker := range 16 {
		wg.Go(func() {
			for i := range 500 {
				var v poolProbe
				doc := `{"name":"w","count":` + strings.Repeat("1", worker%3+1) + `,"nested":{"count":` + strings.Repeat("2", i%4+1) + `}}`
				if i%7 == 0 {
					doc = `{"count":"bad"}`
					if Unmarshal([]byte(doc), &v) == nil {
						t.Error("a bad doc decoded")
						return
					}
					continue
				}
				if err := Unmarshal([]byte(doc), &v); err != nil || v.Nested == nil || v.Name != "w" {
					t.Errorf("worker %d decode %d: %+v, %v", worker, i, v, err)
					return
				}
			}
		})
	}
	wg.Wait()
}

// Decoding a small message allocates only what the result needs: the decode
// state and its parse-state stack come from the pool.
func TestUnmarshalAllocatesOnlyTheResult(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector makes sync.Pool drop items at random")
	}
	doc := []byte(`{"count":1,"name":"abc","nested":{"count":2}}`)
	var v poolProbe
	if got := testing.AllocsPerRun(1000, func() { v = poolProbe{}; _ = Unmarshal(doc, &v) }); got > 2 {
		t.Fatalf("Unmarshal allocates %.1f objects for a small message, want at most 2 (the name string and the nested struct)", got)
	}
}

// A field-error message names the path of the field that failed even though the
// error context is pooled: a failed decode of one document must not show up in
// the message of another.
func TestUnmarshalPoolKeepsErrorContextPerDecode(t *testing.T) {
	var v poolProbe
	err := Unmarshal([]byte(`{"nested":{"count":"x"}}`), &v)
	if err == nil || !strings.Contains(err.Error(), "poolProbe.nested.count") {
		t.Fatalf("error %v does not name the field path", err)
	}
	err = Unmarshal([]byte(`{"count":"y"}`), &v)
	if err == nil || !strings.Contains(err.Error(), "poolProbe.count") || strings.Contains(err.Error(), "nested") {
		t.Fatalf("second error %v carries the first decode's field path", err)
	}
}

// A released decode state holds neither the input (a frame buffer the caller is
// about to reuse), nor a field path, and an oversized parse-state stack is dropped.
func TestReleaseDecodeStateClearsWhatItHeld(t *testing.T) {
	d := new(decodeState)
	d.init([]byte(`{"a":1}`))
	d.savedError = &SyntaxError{msg: "kept"}
	d.useNumber = true
	d.scan.parseState = make([]int, 3, 2000)
	d.errorContext = &errorContext{Struct: reflect.TypeFor[poolProbe](), FieldStack: []string{"name", "count"}}
	context := d.errorContext
	releaseDecodeState(d)
	if d.data != nil || d.savedError != nil || d.useNumber || d.off != 0 {
		t.Fatalf("released state still holds decode data: %+v", *d)
	}
	if d.scan.parseState != nil || cap(d.scan.parseState) != 0 {
		t.Fatalf("an oversized parse-state stack stayed in the pool: cap %d", cap(d.scan.parseState))
	}
	if d.errorContext != context || context.Struct != nil || len(context.FieldStack) != 0 {
		t.Fatalf("error context not reset: %+v", *context)
	}
	for _, name := range context.FieldStack[:cap(context.FieldStack)] {
		if name != "" {
			t.Fatalf("field path %q kept alive in the pooled error context", name)
		}
	}
}
