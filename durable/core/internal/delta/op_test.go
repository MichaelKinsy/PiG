package delta

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

func parse(t *testing.T, s string) any {
	t.Helper()
	v, err := jv.ParseString(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestDecodeApplyRoundTrip(t *testing.T) {
	ops, err := DecodeOps(parse(t, `[["a",["s"]," world"],["t",["s"],2],["s",["b"],[1,2]],["p",["b"],1,0,[9]],["m",["b"],[2,0,1]],["d",["b",0]]]`))
	if err != nil {
		t.Fatal(err)
	}
	if got := jv.Stringify(OpsValue(ops)); got != `[["a",["s"]," world"],["t",["s"],2],["s",["b"],[1,2]],["p",["b"],1,0,[9]],["m",["b"],[2,0,1]],["d",["b",0]]]` {
		t.Fatal(got)
	}
	got, err := Apply(parse(t, `{"a":1,"s":"hello"}`), ops)
	if err != nil {
		t.Fatal(err)
	}
	// "hello" + " world" drops two units -> "llo world"; b = [1,9,2] permuted by [2,0,1] = [2,1,9]; delete index 0 -> [1,9]
	if jv.Stringify(got) != `{"a":1,"s":"llo world","b":[1,9]}` {
		t.Fatal(jv.Stringify(got))
	}
}

func TestApplyDoesNotMutateTarget(t *testing.T) {
	base := parse(t, `{"a":{"b":[1,2,3]},"c":"x"}`)
	before := jv.Stringify(base)
	ops, _ := DecodeOps(parse(t, `[["s",["a","b",1],5],["a",["c"],"y"],["d",["a","b",0]]]`))
	got, err := Apply(base, ops)
	if err != nil {
		t.Fatal(err)
	}
	if jv.Stringify(base) != before || jv.Stringify(got) != `{"a":{"b":[5,3]},"c":"xy"}` {
		t.Fatal(jv.Stringify(base), jv.Stringify(got))
	}
}

func TestDecodeRejects(t *testing.T) {
	for _, bad := range []string{
		`["x"]`, `["s",["a"]]`, `["s",[],1]`, `["s",["__proto__","a"],1]`, `["d",["constructor"]]`, `["a",["s"],1]`,
		`["t",["s"],-1]`, `["p",["a"],0,0]`, `["m",["a"],[0,0]]`, `["m",["a"],[1]]`, `["s",[1.5],1]`, `5`, `[]`,
	} {
		if _, err := DecodeOp(parse(t, bad)); err == nil {
			t.Errorf("DecodeOp(%s) accepted", bad)
		}
	}
}

func TestApplyPathErrors(t *testing.T) {
	base := parse(t, `{"a":[1],"s":1}`)
	for _, bad := range []string{`[["s",["z","y"],1]]`, `[["a",["s"],"x"]]`, `[["d",["a",3]]]`, `[["p",["s"],0,0,[]]]`, `[["s",["a",5],1]]`} {
		ops, err := DecodeOps(parse(t, bad))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Apply(base, ops); err == nil {
			t.Errorf("Apply(%s) succeeded", bad)
		} else if !errors.Is(err, ErrPath) && !errors.Is(err, ErrUnsafePath) {
			t.Errorf("Apply(%s): %v", bad, err)
		}
	}
}

func TestRevokedHandlePanics(t *testing.T) {
	tr := Track(parse(t, `{"a":1}`).(*jv.Object))
	ch := tr.BeginChange()
	state := ch.State()
	if _, err := ch.Prepare(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if r, _ := recover().(error); !errors.Is(r, ErrRevoked) {
			t.Fatal("handle use after Prepare must panic with ErrRevoked")
		}
	}()
	_ = state.Set("a", 2.0)
}

func TestStaleChangeAfterAdopt(t *testing.T) {
	tr := Track(parse(t, `{"a":1}`).(*jv.Object))
	one, two := tr.BeginChange(), tr.BeginChange()
	_ = one.State().Set("a", 2.0)
	p, err := one.Prepare()
	if err != nil || tr.Adopt(p) != nil {
		t.Fatal(err)
	}
	if jv.Stringify(tr.Value()) != `{"a":2}` || tr.Revision() != 1 {
		t.Fatal(jv.Stringify(tr.Value()))
	}
	if _, err := two.Prepare(); err == nil {
		t.Fatal("a change begun before an adoption must be stale")
	}
}
