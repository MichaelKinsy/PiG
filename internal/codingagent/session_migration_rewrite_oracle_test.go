package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// pi: packages/coding-agent/src/core/session-manager.ts

// Opening a version 1 or 2 session file migrates it and rewrites the whole file (_loadEntries then _rewriteFile: `${JSON.stringify(entry)}\n` for every
// parsed entry). An entry the migration does not change is still written as JSON.stringify prints the parsed record: whitespace between tokens goes,
// numbers and escapes take their JSON.stringify spelling, duplicate members collapse and integer-like member names come first. Generated ids differ by
// design and are renumbered by order of appearance.
func TestMigratedSessionFileRewriteMatchesPi(t *testing.T) {
	header := func(version string) string {
		return `{"type":"session",` + version + `"id":"s","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/w"}`
	}
	odd := `"message":{"role":"user","content":"\u00e9\u2028\ud83d\ude00\ud800\u001F\/","timestamp":1e2},"7":"x","extra":[2.50,1E+2,-0,1e400,100000000000000000000,0.0000001]`
	files := map[string][]string{
		"v2-untouched": {
			header(`"version":2,`),
			`{"type":"message","id":"aaaaaaaa","parentId":null,"timestamp":"t",` + odd + `}`,
			`{ "type" : "message", "id" : "bbbbbbbb", "parentId" : "aaaaaaaa", "timestamp" : "t", "message" : {"role":"user","content":"u","timestamp":1.50} }`,
			`{"type":"custom","id":"cccccccc","parentId":"bbbbbbbb","timestamp":"t","customType":"x","data":{"b":1,"a":2,"b":3,"10":4,"2":5}}`,
		},
		"v2-hook": {
			header(`"version":2,`),
			`{"type":"message","id":"aaaaaaaa","parentId":null,"timestamp":"t","message":{"role":"hookMessage","customType":"x","content":"\u00e9","display":true,"timestamp":1e2}}`,
			`{"type":"message","id":"bbbbbbbb","parentId":"aaaaaaaa","timestamp":"t",` + odd + `}`,
		},
		"v1": {
			header(`"version":1,`),
			`{"type":"message","timestamp":"t",` + odd + `}`,
			`{"type":"compaction","timestamp":"t","summary":"s","firstKeptEntryIndex":1,"tokensBefore":5e0}`,
		},
	}
	names := []string{"v2-untouched", "v2-hook", "v1"}
	root := t.TempDir()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(root, name+".jsonl"), []byte(strings.Join(files[name], "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var want map[string]string
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/core/session-manager.js");
const fs = await import("node:fs");
const out = {};
for (const name of input.names) {
	const dir = input.piDir + "/" + name;
	fs.mkdirSync(dir, { recursive: true });
	const source = dir + "/" + name + ".jsonl";
	fs.copyFileSync(input.root + "/" + name + ".jsonl", source);
	mod.SessionManager.open(source, dir);
	out[name] = fs.readFileSync(source, "utf8");
}
emit(out);`, map[string]any{"names": names, "root": root, "piDir": t.TempDir()}, &want, "HOME=/home/oracle")
	for _, name := range names {
		dir := t.TempDir()
		path := filepath.Join(dir, name+".jsonl")
		if err := os.WriteFile(path, []byte(strings.Join(files[name], "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := NewSessionManagerWithDir("/w", dir).Open(path); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if g, w := renumberIDs(string(got)), renumberIDs(want[name]); g != w {
			t.Errorf("%s:\nPiG %s\nPi  %s", name, g, w)
		}
	}
}
