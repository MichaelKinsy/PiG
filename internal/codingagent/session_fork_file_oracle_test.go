package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// SessionManager.forkFrom (session-manager.ts:1815-1865) against ForkFromFile on one corpus of source files: Pi loads the entries (malformed and falsy lines are dropped, the header must come first) and writes every non-header entry back with JSON.stringify, so the copy is normalized, not verbatim, and a failed load reports "source session file is empty or invalid". Known gap left out of the corpus: a truthy scalar record (1, "str", true, [1,2]) that Pi copies and tolerates on reload, where the Go session loader refuses a non-object record.
func TestSessionForkFromFileMatchesPiOracle(t *testing.T) {
	const header = `{"type":"session","version":3,"id":"src-id","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/work/src"}`
	msg := func(id, parent, text string) string {
		return `{"type":"message","id":"` + id + `","parentId":` + parent + `,"timestamp":"2026-01-01T00:00:01.000Z","message":{"role":"user","content":"` + text + `","timestamp":1}}`
	}
	corpus := map[string]string{
		"plain":           header + "\n" + msg("a1", "null", "hi") + "\n" + msg("a2", `"a1"`, "there") + "\n",
		"escapes":         header + "\n" + msg("a1", "null", `caf\u00e9 \ud83d\ude00 \u2028 \/ \u0041`) + "\n",
		"padded":          header + "\n   " + msg("a1", "null", "x") + "  \n\t" + msg("a2", `"a1"`, "y") + "\t\n",
		"crlf":            header + "\r\n" + msg("a1", "null", "x") + "\r\n",
		"blank-lines":     header + "\n\n" + msg("a1", "null", "x") + "\n\n\n" + msg("a2", `"a1"`, "y") + "\n",
		"malformed":       header + "\n" + msg("a1", "null", "x") + "\n{not json\n" + msg("a2", `"a1"`, "y") + "\n",
		"falsy-lines":     header + "\n0\nnull\nfalse\n\"\"\n" + msg("a1", "null", "x") + "\n",
		"numbers":         header + "\n" + `{"type":"custom","id":"n1","parentId":null,"timestamp":"t","customType":"x","data":{"a":1.0,"b":1e21,"c":1E5,"d":-0,"e":0.1e-6,"f":123456789012345678901234567890,"g":1.5e300,"h":4.9e-324}}` + "\n",
		"key-order":       header + "\n" + `{"id":"k1","parentId":null,"type":"custom","customType":"x","data":{"z":1,"a":2,"10":3,"2":4,"__proto__":5},"timestamp":"t"}` + "\n",
		"dup-keys":        header + "\n" + `{"type":"custom","id":"d1","parentId":null,"timestamp":"t","customType":"x","data":{"a":1,"a":2}}` + "\n",
		"second-header":   header + "\n" + msg("a1", "null", "x") + "\n" + strings.Replace(header, "src-id", "other", 1) + "\n" + msg("a2", `"a1"`, "y") + "\n",
		"no-final-nl":     header + "\n" + msg("a1", "null", "x"),
		"bom-header":      "\ufeff" + header + "\n" + msg("a1", "null", "x") + "\n",
		"header-second":   msg("a1", "null", "x") + "\n" + header + "\n",
		"header-only":     header + "\n",
		"empty":           "",
		"garbage":         "hello\nworld\n",
		"unicode-sep":     header + "\n" + msg("a1", "null", "a\u2028b\u2029c") + "\n",
		"nbsp-led":        header + "\n\u00a0" + msg("a1", "null", "x") + "\n",
		"null-entry":      header + "\nnull\n" + msg("a1", "null", "x") + "\n",
		"zz-extra-top":    header + "\n" + `{"type":"message","id":"a1","parentId":null,"timestamp":"t","extra":{"q":1},"message":{"role":"user","content":"x","timestamp":1,"vendor":true}}` + "\n",
		"zz-unknown-type": header + "\n" + `{"type":"future_kind","id":"f1","parentId":null,"timestamp":"t","payload":[1,2]}` + "\n",
		"zz-missing":      header + "\n" + `{"type":"message","id":"a1","parentId":null,"message":{"role":"user","content":[{"type":"text","text":"x"}]}}` + "\n",
		"zz-assistant":    header + "\n" + `{"type":"message","id":"a1","parentId":null,"timestamp":"t","message":{"role":"assistant","content":[{"type":"text","text":"x","textSignature":"s"},{"type":"toolCall","id":"c","name":"n","arguments":{"b":1,"a":2}}],"api":"x","provider":"p","model":"m","usage":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":1}}` + "\n",
		"zz-compaction":   header + "\n" + msg("a1", "null", "x") + "\n" + `{"type":"compaction","id":"c1","parentId":"a1","timestamp":"t","summary":"s","firstKeptEntryId":"a1","tokensBefore":5}` + "\n",
		"zz-label-null":   header + "\n" + msg("a1", "null", "x") + "\n" + `{"type":"label","id":"l1","parentId":"a1","timestamp":"t","targetId":"a1","label":null}` + "\n",
		"lone-surrogate":  header + "\n" + msg("a1", "null", `\ud800`) + "\n",
	}
	names := make([]string, 0, len(corpus))
	root := t.TempDir()
	for name, content := range corpus {
		if err := os.WriteFile(filepath.Join(root, name+".jsonl"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	type result struct {
		Lines []string `json:"lines"`
		Error string   `json:"error"`
	}
	var want map[string]result
	piDir := t.TempDir()
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/core/session-manager.js");
const fs = await import("node:fs");
const path = await import("node:path");
const out = {};
for (const name of input.names) {
	const source = path.join(input.root, name + ".jsonl");
	try {
		const sm = mod.SessionManager.forkFrom(source, "/work/target", path.join(input.piDir, name));
		const lines = fs.readFileSync(sm.getSessionFile(), "utf8").split("\n").filter((l) => l !== "");
		out[name] = { lines: lines.slice(1) };
	} catch (e) {
		out[name] = { error: e.message.split(": " + source)[0] };
	}
}
emit(out);`, map[string]any{"names": names, "root": root, "piDir": piDir}, &want, "HOME=/home/oracle")
	for _, name := range names {
		source := filepath.Join(root, name+".jsonl")
		manager := NewSessionManagerWithDir("/work/target", filepath.Join(t.TempDir(), name))
		forked, err := manager.ForkFromFile(source)
		var have result
		if err != nil {
			have.Error = strings.Split(err.Error(), ": "+source)[0]
		} else {
			data, readErr := os.ReadFile(forked.Path())
			if readErr != nil {
				t.Fatal(readErr)
			}
			for line := range strings.SplitSeq(string(data), "\n") {
				if line != "" {
					have.Lines = append(have.Lines, line)
				}
			}
			have.Lines = have.Lines[1:]
		}
		if have.Error != want[name].Error || strings.Join(have.Lines, "\n") != strings.Join(want[name].Lines, "\n") {
			t.Errorf("%s:\n Go %+v\n Pi %+v", name, have, want[name])
		}
	}
}
