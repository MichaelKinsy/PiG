package codingagent

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
)

// pi: packages/coding-agent/src/core/session-manager.ts loadEntriesFromFile. Corrupted session files (garbage and blank lines, invalid UTF-8, a BOM, truncation, a bad header, non-object records, CRLF, an unterminated last line) load the same entries and leave the same bytes behind.
func TestLoadEntriesFromFileMatchesPiOnCorruptedFiles(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatal("node is required: ", err)
	}
	rng := rand.New(rand.NewSource(20261010))
	header := `{"type":"session","version":3,"id":"abc","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/w"}`
	records := []string{
		`{"type":"message","id":"a1","parentId":null,"timestamp":"2026-01-01T00:00:01.000Z","message":{"role":"user","content":"hi é 日本","timestamp":1}}`,
		`{"type":"thinking_level_change","id":"a2","parentId":"a1","timestamp":"2026-01-01T00:00:02.000Z","thinkingLevel":"high"}`,
		`{"type":"custom","id":"a3","parentId":"a2","timestamp":"2026-01-01T00:00:03.000Z","customType":"x","data":{"b":1,"a":[1,2]}}`,
		`{"type":"label","id":"a4","parentId":"a3","timestamp":"2026-01-01T00:00:04.000Z","targetId":"a1","label":"L"}`,
	}
	garbage := []string{"", " ", "\t", "{", "}", "[", "null", "false", "0", "1", `""`, `"s"`, "[]", "{}", "true", "nope", `{"type":"session"}`, `{"type":"session","id":1}`, `{"type":"session","id":"z"}`, "\u00a0", "\u00a0{}", "\ufeff{}", "\x00", "\xff\xfe", `{"a":"` + "\xc3" + `"}`, "{\"a\":1}\r", "-0", "1e999", `{"__proto__":{"x":1}}`, `{"1":1,"a":2,"0":3}`}
	lines := func() []string {
		out := []string{header}
		for range rng.Intn(5) {
			out = append(out, records[rng.Intn(len(records))])
		}
		return out
	}
	var files [][]byte
	for range 3000 {
		ls := lines()
		for range rng.Intn(4) {
			at := rng.Intn(len(ls) + 1)
			switch rng.Intn(6) {
			case 0:
				ls = append(ls[:at], append([]string{garbage[rng.Intn(len(garbage))]}, ls[at:]...)...)
			case 1:
				if at < len(ls) {
					ls = append(ls[:at], ls[at+1:]...)
				}
			case 2:
				if at < len(ls) && len(ls[at]) > 0 {
					cut := rng.Intn(len(ls[at]))
					ls[at] = ls[at][:cut]
				}
			case 3:
				if at < len(ls) && len(ls[at]) > 0 {
					i := rng.Intn(len(ls[at]))
					ls[at] = ls[at][:i] + string([]byte{byte(rng.Intn(256))}) + ls[at][i:]
				}
			case 4:
				if at < len(ls) {
					ls[at] += "\r"
				}
			default:
				if at < len(ls) {
					ls = append(ls, ls[at])
				}
			}
		}
		eol := "\n"
		if rng.Intn(8) == 0 {
			eol = "\r\n"
		}
		content := strings.Join(ls, eol)
		if rng.Intn(3) != 0 {
			content += eol
		}
		if rng.Intn(15) == 0 {
			content = "\ufeff" + content
		}
		files = append(files, []byte(content))
	}
	files = append(files, nil, []byte("\n"), []byte(header), []byte(header+"\n"), []byte(header+"\n  "), []byte(header+"\n\n\n"),
		// Invalid UTF-8 inside a record: readFileSync's decoder writes one U+FFFD per maximal invalid subpart, not one per byte.
		[]byte(header+"\n"+`{"type":"message","id":"a","parentId":null,"timestamp":"t","message":{"role":"user","content":"x`+"\xe6\x9c"+`"}}`+"\n"),
		[]byte(header+"\n"+`{"type":"message","id":"a","parentId":null,"timestamp":"t","message":{"role":"user","content":"x`+"\xf0\x9f\x98 \xff\xc0\x80\xed\xa0\x80\xe0\x80"+`"}}`+"\n"))
	encoded := make([]string, len(files))
	for i, f := range files {
		encoded[i] = base64.StdEncoding.EncodeToString(f)
	}
	in, _ := json.Marshal(encoded)
	cmd := exec.CommandContext(t.Context(), "node", "testdata/session_load.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(in)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, stderr.String())
	}
	var want []struct{ Entries, After string }
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	var differences []string
	loaded := 0
	for i, content := range files {
		file := filepath.Join(dir, fmt.Sprintf("%d.jsonl", i))
		if err := os.WriteFile(file, content, 0o644); err != nil {
			t.Fatal(err)
		}
		entries, err := LoadEntriesFromFile(file)
		if err != nil {
			differences = append(differences, fmt.Sprintf("file %q: Go error %v", content, err))
			continue
		}
		parts := make([]string, len(entries))
		for j, entry := range entries {
			canonical, cerr := jsonstringify.Canonicalize(entry.Raw())
			if cerr != nil {
				canonical = entry.Raw()
			}
			parts[j] = string(canonical)
		}
		got := "[" + strings.Join(parts, ",") + "]"
		after, _ := os.ReadFile(file)
		if len(entries) > 0 {
			loaded++
		}
		if got != want[i].Entries || base64.StdEncoding.EncodeToString(after) != want[i].After {
			wantAfter, _ := base64.StdEncoding.DecodeString(want[i].After)
			differences = append(differences, fmt.Sprintf("file %q\n  go: %s after %q\n  pi: %s after %q", content, got, after, want[i].Entries, wantAfter))
		}
	}
	if loaded < len(files)/3 {
		t.Fatalf("Pi loads entries from only %d of %d files", loaded, len(files))
	}
	if len(differences) > 0 {
		shown := differences
		if len(shown) > 6 {
			shown = shown[:6]
		}
		t.Errorf("%d of %d files differ from Pi:\n%s", len(differences), len(files), strings.Join(shown, "\n"))
	}
}
