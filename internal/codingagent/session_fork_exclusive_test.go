package codingagent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

const forkExclusiveSource = `{"type":"session","version":3,"id":"src-id","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/work/src"}` + "\n"

// forkFrom writes the new header with flag "wx" (session-manager.ts), so a fork whose timestamp and id name an existing file
// fails with EEXIST and leaves the file as it was, where ForkFromFile used to truncate it.
func TestForkFromFileDoesNotOverwriteAnExistingFile(t *testing.T) {
	fixed := time.Date(2026, 2, 3, 4, 5, 6, 789_000_000, time.UTC)
	restore := sessionClock
	sessionClock = func() time.Time { return fixed }
	t.Cleanup(func() { sessionClock = restore })

	root := t.TempDir()
	source := filepath.Join(root, "source.jsonl")
	if err := os.WriteFile(source, []byte(forkExclusiveSource), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "sessions")
	existing := filepath.Join(dir, "2026-02-03T04-05-06-789Z_dup.jsonl")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const sentinel = "written by someone else\n"
	if err := os.WriteFile(existing, []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}
	forked, err := NewSessionManagerWithDir("/work/target", dir).ForkFromFile(source, "dup")
	if want := "EEXIST: file already exists, open '" + existing + "'"; err == nil || err.Error() != want {
		t.Fatalf("ForkFromFile = %v, %v; want error %q", forked, err, want)
	}
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("error %v does not wrap os.ErrExist", err)
	}
	if data, readErr := os.ReadFile(existing); readErr != nil || string(data) != sentinel {
		t.Fatalf("existing file was changed: %q, %v", data, readErr)
	}
}

// The same collision against Pi: SessionManager.forkFrom with a fixed Date reports EEXIST and keeps the file.
func TestSessionForkFromFileExistingTargetMatchesPiOracle(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.jsonl")
	if err := os.WriteFile(source, []byte(forkExclusiveSource), 0o600); err != nil {
		t.Fatal(err)
	}
	const sentinel = "written by someone else\n"
	type result struct {
		Error   string `json:"error"`
		Content string `json:"content"`
	}
	var want result
	piDir := filepath.Join(root, "pi")
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/core/session-manager.js");
const fs = await import("node:fs");
const path = await import("node:path");
const RealDate = Date;
globalThis.Date = class extends RealDate {
	constructor(...args) { super(...(args.length ? args : ["2026-02-03T04:05:06.789Z"])); }
};
fs.mkdirSync(input.piDir, { recursive: true });
const existing = path.join(input.piDir, "2026-02-03T04-05-06-789Z_dup.jsonl");
fs.writeFileSync(existing, input.sentinel);
let error = "";
try {
	mod.SessionManager.forkFrom(input.source, "/work/target", input.piDir, { id: "dup" });
} catch (e) {
	error = e.message.split(input.piDir).join("<dir>");
}
emit({ error, content: fs.readFileSync(existing, "utf8") });`, map[string]any{"source": source, "piDir": piDir, "sentinel": sentinel}, &want, "HOME=/home/oracle")

	fixed := time.Date(2026, 2, 3, 4, 5, 6, 789_000_000, time.UTC)
	restore := sessionClock
	sessionClock = func() time.Time { return fixed }
	t.Cleanup(func() { sessionClock = restore })
	dir := filepath.Join(root, "go")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(dir, "2026-02-03T04-05-06-789Z_dup.jsonl")
	if err := os.WriteFile(existing, []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}
	var have result
	if _, err := NewSessionManagerWithDir("/work/target", dir).ForkFromFile(source, "dup"); err != nil {
		have.Error = strings.ReplaceAll(err.Error(), dir, "<dir>")
	}
	data, err := os.ReadFile(existing)
	if err != nil {
		t.Fatal(err)
	}
	have.Content = string(data)
	if have != want {
		t.Fatalf("Go %+v\nPi %+v", have, want)
	}
}
