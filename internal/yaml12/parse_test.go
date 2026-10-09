package yaml12

import (
	"encoding/hex"
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"sort"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// canonValue renders a parsed value the way the JavaScript side of the oracle does, so -0, NaN and Infinity survive JSON. A collection that contains itself renders as ["cycle", depth of the enclosing occurrence].
func canonValue(v any) any { return canonValueIn(v, nil) }

// identity is the address of a map, or of a non-empty slice's backing array, which a cyclic value shares with itself.
func identity(v any) uintptr {
	switch t := v.(type) {
	case map[string]any:
		return reflect.ValueOf(t).Pointer()
	case []any:
		if len(t) > 0 {
			return reflect.ValueOf(t).Pointer()
		}
	}
	return 0
}

func canonValueIn(v any, stack []uintptr) any {
	if id := identity(v); id != 0 {
		if depth := slices.Index(stack, id); depth >= 0 {
			return []any{"cycle", float64(depth)}
		}
		stack = append(stack, id)
	}
	switch t := v.(type) {
	case nil, bool, string:
		return t
	case float64:
		if t == 0 && math.Signbit(t) {
			return []any{"n", "-0"}
		}
		return []any{"n", jsnumber.String(t)}
	case []byte:
		return []any{"bin", hex.EncodeToString(t)}
	case Date:
		if math.IsNaN(t.MS) {
			return []any{"date", "NaN"}
		}
		return []any{"date", jsnumber.String(t.MS)}
	case Symbol:
		return []any{"sym", string(t)}
	case Set:
		out := make([]any, len(t.Values))
		for i, x := range t.Values {
			out[i] = canonValue(x)
		}
		return []any{"set", out}
	case Map:
		out := make([]any, len(t.Keys))
		for i := range t.Keys {
			out[i] = []any{canonValue(t.Keys[i]), canonValue(t.Values[i])}
		}
		return []any{"map", out}
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = canonValueIn(x, stack)
		}
		return out
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := map[string]any{}
		for _, k := range keys {
			out[k] = canonValueIn(t[k], stack)
		}
		return map[string]any{"o": out}
	}
	return []any{"x", "unexpected"}
}

type parseOutcome struct {
	Ok    bool   `json:"ok"`
	Value any    `json:"value"`
	Err   string `json:"err"`
}

// TestParseMatchesTheLibrary compares parse() of eemeli/yaml 2.9.0 with Parse for every corpus text: the value, or the exact error message.
func TestParseMatchesTheLibrary(t *testing.T) {
	corpus := yamlCorpus()
	var want []parseOutcome
	pioracle.Run(t, `
const { parse } = await import(new URL("file://" + root + "/../yaml/index.js").href);
// The vendored library is the browser build, whose binary tag decodes with atob into a Uint8Array. Pi runs on Node, where the library's binary tag returns Buffer.from(src, "base64") (yaml 2.9.0 dist/schema/yaml-1.1/binary.js): a Buffer, whose String() is its UTF-8 text rather than the joined byte values. The oracle installs that resolver so it reads binary scalars as Pi does.
const { binary } = await import(new URL("file://" + root + "/../yaml/dist/schema/yaml-1.1/binary.js").href);
binary.resolve = (src) => Buffer.from(src, "base64");
const canon = (v, stack = []) => {
  if (v === null || typeof v === "boolean" || typeof v === "string") return v;
  if (typeof v === "number") return ["n", Object.is(v, -0) ? "-0" : String(v)];
  if (typeof v === "object" && (!Array.isArray(v) || v.length > 0) && stack.includes(v)) return ["cycle", stack.indexOf(v)];
  const inner = typeof v === "object" && (!Array.isArray(v) || v.length > 0) ? [...stack, v] : stack;
  if (typeof v === "symbol") return ["sym", v.description];
  if (v instanceof Date) return ["date", Number.isNaN(v.getTime()) ? "NaN" : String(v.getTime())];
  if (v instanceof Uint8Array) return ["bin", Buffer.from(v).toString("hex")];
  if (v instanceof Set) return ["set", [...v].map((x) => canon(x, inner))];
  if (v instanceof Map) return ["map", [...v].map(([k, x]) => [canon(k, inner), canon(x, inner)])];
  if (Array.isArray(v)) return v.map((x) => canon(x, inner));
  if (Object.getPrototypeOf(v) === Object.prototype) {
    const o = {};
    for (const k of Object.keys(v).sort()) o[k] = canon(v[k], inner);
    return { o };
  }
  return ["x", "unexpected"];
};
emit(input.map((src) => {
  try { return { ok: true, value: canon(parse(src) ?? null) }; } catch (e) { return { ok: false, err: String(e.message) }; }
}));`, corpus, &want)
	for i, src := range corpus {
		v, err := Parse(src)
		got := parseOutcome{Ok: err == nil}
		if err != nil {
			got.Err = err.Error()
		} else {
			got.Value = canonValue(v)
		}
		gj, _ := json.Marshal(got)
		var gotAny parseOutcome
		_ = json.Unmarshal(gj, &gotAny)
		if !reflect.DeepEqual(gotAny, want[i]) {
			wj, _ := json.Marshal(want[i])
			t.Errorf("%q:\n got %s\nwant %s", src, trunc(gj), trunc(wj))
		}
	}
}
