package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

// largeMutationArgs is the arguments of a write and of an edit that each carry about 200 KB of text.
func largeMutationArgs(t *testing.T) (write, edit json.RawMessage) {
	text := strings.Repeat("\tfmt.Println(\"a line of a large <generated> file\")\n", 4000)
	var err error
	if write, err = json.Marshal(map[string]any{"path": "big.go", "content": text}); err != nil {
		t.Fatal(err)
	}
	if edit, err = json.Marshal(map[string]any{"path": "big.go", "edits": []map[string]string{{"oldText": text, "newText": text + "x"}}}); err != nil {
		t.Fatal(err)
	}
	return write, edit
}

// TestMutationArgumentsAreNotCopiedBeforeExecute pins TO-T7: reserving a write's or an edit's place in the mutation queue needs only the
// path, and preparing edit arguments already in the {path, edits: [...]} shape leaves them as they are. Neither may decode or copy the
// texts (each copied the 200 KB arguments, the edit's twice).
func TestMutationArgumentsAreNotCopiedBeforeExecute(t *testing.T) {
	writeArgs, editArgs := largeMutationArgs(t)
	dir := t.TempDir()
	queue := NewFileMutationQueue()
	write := &WriteTool{CWD: dir, Queue: queue}
	edit := &EditTool{CWD: dir, Queue: queue}
	for _, c := range []struct {
		name string
		run  func() bool
	}{
		{"write reserve", func() bool {
			ticket, ok := write.ReserveMutationOrder(writeArgs)
			if ok {
				ticket.Release()
			}
			return ok
		}},
		{"edit reserve", func() bool {
			ticket, ok := edit.ReserveMutationOrder(editArgs)
			if ok {
				ticket.Release()
			}
			return ok
		}},
		{"edit prepare", func() bool {
			prepared, err := edit.PrepareArguments(editArgs)
			return err == nil && &prepared[0] == &editArgs[0]
		}},
	} {
		if !c.run() {
			t.Fatalf("%s failed", c.name)
		}
		bytes := testing.Benchmark(func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				c.run()
			}
		}).AllocedBytesPerOp()
		if bytes > 16<<10 {
			t.Errorf("%s of %d-byte arguments allocated %d bytes/op, want <= 16384", c.name, len(editArgs), bytes)
		}
	}
}

// TestPrepareEditArgumentsScanMatchesOrderedParse checks the scan that leaves {path, edits: [...]} arguments alone against the ordered
// parse on inputs near its boundary: repeated, case-variant and escaped keys, every other edits shape, and invalid JSON.
func TestPrepareEditArgumentsScanMatchesOrderedParse(t *testing.T) {
	for _, raw := range []string{
		`{"path":"a","edits":[{"oldText":"x","newText":"y"}]}`,
		`{"edits":[],"path":"a"}`,
		`{"path":"a","edits":[{"oldText":"x","newText":"y"}],"edits":"[{\"oldText\":\"p\",\"newText\":\"q\"}]"}`,
		`{"path":"a","edits":"[{\"oldText\":\"p\",\"newText\":\"q\"}]","edits":[]}`,
		`{"path":"a","Edits":"[{\"oldText\":\"p\",\"newText\":\"q\"}]","edits":[]}`,
		`{"path":"a","edits":[],"EDITS":"[{\"oldText\":\"p\",\"newText\":\"q\"}]"}`,
		`{"path":"a","Edits":[{"oldText":"x","newText":"y"}]}`,
		`{"path":"a","\u0065dits":"[{\"oldText\":\"p\",\"newText\":\"q\"}]"}`,
		`{"path":"a","edits":[],"oldText":"p","newText":"q"}`,
		`{"path":"a","edits":[],"oldtext":"p","newText":"q"}`,
		`{"path":"a","edits":[],"oldText":"p"}`,
		`{"path":"a","oldText":"p","newText":"q"}`,
		`{"path":"a","edits":{"oldText":"p","newText":"q"}}`,
		`{"path":"a","edits":"{\"oldText\":\"p\",\"newText\":\"q\"}"}`,
		`{"path":"a","edits":null}`,
		`{"path":"a"}`,
		`{"path":"a","edits":[]} trailing`,
		`{"path":"a","edits":[}`,
		`[{"edits":[]}]`,
		`null`,
		` {"path":"<a&b>","edits":[{"oldText":"\u2028","newText":"y"}]} `,
	} {
		got, want := prepareEditArgumentsRaw(json.RawMessage(raw)), prepareEditArgumentsOrdered(json.RawMessage(raw))
		if string(got) != string(want) {
			t.Errorf("prepareEditArgumentsRaw(%s) = %s, the ordered parse gives %s", raw, got, want)
		}
	}
}
