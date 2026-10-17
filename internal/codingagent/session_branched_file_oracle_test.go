package codingagent

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// SessionManager.createBranchedSession (session-manager.ts:1632-1730) against Session.CreateBranchedSession on seeded random session trees with labels, label removals, compactions whose firstKeptEntryId names a label, model changes and custom entries: the branched file holds the root-to-leaf path without labels (re-chained parentIds, compaction firstKeptEntryId rewritten to the entry a label stood for), then one fresh label entry per labeled target on the path in label-map order. The ids of the new label entries are random, so they are renamed L1, L2, ... in file order before comparing.
func TestCreateBranchedSessionMatchesPiOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(20261017))
	const header = `{"type":"session","version":3,"id":"%s","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/work/p"}`
	type probe struct {
		Name string `json:"name"`
		Leaf string `json:"leaf"`
	}
	root := t.TempDir()
	var probes []probe
	for n := range 80 {
		name := fmt.Sprintf("s%02d", n)
		var lines []string
		lines = append(lines, fmt.Sprintf(header, name))
		var ids []string
		var messageIDs []string
		parent := "null"
		pick := func() string {
			if len(ids) == 0 || rng.Intn(3) != 0 {
				return parent
			}
			return `"` + ids[rng.Intn(len(ids))] + `"`
		}
		count := 4 + rng.Intn(14)
		for i := range count {
			id := fmt.Sprintf("%08x", rng.Uint32())
			ts := fmt.Sprintf("2026-01-01T00:%02d:00.000Z", i)
			p := pick()
			var line string
			switch k := rng.Intn(10); {
			case k < 4:
				line = fmt.Sprintf(`{"type":"message","id":"%s","parentId":%s,"timestamp":"%s","message":{"role":"user","content":"u%d","timestamp":1}}`, id, p, ts, i)
				messageIDs = append(messageIDs, id)
			case k < 6:
				line = fmt.Sprintf(`{"type":"message","id":"%s","parentId":%s,"timestamp":"%s","message":{"role":"assistant","content":[{"type":"text","text":"a%d"}],"api":"x","provider":"p","model":"m","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":2}}`, id, p, ts, i)
				messageIDs = append(messageIDs, id)
			case k < 8 && len(ids) > 0:
				target := ids[rng.Intn(len(ids))]
				if rng.Intn(4) == 0 {
					line = fmt.Sprintf(`{"type":"label","id":"%s","parentId":%s,"timestamp":"%s","targetId":"%s"}`, id, p, ts, target)
				} else {
					label := fmt.Sprintf(`"label%d"`, rng.Intn(5))
					line = fmt.Sprintf(`{"type":"label","id":"%s","parentId":%s,"timestamp":"%s","targetId":"%s","label":%s}`, id, p, ts, target, label)
				}
			case k == 8 && len(ids) > 0:
				kept := ids[rng.Intn(len(ids))]
				if rng.Intn(5) == 0 {
					kept = id
				}
				line = fmt.Sprintf(`{"type":"compaction","id":"%s","parentId":%s,"timestamp":"%s","summary":"s%d","firstKeptEntryId":"%s","tokensBefore":%d}`, id, p, ts, i, kept, 100+i)
			default:
				line = fmt.Sprintf(`{"type":"model_change","id":"%s","parentId":%s,"timestamp":"%s","provider":"p","modelId":"m%d"}`, id, p, ts, i)
			}
			lines = append(lines, line)
			ids = append(ids, id)
			parent = `"` + id + `"`
		}
		if len(messageIDs) == 0 {
			continue
		}
		if err := os.WriteFile(filepath.Join(root, name+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, leaf := range []string{messageIDs[len(messageIDs)-1], messageIDs[rng.Intn(len(messageIDs))], ids[rng.Intn(len(ids))]} {
			probes = append(probes, probe{name, leaf})
		}
	}
	type result struct {
		Lines []string `json:"lines"`
		Error string   `json:"error"`
	}
	var want []result
	piDir := t.TempDir()
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/core/session-manager.js");
const fs = await import("node:fs");
let n = 0;
emit(input.probes.map(({ name, leaf }) => {
	const dir = input.piDir + "/" + (n++);
	fs.mkdirSync(dir, { recursive: true });
	const source = dir + "/" + name + ".jsonl";
	fs.copyFileSync(input.root + "/" + name + ".jsonl", source);
	try {
		const sm = mod.SessionManager.open(source, dir);
		const path = sm.createBranchedSession(leaf);
		const lines = path && fs.existsSync(path) ? fs.readFileSync(path, "utf8").split("\n").filter((l) => l !== "").slice(1) : sm.getEntries().map((e) => JSON.stringify(e));
		return { lines };
	} catch (e) { return { error: e.message }; }
}));`, map[string]any{"probes": probes, "root": root, "piDir": piDir}, &want, "HOME=/home/oracle")
	normalize := func(lines []string) string {
		renamed := map[string]string{}
		var out []string
		for _, line := range lines {
			var entry map[string]any
			if json.Unmarshal([]byte(line), &entry) != nil {
				out = append(out, line)
				continue
			}
			if entry["type"] == "label" {
				if id, ok := entry["id"].(string); ok {
					if _, seen := renamed[id]; !seen {
						renamed[id] = fmt.Sprintf("L%d", len(renamed)+1)
					}
				}
			}
			out = append(out, line)
		}
		joined := strings.Join(out, "\n")
		for id, name := range renamed {
			joined = strings.ReplaceAll(joined, `"`+id+`"`, `"`+name+`"`)
		}
		return joined
	}
	differing := 0
	for i, p := range probes {
		manager := NewSessionManagerWithDir("/work/p", filepath.Join(t.TempDir(), "go"))
		session, err := manager.Open(filepath.Join(root, p.Name+".jsonl"))
		var have result
		if err != nil {
			have.Error = err.Error()
		} else {
			path, branchErr := session.CreateBranchedSession(p.Leaf)
			if branchErr != nil {
				have.Error = branchErr.Error()
			} else if data, readErr := os.ReadFile(path); readErr == nil {
				for line := range strings.SplitSeq(string(data), "\n") {
					if line != "" {
						have.Lines = append(have.Lines, line)
					}
				}
				have.Lines = have.Lines[1:]
			} else {
				// A branch without a conversation is not written yet: both sides compare the in-memory entries.
				for _, entry := range session.GetEntries() {
					raw, marshalErr := json.Marshal(entry)
					if marshalErr != nil {
						t.Fatal(marshalErr)
					}
					have.Lines = append(have.Lines, string(raw))
				}
			}
		}
		if (have.Error != "") != (want[i].Error != "") || normalize(have.Lines) != normalize(want[i].Lines) {
			differing++
			if differing <= 6 {
				t.Errorf("%s leaf %s:\n Go %+v\n Pi %+v", p.Name, p.Leaf, have, want[i])
			}
		}
	}
	if differing > 0 {
		t.Errorf("%d of %d branches differ from Pi", differing, len(probes))
	}
}
