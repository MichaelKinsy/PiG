package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// TestLoadEntriesFromFileMatchesPi runs loadEntriesFromFile of Pi 1.0.4 and LoadEntriesFromFile over session file texts: headers, malformed and JSON-falsy lines, blank lines, CRLF, a missing final newline (which both repair), invalid UTF-8 and a missing or ill-typed header.
func TestLoadEntriesFromFileMatchesPi(t *testing.T) {
	header := `{"type":"session","version":3,"id":"abc","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/w"}`
	entry := `{"type":"message","id":"1","parentId":null,"timestamp":"2026-01-01T00:00:00.000Z","message":{"role":"user","content":"hi"}}`
	texts := []string{
		"", "\n", header, header + "\n", header + "\n" + entry, header + "\n" + entry + "\n", header + "\r\n" + entry + "\r\n",
		header + "\nnot json\n" + entry + "\n", header + "\n\n   \n" + entry + "\n", header + "\nnull\nfalse\n0\n\"\"\n1\ntrue\n\"s\"\n[]\n{}\n" + entry,
		entry + "\n" + header + "\n", `{"type":"session","id":5}` + "\n" + entry, `{"type":"session"}` + "\n" + entry, "garbage\n" + header + "\n" + entry + "\n",
		header + "\n{\"type\":\"message\",\"x\":\"\xff\xfe\"}\n", header + "\n" + `{"a":1e300}` + "\n" + `{"b":-0}` + "\n" + `{"big":12345678901234567890}` + "\n",
		header + "\n" + `{"dup":1,"dup":2,"2":0,"1":1}` + "\n", "\ufeff" + header + "\n" + entry + "\n", header + "\n" + entry[:40],
		header + "\n" + `{"s":"\ud800"}` + "\n" + `{"u":"é😀\u2028"}`,
	}
	dir := t.TempDir()
	paths := make([]string, len(texts))
	oraclePaths := make([]string, len(texts))
	for i, text := range texts {
		paths[i] = filepath.Join(dir, "pig-"+strconv.Itoa(i)+".jsonl")
		oraclePaths[i] = filepath.Join(dir, "pi-"+strconv.Itoa(i)+".jsonl")
		for _, p := range []string{paths[i], oraclePaths[i]} {
			if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	var want []struct {
		Entries []any  `json:"entries"`
		File    string `json:"file"`
	}
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/core/session-manager.js");
const fs = await import("node:fs");
emit(input.map((p) => { const entries = mod.loadEntriesFromFile(p); return { entries, file: fs.readFileSync(p).toString("latin1") }; }));`, oraclePaths, &want)
	for i, text := range texts {
		records, err := LoadEntriesFromFile(paths[i])
		if err != nil {
			t.Errorf("text %d: %v", i, err)
			continue
		}
		got := make([]any, len(records))
		for j, raw := range records {
			if err := json.Unmarshal(raw.Raw(), &got[j]); err != nil {
				t.Fatalf("text %d: Pig returned invalid JSON %q: %v", i, raw, err)
			}
		}
		if len(got) == 0 && len(want[i].Entries) == 0 {
			got = want[i].Entries
		}
		if !reflect.DeepEqual(got, want[i].Entries) {
			t.Errorf("text %d %q: entries\n  Pig %v\n  Pi  %v", i, text, got, want[i].Entries)
		}
		file, _ := os.ReadFile(paths[i])
		var latin []rune
		for _, b := range file {
			latin = append(latin, rune(b))
		}
		if string(latin) != want[i].File {
			t.Errorf("text %d %q: file after load %q, Pi %q", i, text, string(latin), want[i].File)
		}
	}
}

// TestDefaultSessionDirMatchesPi compares the directory name getDefaultSessionDir of Pi 1.0.4 derives from a cwd with GetDefaultSessionDirPath.
func TestDefaultSessionDirMatchesPi(t *testing.T) {
	agentDir := t.TempDir()
	cwds := []string{"/", "/tmp/foo", "/tmp/foo/", "//double", `\back\slash`, `C:\Users\x`, "/a:b/c", "/é/😀", "/with space/x", "/.hidden/--x--", "/tmp/foo/../bar", "/a//b"}
	var want []string
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/core/session-manager.js");
const path = await import("node:path");
emit(input.cwds.map((cwd) => path.relative(input.agentDir, mod.getDefaultSessionDir(cwd, input.agentDir))));`, map[string]any{"cwds": cwds, "agentDir": agentDir}, &want)
	for i, cwd := range cwds {
		got, err := filepath.Rel(agentDir, GetDefaultSessionDirPath(cwd, agentDir))
		if err != nil || got != want[i] {
			t.Errorf("GetDefaultSessionDirPath(%q) = %q, Pi %q", cwd, got, want[i])
		}
	}
}
