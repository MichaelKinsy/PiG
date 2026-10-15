package codingagent

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// pi: packages/coding-agent/src/core/session-export.ts

// serializeSessionBranch over seeded session files whose entries carry unknown members, members in odd orders (parentId first or last), integer-like member names,
// duplicate members, unicode escapes and numbers in several spellings, for the default leaf and for a branch from a random entry: the header, then each entry of the
// branch as JSON.stringify writes {...entry, parentId} with the chained parent, line by line. The header's timestamp is the export time, so it is compared apart.
func TestSerializeSessionBranchMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(257))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	root := t.TempDir()
	type probe struct {
		Name string `json:"name"`
		Leaf string `json:"leaf"`
	}
	var probes []probe
	for n := range 60 {
		name := fmt.Sprintf("s%02d", n)
		lines := []string{fmt.Sprintf(`{"type":"session","version":3,"id":"%s","timestamp":"2026-01-01T00:00:00.000Z","cwd":%s}`, name, pick(`"/work/p"`, `"/w\u00e9/\ud83d\ude00"`, `"/a/\u2028b"`))}
		var ids []string
		parent := "null"
		for i := range 4 + rng.Intn(10) {
			id := fmt.Sprintf("%08x", rng.Uint32())
			p := parent
			if len(ids) > 0 && rng.Intn(4) == 0 {
				p = `"` + ids[rng.Intn(len(ids))] + `"`
			}
			ts := fmt.Sprintf("2026-01-01T00:%02d:00.000Z", i)
			content := pick(`"plain"`, `"line\nbreak"`, `"\u00e9\ud83d\ude00"`, `"<&>"`, `"\u2028\u2029"`, `"tab\t"`, `"\u007f"`, `""`)
			head := fmt.Sprintf(`"type":"message","id":"%s","parentId":%s,"timestamp":"%s"`, id, p, ts)
			message := fmt.Sprintf(`"message":{"role":"user","content":%s,"timestamp":%s}`, content, pick("1", "1.0", "1e2", "1.5", "0.1", "-0", "100000000000000000000", "1E+2"))
			members := []string{head, message}
			switch rng.Intn(7) {
			case 0:
				members = []string{`"parentId":` + p, `"type":"message"`, `"id":"` + id + `"`, `"timestamp":"` + ts + `"`, message}
			case 1:
				members = append(members, `"extra":`+pick(`{"b":1,"a":2}`, `[1,2.50,"x"]`, `null`, `true`, `1e400`, `"\u00e9"`))
			case 2:
				members = append(members, `"7":"seven"`, `"extra":1`, `"2":"two"`)
			case 3:
				members = append(members, `"parentId":"override"`)
			case 4:
				members = []string{`"type":"message"`, `"id":"` + id + `"`, `"timestamp":"` + ts + `"`, message}
			}
			lines = append(lines, "{"+strings.Join(members, pick(",", ", ", ",\n"))+"}")
			ids = append(ids, id)
			parent = `"` + id + `"`
		}
		if err := os.WriteFile(filepath.Join(root, name+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		probes = append(probes, probe{name, ""}, probe{name, ids[rng.Intn(len(ids))]})
	}
	type result struct {
		Lines []string `json:"lines"`
		Error string   `json:"error"`
	}
	var want []result
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/core/session-manager.js");
const exp = await load("pi-coding-agent/core/session-export.js");
const fs = await import("node:fs");
let n = 0;
emit(input.probes.map(({ name, leaf }) => {
	const dir = input.piDir + "/" + (n++);
	fs.mkdirSync(dir, { recursive: true });
	const source = dir + "/" + name + ".jsonl";
	fs.copyFileSync(input.root + "/" + name + ".jsonl", source);
	try {
		const sm = mod.SessionManager.open(source, dir);
		if (leaf) sm.branch(leaf);
		return { lines: exp.serializeSessionBranch(sm).split("\n").filter((line) => line !== "") };
	} catch (e) { return { error: e.message }; }
}));`, map[string]any{"probes": probes, "root": root, "piDir": t.TempDir()}, &want, "HOME=/home/oracle")
	failures := 0
	for i, p := range probes {
		manager := NewSessionManagerWithDir("/work/p", filepath.Join(t.TempDir(), "go"))
		session, err := manager.Open(filepath.Join(root, p.Name+".jsonl"))
		var got result
		if err != nil {
			got.Error = err.Error()
		} else {
			if p.Leaf != "" {
				if err := session.Branch(p.Leaf); err != nil {
					got.Error = err.Error()
				}
			}
			if got.Error == "" {
				text, err := SerializeSessionBranch(session.ExportHeader(), session.GetBranch(), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), nil)
				if err != nil {
					got.Error = err.Error()
				}
				for line := range strings.SplitSeq(text, "\n") {
					if line != "" {
						got.Lines = append(got.Lines, line)
					}
				}
			}
		}
		if got.Error != "" || want[i].Error != "" {
			if got.Error != want[i].Error {
				if failures++; failures <= 8 {
					t.Errorf("%s leaf %q: PiG error %q, Pi error %q", p.Name, p.Leaf, got.Error, want[i].Error)
				}
			}
			continue
		}
		// The header line carries the export time.
		if len(got.Lines) != len(want[i].Lines) {
			failures++
			t.Errorf("%s leaf %q: PiG %d lines, Pi %d", p.Name, p.Leaf, len(got.Lines), len(want[i].Lines))
			continue
		}
		for k := 1; k < len(got.Lines); k++ {
			if got.Lines[k] != want[i].Lines[k] {
				if failures++; failures <= 8 {
					t.Errorf("%s leaf %q line %d:\nPiG %s\nPi  %s", p.Name, p.Leaf, k, got.Lines[k], want[i].Lines[k])
				}
				break
			}
		}
		stripTime := func(line string) string {
			if k := strings.Index(line, `"timestamp":"`); k >= 0 {
				if end := strings.Index(line[k+13:], `"`); end >= 0 {
					return line[:k+13] + line[k+13+end:]
				}
			}
			return line
		}
		if len(got.Lines) > 0 && stripTime(got.Lines[0]) != stripTime(want[i].Lines[0]) {
			if failures++; failures <= 8 {
				t.Errorf("%s header:\nPiG %s\nPi  %s", p.Name, got.Lines[0], want[i].Lines[0])
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d probes differ", failures, len(probes))
	}
}
