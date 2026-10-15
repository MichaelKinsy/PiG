package codingagent

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// pi: packages/coding-agent/src/core/session-manager.ts

// listSessionsFromDir reads names with readdir, which decodes them as UTF-8, so a session file named with invalid UTF-8 is opened at its decoded path, does not exist there and is left out of the list.
func TestListSessionsLeavesOutFilesWhoseNamesAreNotValidUTF8LikePi(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("file names are arbitrary bytes only on Linux")
	}
	work := t.TempDir()
	cwd := filepath.Join(work, "proj")
	dir := filepath.Join(work, "sessions")
	for _, d := range []string{cwd, dir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for i, name := range []string{"a.jsonl", "b\xffd.jsonl", "t\xe2\x82.jsonl", "z.jsonl"} {
		body := `{"type":"session","version":3,"id":"s` + strconv.Itoa(i) + `","timestamp":"2026-01-01T00:00:00.000Z","cwd":` + strconv.Quote(cwd) + `}` + "\n" +
			`{"type":"message","id":"m","parentId":null,"timestamp":"2026-01-02T00:00:00.000Z","message":{"role":"user","content":"hi"}}` + "\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Skipf("file system refuses the name %q: %v", name, err)
		}
	}
	var want []string
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/core/session-manager.js");
const path = await import("node:path");
const infos = await mod.SessionManager.list(input.cwd, input.dir);
emit(infos.map((s) => path.basename(s.path) + "|" + s.id));`, map[string]any{"cwd": cwd, "dir": dir}, &want)
	infos, err := NewSessionManagerWithDir(cwd, dir).ListCurrentSessions()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range infos {
		got = append(got, filepath.Base(s.Path)+"|"+s.ID)
	}
	if !slices.Equal(got, want) {
		t.Errorf("PiG %q\nPi  %q", got, want)
	}
}
