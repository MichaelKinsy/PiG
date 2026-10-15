package json

import (
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

type trustedRecord struct {
	Type    string            `json:"type"`
	ID      string            `json:"id"`
	Count   int               `json:"count"`
	Ratio   float64           `json:"ratio"`
	Tags    []string          `json:"tags"`
	Extra   map[string]any    `json:"extra"`
	Nested  *trustedRecord    `json:"nested,omitempty"`
	Raw     RawMessage        `json:"raw"`
	Fields  map[string]string `json:"fields"`
	Missing any               `json:"missing"`
}

func randomTrustedRecord(rng *rand.Rand, depth int) *trustedRecord {
	text := func() string {
		var b strings.Builder
		for range rng.IntN(12) {
			b.WriteString(swarPieces[rng.IntN(len(swarPieces))])
		}
		return b.String()
	}
	r := &trustedRecord{Type: text(), ID: text(), Count: rng.IntN(1000) - 500, Ratio: rng.Float64() * 1e6, Raw: RawMessage(`{"a":[1,"x",null]}`)}
	for range rng.IntN(3) {
		r.Tags = append(r.Tags, text())
	}
	if rng.IntN(2) == 0 {
		r.Extra = map[string]any{text(): text(), "n": float64(rng.IntN(9)), "b": true, "l": []any{text(), 1.5, nil}}
		r.Fields = map[string]string{text(): text()}
	}
	if depth < 2 && rng.IntN(3) == 0 {
		r.Nested = randomTrustedRecord(rng, depth+1)
	}
	return r
}

// UnmarshalTrusted decodes what this package encodes exactly as Unmarshal does, into structs, maps and interfaces, over strings with every
// escape, surrogate and ill-formed byte the encoder writes.
func TestUnmarshalTrustedMatchesUnmarshal(t *testing.T) {
	rng := rand.New(rand.NewPCG(31, 32))
	for i := range 5000 {
		value := randomTrustedRecord(rng, 0)
		var data []byte
		var err error
		switch i % 3 {
		case 0:
			data, err = Marshal(value)
		case 1:
			data, err = MarshalJS(value)
		default:
			data, err = MarshalStringify(value)
		}
		if err != nil {
			t.Fatal(err)
		}
		var want, got trustedRecord
		wantErr := Unmarshal(data, &want)
		gotErr := UnmarshalTrusted(data, &got)
		if (wantErr == nil) != (gotErr == nil) || !reflect.DeepEqual(got, want) {
			t.Fatalf("case %d %s:\n got %#v (%v)\nwant %#v (%v)", i, data, got, gotErr, want, wantErr)
		}
		var wantAny, gotAny any
		_ = Unmarshal(data, &wantAny)
		_ = UnmarshalTrusted(data, &gotAny)
		if !reflect.DeepEqual(gotAny, wantAny) {
			t.Fatalf("case %d any: %#v, want %#v", i, gotAny, wantAny)
		}
	}
}

// The package's tests run in the verify mode, where UnmarshalTrusted of invalid JSON panics instead of decoding it.
func TestUnmarshalTrustedVerifiesInTests(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("UnmarshalTrusted of invalid JSON did not panic in the verify mode")
		}
	}()
	var v any
	_ = UnmarshalTrusted([]byte(`{"a":1,}`), &v)
}

func BenchmarkUnmarshalTrusted(b *testing.B) {
	content := strings.Repeat("export const f = (a: number[]) => a.reduce((x, y) => x + y, 0);\n", 3200)
	data, err := MarshalStringify(map[string]any{"type": "message", "content": content, "n": 1})
	if err != nil {
		b.Fatal(err)
	}
	saved := verifyTrusted
	verifyTrusted = false
	defer func() { verifyTrusted = saved }()
	b.Run("Unmarshal", func(b *testing.B) {
		b.SetBytes(int64(len(data)))
		for b.Loop() {
			var v struct{ Content string }
			_ = Unmarshal(data, &v)
		}
	})
	b.Run("UnmarshalTrusted", func(b *testing.B) {
		b.SetBytes(int64(len(data)))
		for b.Loop() {
			var v struct{ Content string }
			_ = UnmarshalTrusted(data, &v)
		}
	})
}
