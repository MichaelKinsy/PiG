//go:build !race

package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
)

// writeHTMLSession writes a session whose tool results are source code with <, > and &, which encoding/json's HTML escaping rewrites.
func writeHTMLSession(tb testing.TB, turns int) (path string, size int64) {
	tb.Helper()
	code := strings.Repeat("if (a < b && c > d) { return \"<div>\" + x; }\n", 400)
	var b strings.Builder
	b.WriteString(`{"type":"session","version":3,"id":"html","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/w"}` + "\n")
	parent := "null"
	for turn := range turns {
		user, result := fmt.Sprintf("u%06d", turn), fmt.Sprintf("r%06d", turn)
		fmt.Fprintf(&b, `{"type":"message","id":%q,"parentId":%s,"timestamp":"2026-01-01T00:00:00.000Z","message":{"role":"user","content":"look at <file %d> & fix","timestamp":1767225600000}}`+"\n", user, parent, turn)
		fmt.Fprintf(&b, `{"type":"message","id":%q,"parentId":%q,"timestamp":"2026-01-01T00:00:02.000Z","message":{"role":"toolResult","toolCallId":"c%d","toolName":"read","content":[{"type":"text","text":%q}],"isError":false,"timestamp":1767225602000}}`+"\n", result, user, turn, code)
		parent = fmt.Sprintf("%q", result)
	}
	path = filepath.Join(tb.TempDir(), "html.jsonl")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		tb.Fatal(err)
	}
	return path, int64(b.Len())
}

// ForkFromFile writes each record as JSON.stringify of its parsed value straight from the record, through one buffered writer: no
// HTML-escaping re-encode that sends the record down the slow canonical rewrite, no probe decode per line (PE-F10).
func TestForkFromFileAllocationBudget(t *testing.T) {
	path, size := writeHTMLSession(t, 200)
	dir := t.TempDir()
	sm := NewSessionManagerWithDir(dir, dir)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	forked, err := sm.ForkFromFile(path)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	if forked.snapshot != nil {
		defer forked.snapshot.close()
	}
	perByte := float64(after.TotalAlloc-before.TotalAlloc) / float64(size)
	t.Logf("fork of %d bytes allocated %.2f bytes per source byte", size, perByte)
	if perByte > forkBytesPerByte {
		t.Fatalf("fork allocated %.2f bytes per source byte, want at most %.2f", perByte, forkBytesPerByte)
	}
}

// forkBytesPerByte bounds the bytes ForkFromFile allocates per byte of the source file: 11.2 measured (the source load and the reload of the
// copy), 41.2 when each record was re-encoded, rewritten on the slow canonical path and probed.
const forkBytesPerByte = 14.0

// The fork's lines are the ones the encode round trip wrote: Canonicalize of the record as read equals Canonicalize of json.Marshal of the
// record (encoding/json's HTML escapes and compaction change the spelling, not the value).
func TestForkFromFileLinesMatchTheEncodeRoundTrip(t *testing.T) {
	path, _ := writeHTMLSession(t, 3)
	extra := []string{
		`{"type":"custom","id":"n1","parentId":null,"timestamp":"t","customType":"x","data":{"a":1.0,"b":1e21,"c":"<\u0026>","d":-0,"e":"\u2028\ud83d\ude00","f":"\ud800"}}`,
		`{"type":"custom","id":"d1","parentId":null,"timestamp":"t","customType":"x","data":{"z":1,"10":2,"a":{"b":"\u003cscript\u003e"},"a":3}}`,
		`{ "type" : "message" , "id":"s1","parentId":null,"timestamp":"t","message":{"role":"user","content":"&amp; <b>","timestamp":1} }`,
		`{"type":"future_kind","id":"f1","parentId":null,"timestamp":"t","payload":[1,"<",{"&":true}]}`,
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(content, []byte(strings.Join(extra, "\n")+"\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	records, err := LoadEntriesFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var want bytes.Buffer
	for _, record := range records[1:] {
		raw, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		line, err := jsonstringify.Canonicalize(raw)
		if err != nil {
			t.Fatal(err)
		}
		want.Write(line)
		want.WriteByte('\n')
	}
	dir := t.TempDir()
	forked, err := NewSessionManagerWithDir(dir, dir).ForkFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if forked.snapshot != nil {
		defer forked.snapshot.close()
	}
	got, err := os.ReadFile(forked.Path())
	if err != nil {
		t.Fatal(err)
	}
	_, body, _ := bytes.Cut(got, []byte("\n"))
	if !bytes.Equal(body, want.Bytes()) {
		t.Fatalf("fork lines differ from the encode round trip:\n got %s\nwant %s", body, want.Bytes())
	}
}
