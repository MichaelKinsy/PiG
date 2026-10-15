package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// session-manager.ts findMostRecentSession lists the session directory with readdirSync and stable-sorts it by mtime, and findById takes
// the first file in readdirSync order whose header has the id. Node's readdirSync order is libuv's scandir: strcmp on Unix, the file
// system's order on Windows, which on NTFS ignores case. With two sessions of equal mtime, and the same id, in B.jsonl and a.jsonl, Pig
// picks the file Pi picks.
func TestSessionDiscoveryKeepsNodesReaddirOrder(t *testing.T) {
	work := t.TempDir()
	cwd, dir := filepath.Join(work, "proj"), filepath.Join(work, "sessions")
	for _, d := range []string{cwd, dir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"B.jsonl", "a.jsonl"} {
		header, err := json.Marshal(map[string]any{"type": "session", "version": 3, "id": "same", "timestamp": "2026-01-01T00:00:00.000Z", "cwd": cwd})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, append(header, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		when := time.Unix(50, 0)
		if err := os.Chtimes(path, when, when); err != nil {
			t.Fatal(err)
		}
	}
	var want struct {
		Recent *string `json:"recent"`
		ByID   *string `json:"byId"`
	}
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/core/session-manager.js");
emit({ recent: mod.findMostRecentSession(input.dir), byId: mod.SessionManager.findById(input.cwd, "same", input.dir) ?? null });`,
		map[string]string{"dir": dir, "cwd": cwd}, &want)
	if want.Recent == nil || want.ByID == nil {
		t.Fatalf("Pi found no session: %+v", want)
	}
	sm := NewSessionManagerWithDir(cwd, dir)
	if got := sm.findMostRecent(false); got != *want.Recent {
		t.Errorf("findMostRecent = %q, Pi %q", got, *want.Recent)
	}
	if got := sm.FindByID("same"); got != *want.ByID {
		t.Errorf("FindByID = %q, Pi %q", got, *want.ByID)
	}
}
