package protocol

// pi: packages/protocol/src/cbor/encoder.ts
// pi: packages/protocol/src/cbor/decoder.ts
// pi: packages/protocol/src/cbor/options.ts

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// cborSpec is a JavaScript value the Node side builds and the Go side maps to its own representation.
type cborSpec struct {
	K     string          `json:"k"`
	V     bool            `json:"v"`
	Bits  string          `json:"bits"`
	Hex   string          `json:"hex"`
	Bad   bool            `json:"bad"`
	Items []cborSpec      `json:"items"`
	Props [][]interface{} `json:"props"`
}

type cborProbe struct {
	Op   string       `json:"op"`
	Hex  string       `json:"hex"`
	Spec *cborSpec    `json:"spec,omitempty"`
	Opts *cborOptions `json:"opts"`
}

type cborOptions struct {
	MaxByteLength      *float64 `json:"maxByteLength"`
	MaxContainerLength *float64 `json:"maxContainerLength"`
	MaxDepth           *float64 `json:"maxDepth"`
}

type cborOutcome struct {
	Ok  *string `json:"ok,omitempty"`
	Err string  `json:"err,omitempty"`
}

func (o *cborOptions) toOptions() CborOptions {
	if o == nil {
		return CborOptions{}
	}
	return CborOptions{MaxByteLength: o.MaxByteLength, MaxContainerLength: o.MaxContainerLength, MaxDepth: o.MaxDepth}
}

func cborBitsHex(v float64) string { return fmt.Sprintf("%016x", math.Float64bits(v)) }

// cborRender is the rendering the Node side prints: numbers as IEEE bits, strings and keys as UTF-8 hex, object properties in order.
func cborRender(value any) (string, error) {
	switch v := value.(type) {
	case nil:
		return "null", nil
	case bool:
		return strconv.FormatBool(v), nil
	case float64:
		return "n" + cborBitsHex(v), nil
	case string:
		return "s" + hex.EncodeToString([]byte(v)), nil
	case []byte:
		return "b" + hex.EncodeToString(v), nil
	case []any:
		parts := make([]string, len(v))
		for i, item := range v {
			part, err := cborRender(item)
			if err != nil {
				return "", err
			}
			parts[i] = part
		}
		return "[" + strings.Join(parts, ",") + "]", nil
	case Object:
		parts := make([]string, len(v))
		for i, property := range v {
			part, err := cborRender(property.Value)
			if err != nil {
				return "", err
			}
			parts[i] = "s" + hex.EncodeToString([]byte(property.Key.(string))) + ":" + part
		}
		return "{" + strings.Join(parts, ",") + "}", nil
	}
	return "", fmt.Errorf("unexpected decoded type %T", value)
}

func cborBuild(spec cborSpec) any {
	switch spec.K {
	case "null":
		return nil
	case "undef":
		return Undefined{}
	case "bool":
		return spec.V
	case "num":
		bits, _ := strconv.ParseUint(spec.Bits, 16, 64)
		return math.Float64frombits(bits)
	case "str":
		if spec.Bad {
			return "\xed\xa0\x80"
		}
		text, _ := hex.DecodeString(spec.Hex)
		return string(text)
	case "bytes":
		data, _ := hex.DecodeString(spec.Hex)
		return append([]byte{}, data...)
	case "arr":
		items := make([]any, len(spec.Items))
		for i, item := range spec.Items {
			items[i] = cborBuild(item)
		}
		return items
	}
	object := Object{}
	for _, prop := range spec.Props {
		key, _ := hex.DecodeString(prop[0].(string))
		raw, _ := json.Marshal(prop[1])
		var value cborSpec
		_ = json.Unmarshal(raw, &value)
		object = append(object, Property{Key: string(key), Value: cborBuild(value)})
	}
	return object
}

func cborErrorText(err error) string {
	var cborError *CborError
	var rangeError *RangeError
	switch {
	case errors.As(err, &cborError):
		return "CborError: " + err.Error()
	case errors.As(err, &rangeError):
		return "RangeError: " + err.Error()
	}
	return "Error: " + err.Error()
}

func runCborProbe(t *testing.T, probe cborProbe) cborOutcome {
	t.Helper()
	options := probe.Opts.toOptions()
	var out string
	if probe.Op == "decode" {
		data, _ := hex.DecodeString(probe.Hex)
		value, err := DecodeCbor(data, options)
		if err != nil {
			return cborOutcome{Err: cborErrorText(err)}
		}
		if out, err = cborRender(value); err != nil {
			t.Fatal(err)
		}
		return cborOutcome{Ok: &out}
	}
	data, err := EncodeCbor(cborBuild(*probe.Spec), options)
	if err != nil {
		return cborOutcome{Err: cborErrorText(err)}
	}
	out = hex.EncodeToString(data)
	return cborOutcome{Ok: &out}
}

type cborGenerator struct{ random *rand.Rand }

func (g cborGenerator) head(major byte, value uint64) []byte {
	prefix := major << 5
	switch g.random.Intn(8) {
	case 0: // a longer argument than the value needs
		if value <= 0xff {
			return []byte{prefix | 24, byte(value)}
		}
	case 1:
		if value <= 0xffff {
			return []byte{prefix | 25, byte(value >> 8), byte(value)}
		}
	}
	switch {
	case value < 24:
		return []byte{prefix | byte(value)}
	case value <= 0xff:
		return []byte{prefix | 24, byte(value)}
	case value <= 0xffff:
		return []byte{prefix | 25, byte(value >> 8), byte(value)}
	case value <= 0xffffffff:
		return []byte{prefix | 26, byte(value >> 24), byte(value >> 16), byte(value >> 8), byte(value)}
	}
	out := []byte{prefix | 27}
	for shift := 56; shift >= 0; shift -= 8 {
		out = append(out, byte(value>>shift))
	}
	return out
}

func (g cborGenerator) text(n int) []byte {
	words := []string{"a", "é", "日本", "😀", "", "__proto__", "0", "10", "2", "constructor"}
	var out []byte
	for len(out) < n {
		out = append(out, words[g.random.Intn(len(words))]...)
	}
	if g.random.Intn(12) == 0 {
		out = append(out, 0xff)
	}
	return out
}

func (g cborGenerator) item(depth int) []byte {
	r := g.random
	switch r.Intn(14) {
	case 0, 1:
		return g.head(0, []uint64{0, 1, 23, 24, 255, 256, 65535, 65536, 4294967295, 4294967296, 1<<53 - 2, 1<<53 - 1, 1 << 53, 1<<64 - 1, r.Uint64() >> uint(r.Intn(64))}[r.Intn(15)])
	case 2:
		return g.head(1, []uint64{0, 1, 23, 24, 1<<53 - 2, 1<<53 - 1, 1 << 53, 1<<64 - 1, r.Uint64() >> uint(r.Intn(64))}[r.Intn(9)])
	case 3:
		n := r.Intn(30)
		data := make([]byte, n)
		r.Read(data)
		return append(g.head(2, uint64(n)), data...)
	case 4:
		text := g.text(r.Intn(20))
		return append(g.head(3, uint64(len(text))), text...)
	case 5, 6:
		if depth > 0 {
			n := r.Intn(4)
			out := g.head(4, uint64(n))
			for range n {
				out = append(out, g.item(depth-1)...)
			}
			return out
		}
	case 7, 8:
		if depth > 0 {
			n := r.Intn(4)
			out := g.head(5, uint64(n))
			for range n {
				if r.Intn(10) == 0 {
					out = append(out, g.item(depth-1)...) // any key type
				} else {
					text := g.text(r.Intn(3))
					out = append(out, g.head(3, uint64(len(text)))...)
					out = append(out, text...)
				}
				out = append(out, g.item(depth-1)...)
			}
			return out
		}
	case 9:
		return append(g.head(6, uint64(r.Intn(40))), g.item(0)...)
	case 10:
		return []byte{[]byte{0xf4, 0xf5, 0xf6, 0xf7, 0xe0, 0xf8, 0xff, 0xf9, 0xfa, 0xfc}[r.Intn(10)]}
	case 11:
		floats := []float64{0, math.Copysign(0, -1), 1.5, -2.5, 1 << 53, -(1 << 53), 9007199254740991, 1e300, math.Inf(1), math.NaN(), 5e-324, 3}
		out := []byte{0xfb}
		bits := math.Float64bits(floats[r.Intn(len(floats))])
		for shift := 56; shift >= 0; shift -= 8 {
			out = append(out, byte(bits>>shift))
		}
		return out
	case 12:
		return []byte{[]byte{0x9f, 0xbf, 0x5f, 0x7f, 0x1f}[r.Intn(5)]}
	}
	return []byte{0xf6}
}

func cborDecodeProbes() []cborProbe {
	random := rand.New(rand.NewSource(20260927))
	generator := cborGenerator{random}
	limit := func(values ...float64) *float64 {
		if len(values) == 0 || random.Intn(3) != 0 {
			return nil
		}
		v := values[random.Intn(len(values))]
		return &v
	}
	var probes []cborProbe
	for range 1500 {
		data := generator.item(1 + random.Intn(5))
		switch random.Intn(6) {
		case 0:
			data = data[:random.Intn(len(data)+1)]
		case 1:
			data = append(data, byte(random.Intn(256)))
		case 2:
			if len(data) > 0 {
				data[random.Intn(len(data))] ^= byte(1 << random.Intn(8))
			}
		}
		opts := &cborOptions{
			MaxByteLength:      limit(0, 1, 5, 20, 64, -1, 1.5, 4294967295, 4294967296),
			MaxContainerLength: limit(0, 1, 2, 3, -1, 2.5, 4294967295, 4294967296),
			MaxDepth:           limit(0, 1, 2, 3, 512, 513, -1, 0.5),
		}
		probes = append(probes, cborProbe{Op: "decode", Hex: hex.EncodeToString(data), Opts: opts})
	}
	deep := bytes.Repeat([]byte{0x81}, 70)
	probes = append(probes, cborProbe{Op: "decode", Hex: hex.EncodeToString(append(deep, 0xf6))}, cborProbe{Op: "decode", Hex: ""})
	return probes
}

func cborEncodeProbes() []cborProbe {
	random := rand.New(rand.NewSource(20260928))
	numbers := []float64{0, math.Copysign(0, -1), 1, -1, 23, 24, 255, 256, 65535, 65536, 4294967295, 4294967296, 1.5, -2.25, 1<<53 - 1, -(1<<53 - 1), 1 << 53, -(1 << 53), 1e300, math.Inf(1), math.Inf(-1), math.NaN(), 5e-324}
	keys := []string{"a", "b", "10", "2", "1", "__proto__", "constructor", "é", "", "日本", "4294967295", "4294967294", "-1", "01"}
	var gen func(depth int) cborSpec
	gen = func(depth int) cborSpec {
		switch random.Intn(10) {
		case 0:
			return cborSpec{K: "null"}
		case 1:
			return cborSpec{K: "undef"}
		case 2:
			return cborSpec{K: "bool", V: random.Intn(2) == 0}
		case 3, 4:
			return cborSpec{K: "num", Bits: cborBitsHex(numbers[random.Intn(len(numbers))])}
		case 5:
			if random.Intn(8) == 0 {
				return cborSpec{K: "str", Bad: true}
			}
			return cborSpec{K: "str", Hex: hex.EncodeToString([]byte(keys[random.Intn(len(keys))] + strings.Repeat("x", random.Intn(30))))}
		case 6:
			data := make([]byte, random.Intn(30))
			random.Read(data)
			return cborSpec{K: "bytes", Hex: hex.EncodeToString(data)}
		case 7:
			if depth > 0 {
				items := make([]cborSpec, random.Intn(4))
				for i := range items {
					items[i] = gen(depth - 1)
				}
				return cborSpec{K: "arr", Items: items}
			}
		case 8, 9:
			if depth > 0 {
				props := make([][]interface{}, random.Intn(5))
				for i := range props {
					props[i] = []interface{}{hex.EncodeToString([]byte(keys[random.Intn(len(keys))])), gen(depth - 1)}
				}
				return cborSpec{K: "obj", Props: props}
			}
		}
		return cborSpec{K: "num", Bits: cborBitsHex(numbers[random.Intn(len(numbers))])}
	}
	limit := func(values ...float64) *float64 {
		if random.Intn(3) != 0 {
			return nil
		}
		v := values[random.Intn(len(values))]
		return &v
	}
	var probes []cborProbe
	for range 1500 {
		spec := gen(1 + random.Intn(5))
		opts := &cborOptions{
			MaxByteLength:      limit(0, 1, 5, 20, 64, 4294967296),
			MaxContainerLength: limit(0, 1, 2, 3, -1),
			MaxDepth:           limit(0, 1, 2, 3, 513),
		}
		probes = append(probes, cborProbe{Op: "encode", Spec: &spec, Opts: opts})
	}
	return probes
}

// cbor/encoder.ts, decoder.ts and options.ts run in Node from the pinned source against the same probes: every decoded value (numbers by IEEE bits,
// property order), every encoded byte, every error name and message, and every limit check must match Pi's.
func TestCborMatchesPiOnSeededProbes(t *testing.T) {
	probes := append(cborDecodeProbes(), cborEncodeProbes()...)
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages", "protocol", "src", "cbor", "index.ts")
	cmd := exec.CommandContext(t.Context(), "node", "testdata/cbor.mjs", source)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []cborOutcome
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(probes) {
		t.Fatalf("Pi answered %d of %d probes", len(expected), len(probes))
	}
	okCount, differing := 0, 0
	for i, probe := range probes {
		got := runCborProbe(t, probe)
		if expected[i].Ok != nil {
			okCount++
		}
		if !reflect.DeepEqual(got, expected[i]) {
			if differing++; differing <= 5 {
				t.Errorf("probe %d (%s %s %+v) differs from Pi:\n  Pig %s\n  Pi  %s", i, probe.Op, cborProbeText(probe), probe.Opts, cborOutcomeText(got), cborOutcomeText(expected[i]))
			}
		}
	}
	if differing > 5 {
		t.Errorf("%d of %d probes differ from Pi", differing, len(probes))
	}
	if okCount < len(probes)/4 || okCount > len(probes)*9/10 {
		t.Errorf("probes are one-sided: Pi accepted %d of %d", okCount, len(probes))
	}
}

func cborProbeText(p cborProbe) string {
	if p.Op == "decode" {
		return p.Hex
	}
	raw, _ := json.Marshal(p.Spec)
	return string(raw)
}

func cborOutcomeText(o cborOutcome) string {
	if o.Ok != nil {
		return "ok " + *o.Ok
	}
	return "err " + o.Err
}
