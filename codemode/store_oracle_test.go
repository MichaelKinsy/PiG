package codemode_test

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/codemode"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// host.ts serializeStore sends the script's load() snapshot as `key -> JSON.stringify(value)`, assigning with `serialized[key] = json`, so a `__proto__`
// key never reaches the script. The pinned Pi's CodemodeSandbox and Sandbox.Execute run the same stores; load() of every key must return the same value
// and the same String() of it. Keys include unpaired surrogates, which a Go map key holds in its WTF-8 form.
func TestStoreSnapshotMatchesPi(t *testing.T) {
	keys := [][]uint16{{'a'}, {'b'}, {'0'}, {'1'}, {'1', '0'}, jsstring.ToUTF16("__proto__"), jsstring.ToUTF16("constructor"), {0xd800}, {0xd801}, {0xdc00}, {0xe9}, {0x65, 0x301}, {0}, {'x', ' ', 'y'}, {0xd83d, 0xde00}}
	values := []string{`1`, `1.0`, `1e2`, `1E400`, `-0`, `0.1e1`, `12345678901234567890`, `1.5e-7`, `"x"`, `"\ud800"`, `"\u00e9"`, `"\/"`, `[1,2]`, `{"b":1,"a":2}`, `{"a":1,"a":2}`, `{ "a" : [ 1 , 2 ] }`, `null`, `true`, `false`, `{}`, `[]`, `""`}
	writeValues := `["1","1.0","1E400","-0","\"\\ud800\"","[1,{\"a\":null}]","{\"b\":1,\"a\":2}","null","\"\\u00e9\""]`
	script := func() string {
		var b strings.Builder
		b.WriteString("const writes = " + writeValues + "; const keys = [")
		for _, key := range keys {
			b.WriteString("String.fromCharCode(")
			for i, unit := range key {
				if i > 0 {
					b.WriteString(",")
				}
				fmt.Fprint(&b, unit)
			}
			b.WriteString("),")
		}
		b.WriteString("]; const seen = keys.map((k) => [load(k), String(load(k))]);")
		b.WriteString(" keys.forEach((k, i) => { if (i % 3 === 0) store(k, undefined); else store(k, JSON.parse(writes[i % writes.length])); });")
		b.WriteString(" return seen;")
		return b.String()
	}
	type probe struct {
		Keys   [][]uint16 `json:"keys"`
		Values []string   `json:"values"`
		Script string     `json:"script"`
	}
	rng := rand.New(rand.NewPCG(3, 5))
	var probes []probe
	for range 120 {
		p := probe{Script: script()}
		for _, key := range keys {
			if rng.IntN(2) == 0 {
				p.Keys = append(p.Keys, key)
				p.Values = append(p.Values, values[rng.IntN(len(values))])
			}
		}
		probes = append(probes, p)
	}
	type outcome struct {
		Value  string `json:"value"`
		Err    string `json:"err"`
		Writes string `json:"writes"`
	}
	wasmPath, err := filepath.Abs("assets/quickjs.wasm")
	if err != nil {
		t.Fatal(err)
	}
	scriptPath, err := filepath.Abs("testdata/store_oracle.mjs")
	if err != nil {
		t.Fatal(err)
	}
	var want []outcome
	pioracle.Run(t, `
const { spawnSync } = await import("node:child_process");
const child = spawnSync(process.execPath, [process.env.ORACLE_SCRIPT], { input: JSON.stringify(input), encoding: "utf8", maxBuffer: 1 << 28 });
if (child.status !== 0) throw new Error(child.stderr);
emit(JSON.parse(child.stdout));
`, probes, &want, "QUICKJS_WASM="+wasmPath, "ORACLE_SCRIPT="+scriptPath)
	bad := 0
	for i, p := range probes {
		store := map[string]json.RawMessage{}
		for j, key := range p.Keys {
			store[jsstring.FromUTF16(key)] = json.RawMessage(p.Values[j])
		}
		sandbox, err := codemode.NewSandbox(codemode.SandboxOptions{TimeoutMs: 10_000})
		if err != nil {
			t.Fatal(err)
		}
		result, err := sandbox.Execute(t.Context(), p.Script, codemode.ExecuteOptions{Store: store})
		_ = sandbox.Close()
		if err != nil {
			t.Fatal(err)
		}
		got := outcome{}
		if result.OK {
			got.Value = string(result.Value)
			got.Writes = describeWrites(t, result.StoreWrites)
		} else {
			got.Err = result.Error.Message
		}
		if got != want[i] {
			if bad++; bad <= 5 {
				t.Errorf("store %v %v\n  PiG %+v\n  Pi  %+v", p.Keys, p.Values, got, want[i])
			}
		}
	}
	if bad > 5 {
		t.Errorf("%d of %d stores differ", bad, len(probes))
	}
}

// describeWrites writes a store's writes as the oracle script does: the UTF-16 units of each key, then the canonical JSON text of each value, in key order.
func describeWrites(t *testing.T, writes codemode.StoreWrites) string {
	t.Helper()
	var set []string
	for key, value := range writes.Set {
		canonical, err := jsonstringify.Canonicalize(value)
		if err != nil {
			t.Fatalf("store write %q is not JSON: %v", value, err)
		}
		set = append(set, fmt.Sprintf("%v=%s", jsstring.ToUTF16(key), canonical))
	}
	slices.Sort(set)
	var deleted []string
	for _, key := range writes.Delete {
		deleted = append(deleted, fmt.Sprint(jsstring.ToUTF16(key)))
	}
	slices.Sort(deleted)
	return fmt.Sprintf("set %v delete %v", set, deleted)
}
