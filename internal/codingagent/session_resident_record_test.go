package codingagent

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/sessionentry"
	"github.com/MichaelKinsy/PiG/test/longsession"
)

// holdsItsRecord reports whether entry keeps a record: Raw returns that record's bytes on every call, while an entry without one encodes its
// fields afresh each time.
func holdsItsRecord(entry SessionEntry) bool {
	a, b := entry.Raw(), entry.Raw()
	return len(a) > 0 && &a[0] == &b[0]
}

// A resumed session holds the context entries it reads back without their records when the entry encodes to its record again, and keeps the
// record otherwise: a record Pi or another writer spelled as JSON.stringify does not (a number's spelling, an escape, the members' order).
// Every entry serves its line's bytes.
func TestResumedContextEntriesServeTheirLines(t *testing.T) {
	lines := []string{
		`{"type":"session","version":3,"id":"s1","timestamp":"2026-10-13T08:00:00.000Z","cwd":"/project"}`,
		`{"type":"message","id":"a1","parentId":null,"timestamp":"2026-10-13T08:00:01.000Z","message":{"role":"user","content":"read main.go PAD","timestamp":1760000000000}}`,
		`{"type":"message","id":"a2","parentId":"a1","timestamp":"2026-10-13T08:00:02.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"c1","name":"read","arguments":{"path":"main.goPAD"}}],"api":"openai-completions","provider":"faux","model":"faux-1","usage":{"input":10,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":12,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"toolUse","timestamp":1760000000001}}`,
		`{"type":"message","id":"a3","parentId":"a2","timestamp":"2026-10-13T08:00:03.000Z","message":{"role":"toolResult","toolCallId":"c1","toolName":"read","content":[{"type":"text","text":"package main\n\tfunc main() {\"<&>\"}\nPAD"}],"isError":false,"timestamp":1760000000002}}`,
		`{"type":"message","id":"a4","parentId":"a3","timestamp":"2026-10-13T08:00:04.000Z","message":{"role":"user","content":"unknown member PAD","timestamp":1760000000003,"extra":{"b":1,"a":2}}}`,
		`{"type":"message","id":"a5","parentId":"a4","timestamp":"2026-10-13T08:00:05.000Z","message":{"role":"user","content":"number spelled 1e3 PAD","timestamp":1.760000000004e12}}`,
		`{"type":"message","id":"a6","parentId":"a5","timestamp":"2026-10-13T08:00:06.000Z","message":{"role":"user","content":"escaped \u00e9 \/ PAD","timestamp":1760000000005}}`,
		`{"type":"message","parentId":"a6","id":"a7","timestamp":"2026-10-13T08:00:07.000Z","message":{"role":"user","content":"members reordered PAD","timestamp":1760000000006}}`,
		`{"type":"message","id":"a8","parentId":"a7","timestamp":"2026-10-13T08:00:08.000Z","message":{"role":"assistant","content":[{"type":"text","text":"done PAD"}],"api":"openai-completions","provider":"faux","model":"faux-1","usage":{"input":10,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":12,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":1760000000007}}`,
		`{"type":"message","id":"a9","parentId":"a8","timestamp":"2026-10-13T08:00:09.000Z","message":{"role":"bashExecution","command":"ls PAD","output":"a\nb","exitCode":0,"cancelled":false,"truncated":false,"timestamp":1760000000008,"zeta":[1,{"y":2,"x":1}]}}`,
		`{"type":"message","id":"a10","parentId":"a9","timestamp":"2026-10-13T08:00:10.000Z","message":{"role":"user","content":"a member after the message","timestamp":1760000000009},"extra":null}`,
		`{"type":"message","id":"a11","parentId":"a10","timestamp":"2026-10-13T08:00:11.000Z","message":{"role":"user","content":"parentId again after the message","timestamp":1760000000010},"parentId":"a10"}`,
	}
	// Entries the session holds without their records: they are JSON.stringify's text of the members the entry writes. a4's and a9's
	// messages keep the text they were decoded from (an unknown member, a custom role) and write it. The others are spelled as
	// JSON.stringify does not write them.
	reencodes := map[string]bool{"a1": true, "a2": true, "a3": true, "a4": true, "a8": true, "a9": true}
	for i := range lines {
		lines[i] = strings.ReplaceAll(lines[i], " PAD", "")
		lines[i] = strings.ReplaceAll(lines[i], "PAD", "")
	}
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	session, err := NewSessionManagerWithDir(filepath.Dir(path), filepath.Dir(path)).Open(path)
	if err != nil {
		t.Fatal(err)
	}
	session.BuildSessionContext() // the context a resume starts from makes its entries resident
	entries := session.GetEntries()
	if len(entries) != len(lines)-1 {
		t.Fatalf("%d entries, the file holds %d", len(entries), len(lines)-1)
	}
	resident := 0
	for i, entry := range entries {
		id := entry.Base().ID
		if got := entry.Raw(); !bytes.Equal(got, []byte(lines[i+1])) {
			t.Fatalf("entry %s serves\n%s\nits line is\n%s", id, got, lines[i+1])
		}
		session.mu.RLock()
		slotEntry := session.slots[session.slotOrZero(id)].entry
		session.mu.RUnlock()
		if slotEntry == nil {
			continue
		}
		resident++
		if kept := holdsItsRecord(slotEntry); kept == reencodes[id] {
			t.Fatalf("entry %s holds its record = %v; it encodes to its line again = %v", id, kept, reencodes[id])
		}
	}
	if resident != len(entries) {
		t.Fatalf("%d of %d entries resident after the context was built", resident, len(entries))
	}
}

// recordIsEncodingOf is sound over every message record of the parity scenarios' session files (written by Pi, by hand, poisoned, legacy) and
// of a generated long session: whenever it holds, the entry without its record encodes to the record byte for byte.
func TestRecordIsEncodingOfIsSound(t *testing.T) {
	var corpora [][]byte
	paths, err := filepath.Glob("../../test/parity/scenarios/*/testdata/*/sessions/*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	more, _ := filepath.Glob("../../test/parity/scenarios/*/testdata/sessions/*.jsonl")
	paths = append(paths, more...)
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		corpora = append(corpora, data)
	}
	var generated bytes.Buffer
	if _, err := longsession.Generate(&generated, longsession.Options{Tokens: 200000, Window: 200000, Seed: 3, Cwd: "/project"}); err != nil {
		t.Fatal(err)
	}
	corpora = append(corpora, generated.Bytes())
	// Records whose members a decode does not write back as they are: a member after the message, parentId repeated, integer-like keys, the
	// base members reordered or missing.
	head := `{"type":"message","id":"0b9a7059","parentId":null,"timestamp":"2026-01-01T00:05:00.000Z",`
	message := `"message":{"role":"user","content":"<&>","timestamp":1}`
	corpora = append(corpora, []byte(strings.Join([]string{
		head + message + `,"extra":null}`,
		head + message + `,"extra":{"b":1,"a":2}}`,
		head + message + `,"parentId":"override"}`,
		head + message + `,"7":"seven","extra":1,"2":"two"}`,
		`{"parentId":null,"type":"message","id":"0b9a7059","timestamp":"2026-01-01T00:05:00.000Z",` + message + `}`,
		`{"type":"message","id":"0b9a7059","timestamp":"2026-01-01T00:05:00.000Z",` + message + `}`,
		head + message + `}`,
	}, "\n")))
	var scratch []byte
	held, messages := 0, 0
	for _, data := range corpora {
		for line := range bytes.SplitSeq(data, []byte("\n")) {
			line = bytes.TrimSpace(line)
			message, ok := asMessage(sessionentry.DecodeSessionEntry(bytes.Clone(line)))
			if !ok {
				continue
			}
			messages++
			bare := sessionentry.WithRecord(message, nil)
			if !recordIsEncodingOf(bare, line, &scratch) {
				continue
			}
			held++
			if encoded, err := bare.AppendStringify(nil); err != nil || !bytes.Equal(encoded, line) {
				t.Fatalf("recordIsEncodingOf holds, but the entry encodes to\n%s (%v)\nnot its record\n%s", encoded, err, line)
			}
		}
	}
	t.Logf("%d session files, %d message records: %d held without their records", len(corpora), messages, held)
	if len(paths) < 20 || held == 0 || held == messages {
		t.Fatalf("%d files, %d of %d records held: the corpus no longer covers both outcomes", len(paths), held, messages)
	}
}
