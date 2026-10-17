package delta

import (
	"bufio"
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

func mustObj(t *testing.T, v any) *jv.Object {
	t.Helper()
	o, ok := v.(*jv.Object)
	if !ok {
		t.Fatalf("not an object: %v", v)
	}
	return o
}

func field(o *jv.Object, k string) any { v, _ := o.Get(k); return v }

// walk resolves a step path from the draft root to a container handle.
func walk(t *testing.T, root *Obj, path []any) any {
	t.Helper()
	var cur any = root
	for _, seg := range path {
		switch c := cur.(type) {
		case *Obj:
			cur = c.Get(seg.(string))
		case *Arr:
			cur = c.Get(int(seg.(float64)))
		default:
			t.Fatalf("path %v leaves containers", path)
		}
	}
	return cur
}

func applyStep(t *testing.T, root *Obj, s *jv.Object) {
	t.Helper()
	target := walk(t, root, field(s, "path").([]any))
	op := field(s, "op").(string)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	if a, ok := target.(*Arr); ok {
		switch op {
		case "push":
			_, err := a.Push(field(s, "values").([]any)...)
			must(err)
		case "unshift":
			_, err := a.Unshift(field(s, "values").([]any)...)
			must(err)
		case "pop":
			a.Pop()
		case "shift":
			a.Shift()
		case "splice":
			_, err := a.Splice(int(field(s, "start").(float64)), int(field(s, "remove").(float64)), field(s, "items").([]any)...)
			must(err)
		case "setIndex":
			must(a.Set(int(field(s, "index").(float64)), field(s, "value")))
		default:
			t.Fatalf("array step %s", op)
		}
		return
	}
	o := target.(*Obj)
	switch op {
	case "set":
		must(o.Set(field(s, "key").(string), field(s, "value")))
	case "delete":
		o.Delete(field(s, "key").(string))
	default:
		t.Fatalf("object step %s", op)
	}
}

// TestTrackerMatchesChord replays testdata/corpus.jsonl: random mutation
// programs run through Chord 1.0.4's tracker (testdata/gen.mjs), with the
// emitted operations and the prepared value as the oracle.
func TestTrackerMatchesChord(t *testing.T) {
	path := "testdata/corpus.jsonl"
	if p := os.Getenv("DELTA_CORPUS"); p != "" {
		path = p // a freshly generated corpus (SEED=… N=… node testdata/gen.mjs)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	n, failures := 0, 0
	for sc.Scan() {
		n++
		line := mustObj(t, mustParse(t, sc.Bytes()))
		base := mustObj(t, field(line, "base"))
		wantOps := jv.Stringify(field(line, "ops"))
		wantValue := jv.Stringify(field(line, "value"))
		tr := Track(base)
		ch := tr.BeginChange()
		for _, s := range field(line, "steps").([]any) {
			applyStep(t, ch.State(), mustObj(t, s))
		}
		prepared, err := ch.Prepare()
		if err != nil {
			t.Fatalf("line %d: prepare: %v", n, err)
		}
		gotOps := jv.Stringify(OpsValue(prepared.Ops()))
		gotValue := jv.Stringify(prepared.Value())
		if gotOps != wantOps || gotValue != wantValue {
			failures++
			if failures <= 5 {
				t.Errorf("line %d\n base  %s\n steps %s\n ops   got %s\n      want %s\n value got %s\n      want %s",
					n, jv.Stringify(base), jv.Stringify(field(line, "steps")), gotOps, wantOps, gotValue, wantValue)
			}
		}
	}
	if n < 2000 && os.Getenv("DELTA_CORPUS") == "" {
		t.Fatalf("corpus too small: %d", n)
	}
	if failures > 0 {
		t.Fatalf("%d of %d programs differ from Chord", failures, n)
	}
}

func mustParse(t *testing.T, b []byte) any {
	t.Helper()
	v, err := jv.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
