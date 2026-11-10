package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// A session file Pi wrote holds lone UTF-16 surrogates as the escape `\ud800` in every kind of entry. Resuming it must read each unit, and the
// copies Pig writes (a fork of the file, a clone of the branch) and the lines it appends must leave every Pi-written line byte for byte as it was.
func TestSessionFileWithLoneSurrogatesResumesAndResavesByteForByte(t *testing.T) {
	const usage = `{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}`
	const header = `{"type":"session","version":3,"id":"poisoned","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/work"}`
	entries := []string{
		`{"type":"message","id":"e1","parentId":null,"timestamp":"2026-01-01T00:00:01.000Z","message":{"role":"user","content":"hi \ud83d<&>","timestamp":1}}`,
		`{"type":"message","id":"e2","parentId":"e1","timestamp":"2026-01-01T00:00:02.000Z","message":{"role":"assistant","content":[{"type":"text","text":"x\ud800"},{"type":"toolCall","id":"c","name":"n","arguments":{"b":"\udfff","a":["\ud800"]}}],"api":"x","provider":"p","model":"m","usage":` + usage + `,"stopReason":"toolUse","timestamp":2}}`,
		`{"type":"message","id":"e3","parentId":"e2","timestamp":"2026-01-01T00:00:03.000Z","message":{"role":"toolResult","toolCallId":"c","toolName":"n","content":[{"type":"text","text":"r\ud800"}],"details":{"d":"\udfff"},"isError":false,"timestamp":3}}`,
		`{"type":"custom","id":"e4","parentId":"e3","timestamp":"2026-01-01T00:00:04.000Z","customType":"x","data":{"k":"\ud800","<":"&"}}`,
		`{"type":"compaction","id":"e5","parentId":"e4","timestamp":"2026-01-01T00:00:05.000Z","summary":"s\ud800","firstKeptEntryId":"e1","tokensBefore":5,"details":{"d":"\udfff"},"fromHook":false}`,
		`{"type":"custom_message","id":"e6","parentId":"e5","timestamp":"2026-01-01T00:00:06.000Z","customType":"n","content":"c\ud800","display":true,"details":{"x":"\udfff"}}`,
		`{"type":"branch_summary","id":"e7","parentId":"e6","timestamp":"2026-01-01T00:00:07.000Z","fromId":"e1","summary":"b\ud800"}`,
		`{"type":"session_info","id":"e8","parentId":"e7","timestamp":"2026-01-01T00:00:08.000Z","name":"n\udfff"}`,
	}
	lines := append([]string{header}, entries...)

	// The fixture is what Pi writes: JSON.stringify(JSON.parse(line)) is the line.
	input, err := jsstring.MarshalKeepingSurrogates(lines)
	if err != nil {
		t.Fatal(err)
	}
	var piLines []string
	if err := jsstring.UnmarshalKeepingSurrogates(testenv.NodeJSON(t, `return input.map((line) => JSON.stringify(JSON.parse(line)));`, input), &piLines); err != nil {
		t.Fatal(err)
	}
	for i := range lines {
		if piLines[i] != lines[i] {
			t.Fatalf("fixture line %d is not Pi's bytes:\n got  %s\n Pi's %s", i, lines[i], piLines[i])
		}
	}

	sm := tempSessionMgr(t)
	source := filepath.Join(sm.SessionDir(), "poisoned.jsonl")
	original := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(source, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	read := func(path string) []string {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	}

	resumed, err := sm.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(resumed.GetEntries()); got != len(entries) {
		t.Fatalf("resumed %d entries, want %d", got, len(entries))
	}
	for i, entry := range resumed.GetEntries() {
		if string(entry.Raw()) != entries[i] {
			t.Errorf("resumed entry %d = %s, want %s", i, entry.Raw(), entries[i])
		}
	}

	fork, err := sm.ForkFromFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if got := read(fork.Path()); len(got) != len(lines) || !equalLines(got[1:], entries) {
		t.Errorf("fork of the file changed entries:\n%s", strings.Join(got, "\n"))
	}

	clone, err := sm.Clone(resumed, "e8")
	if err != nil {
		t.Fatal(err)
	}
	if got := read(clone.Path()); !equalLines(got[1:], entries) {
		t.Errorf("clone of the branch changed entries:\n%s", strings.Join(got, "\n"))
	}

	// Appending to the resumed session leaves what Pi wrote untouched.
	if _, err := resumed.AppendMessage(mkUserMsg("next" + jsstring.FromUTF16([]uint16{0xd800}))); err != nil {
		t.Fatal(err)
	}
	if got := read(source); !equalLines(got[:len(lines)], lines) || !strings.Contains(got[len(lines)], `"content":[{"type":"text","text":"next\ud800"}]`) {
		t.Errorf("append changed the file:\n%s", strings.Join(got, "\n"))
	}
}

func equalLines(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
