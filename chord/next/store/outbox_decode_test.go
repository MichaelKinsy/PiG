package store

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// decodeStoreWrite assumes an object that passed the write schema (archive.go's CheckWrite) but Outbox.Read hands it
// recorded elements nobody checked, and Pi's outbox passes any JSON through. These tests are the proof that
// decodeStoreWrite on a malformed write returns an error or a value and never panics, so Outbox.Read needs no recover.

// outboxDecodeSeeds is one well-formed recorded write of every kind, with every nested structure the decoder reads
// (columns, key, row, set and where nodes, table edits, documents, ops).
var outboxDecodeSeeds = []string{
	`{"kind":"table","table":"a:b:c","version":1,"state":"ready","columns":{"a":{"type":"int","optional":true,"default":1,"version":1,"fingerprint":"f"},"b":{"type":"str"}},"key":["a","b"],"fingerprint":"f"}`,
	`{"kind":"put","table":"a:b:c","row":{"a":1,"b":"x"},"version":1,"columns":{"a":1,"b":1}}`,
	`{"kind":"index","table":"a:b:c","name":"i","columns":["a","b"]}`,
	`{"kind":"dropIndex","table":"a:b:c","name":"i"}`,
	`{"kind":"alter","table":"a:b:c","version":2,"edit":{"kind":"rename","from":"a","to":"b"}}`,
	`{"kind":"alter","table":"a:b:c","version":2,"edit":{"kind":"add","name":"c","column":{"type":"str","optional":true,"default":"d","version":2,"fingerprint":"f"}}}`,
	`{"kind":"alter","table":"a:b:c","version":2,"edit":{"kind":"json","name":"c","column":{"type":"json"}}}`,
	`{"kind":"alter","table":"a:b:c","version":2,"edit":{"kind":"drop","name":"c"}}`,
	`{"kind":"alter","table":"a:b:c","version":2,"edit":{"kind":"dropIndex","name":"i"}}`,
	`{"kind":"alter","table":"a:b:c","version":2,"edit":{"kind":"index","name":"i","columns":["a","b"]}}`,
	`{"kind":"update","table":"a:b:c","key":{"a":1},"set":{"a":{"add":[{"col":"a"},1]},"b":{"concat":[{"col":"b"},"x"]},"c":null,"d":{"json":{"x":[1]}},"e":{"replacePrefix":[{"col":"e"},"f","t"]},"f":{"when":[{"isNull":"a"},1,2]},"g":{"coalesce":[{"col":"g"},true]}},"columns":{"a":1},"count":1,"version":2}`,
	`{"kind":"delete","table":"a:b:c","key":{"a":1},"count":1}`,
	`{"kind":"updateWhere","table":"a:b:c","where":{"and":[{"eq":["a",1]},{"or":[{"gt":["a",1]},{"not":{"isNull":"b"}}]},{"inArray":["a",[1,"x",true]]},{"prefix":["b","p"]}]},"set":{"a":{"sub":[{"col":"a"},{"max":[1,{"min":[2,3]}]}]}},"columns":{"a":1},"count":2,"version":2}`,
	`{"kind":"deleteWhere","table":"a:b:c","where":{"lte":["a",2]},"count":2}`,
	`{"kind":"document.create","record":{"id":1,"owner":"o","namespace":"n","kind":"k","history":"delta","createdAt":1,"revision":1,"version":1,"fingerprint":"f","key":"k","retiredAt":2,"meta":"m"},"base":{"version":1,"fingerprint":"f","value":{"n":0}}}`,
	`{"kind":"document.change","id":1,"revision":2,"content":{"kind":"delta","version":2,"fingerprint":"f","value":{"n":1},"ops":[["set","n",1],["del","m"]]}}`,
	`{"kind":"document.retire","id":1,"revision":3}`,
	`{"kind":"document.revision","id":1,"seq":2,"content":{"kind":"delta","version":2,"value":{"n":1},"ops":[["set","n",1]]}}`,
}

// outboxWrongTypes are the replacements of every member: one value of each JSON type, plus the empty array and object.
var outboxWrongTypes = []struct {
	name  string
	value any
}{
	{"string", "x"}, {"number", float64(1.5)}, {"negative", float64(-1)}, {"bool", true}, {"null", nil},
	{"array", []any{}}, {"array of strings", []any{"x", "y"}}, {"object", map[string]any{}},
	{"object with a member", map[string]any{"kind": "x"}},
}

// mutateEach calls visit with a copy of root in which each node (every depth, members and elements) is in turn
// replaced by each wrong-typed value and removed; the path names the node.
func mutateEach(root any, visit func(path string, mutated any)) {
	var walk func(node any, path string, replace func(value any, remove bool) any)
	walk = func(node any, path string, replace func(value any, remove bool) any) {
		for _, wrong := range outboxWrongTypes {
			visit(path+" <- "+wrong.name, replace(wrong.value, false))
		}
		visit(path+" removed", replace(nil, true))
		switch typed := node.(type) {
		case map[string]any:
			for key, child := range typed {
				walk(child, path+"."+key, func(value any, remove bool) any {
					return replace(copyWith(typed, key, value, remove), false)
				})
			}
		case []any:
			for index, child := range typed {
				walk(child, fmt.Sprintf("%s[%d]", path, index), func(value any, remove bool) any {
					return replace(copyElementWith(typed, index, value, remove), false)
				})
			}
		}
	}
	walk(root, "", func(value any, remove bool) any { return value })
}

func copyWith(object map[string]any, key string, value any, remove bool) map[string]any {
	out := make(map[string]any, len(object))
	for name, member := range object {
		out[name] = member
	}
	if remove {
		delete(out, key)
	} else {
		out[key] = value
	}
	return out
}

func copyElementWith(list []any, index int, value any, remove bool) []any {
	out := make([]any, 0, len(list))
	for at, element := range list {
		switch {
		case at != index:
			out = append(out, element)
		case !remove:
			out = append(out, value)
		}
	}
	return out
}

// decodeNoPanic decodes text as typedWrite does; a panic is reported as a string.
func decodeNoPanic(text []byte) (panicked any, err error) {
	value, err := delta.DecodeJson(text)
	if err != nil {
		return nil, err
	}
	object, ok := value.(*delta.JsonObject)
	if !ok {
		return nil, nil
	}
	defer func() { panicked = recover() }()
	_, err = decodeStoreWrite(object, "outbox")
	return nil, err
}

func TestDecodeStoreWriteNeverPanicsOnMalformedWrites(t *testing.T) {
	cases := 0
	panics := map[string]string{}
	check := func(path string, mutated any) {
		text, err := json.Marshal(mutated)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		cases++
		if panicked, _ := decodeNoPanic(text); panicked != nil {
			panics[string(text)] = fmt.Sprintf("%s: %v", path, panicked)
		}
	}
	for _, seed := range outboxDecodeSeeds {
		var root any
		if err := json.Unmarshal([]byte(seed), &root); err != nil {
			t.Fatal(err)
		}
		if panicked, err := decodeNoPanic([]byte(seed)); panicked != nil || err != nil {
			t.Fatalf("seed %s: panic %v, err %v", seed, panicked, err)
		}
		mutateEach(root, check)
	}
	for text, report := range panics {
		t.Errorf("decodeStoreWrite panicked on %s\n\t%s", text, report)
	}
	if cases == 0 {
		t.Fatal("no cases ran")
	}
	t.Logf("%d malformed writes, %d panics", cases, len(panics))
}

// A non-object element and a non-array Raw reach no decodeStoreWrite: typedWrite and parseRecordedWrites keep them.
func TestParseRecordedWritesKeepsMalformedElements(t *testing.T) {
	for _, text := range []string{`[1,"x",null,true,[],{},{"kind":1},{"kind":"document.change","content":3},{"kind":"document.revision"}]`, `{}`, `3`, `null`} {
		raw, writes, err := parseRecordedWrites(text)
		if err != nil || raw == nil && text != `null` {
			t.Fatalf("%s: raw %v, err %v", text, raw, err)
		}
		for index, write := range writes {
			if _, unknown := write.(*UnknownWrite); !unknown {
				t.Errorf("%s: writes[%d] = %T, want *UnknownWrite", text, index, write)
			}
		}
	}
}
