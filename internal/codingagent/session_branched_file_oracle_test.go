package codingagent

import (
	"encoding/base64"
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
	rng := rand.New(rand.NewSource(20261102))
	const header = `{"type":"session","version":3,"id":"%s","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/work/p"}`
	root := t.TempDir()
	var probes []branchProbe
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
			switch k := rng.Intn(18); {
			case k < 3:
				line = fmt.Sprintf(`{"type":"message","id":"%s","parentId":%s,"timestamp":"%s","message":{"role":"user","content":"u%d","timestamp":1}}`, id, p, ts, i)
				messageIDs = append(messageIDs, id)
			case k < 5:
				line = fmt.Sprintf(`{"type":"message","id":"%s","parentId":%s,"timestamp":"%s","message":{"role":"assistant","content":[{"type":"text","text":"a%d"}],"api":"x","provider":"p","model":"m","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":2}}`, id, p, ts, i)
				messageIDs = append(messageIDs, id)
			case k == 5:
				line = fmt.Sprintf(`{"type":"message","id":"%s","parentId":%s,"timestamp":"%s","message":{"role":"user","content":[{"type":"text","text":"look %d"},{"type":"image","data":"%s","mimeType":"image/png"}],"timestamp":3}}`, id, p, ts, i, fixtureImage(rng))
				messageIDs = append(messageIDs, id)
			case k == 6:
				line = fmt.Sprintf(`{"type":"message","id":"%s","parentId":%s,"timestamp":"%s","message":{"role":"toolResult","toolCallId":"c%d","toolName":"read","content":[{"type":"image","data":"%s","mimeType":"image/jpeg"}],"isError":false,"timestamp":4}}`, id, p, ts, i, fixtureImage(rng))
				messageIDs = append(messageIDs, id)
			case k == 7:
				// A record that is valid JSON but not JSON.stringify's text: Pi's parse and stringify canonicalize it.
				line = fmt.Sprintf(`{ "type" : "message", "id":"%s", "parentId":%s, "timestamp":"%s", "message":{"role":"user","content":"caf\u00e9 \/ x\u2028 %d","timestamp":1.0}}`, id, p, ts, i)
				messageIDs = append(messageIDs, id)
			case k < 11 && len(ids) > 0:
				target := ids[rng.Intn(len(ids))]
				if rng.Intn(4) == 0 {
					line = fmt.Sprintf(`{"type":"label","id":"%s","parentId":%s,"timestamp":"%s","targetId":"%s"}`, id, p, ts, target)
				} else {
					label := fmt.Sprintf(`"label%d"`, rng.Intn(5))
					line = fmt.Sprintf(`{"type":"label","id":"%s","parentId":%s,"timestamp":"%s","targetId":"%s","label":%s}`, id, p, ts, target, label)
				}
			case k == 11 && len(ids) > 0:
				kept := ids[rng.Intn(len(ids))]
				if rng.Intn(5) == 0 {
					kept = id
				}
				line = fmt.Sprintf(`{"type":"compaction","id":"%s","parentId":%s,"timestamp":"%s","summary":"s%d","firstKeptEntryId":"%s","tokensBefore":%d}`, id, p, ts, i, kept, 100+i)
			case k == 12:
				line = fmt.Sprintf(`{"type":"custom","id":"%s","parentId":%s,"timestamp":"%s","customType":"ext","data":{"n":%d,"nested":{"a":[1,2,{"b":null}]}}}`, id, p, ts, i)
			case k == 13:
				line = fmt.Sprintf(`{"type":"custom_message","id":"%s","parentId":%s,"timestamp":"%s","customType":"ext","content":"note %d","display":true,"details":{"k":"v"}}`, id, p, ts, i)
			case k == 14:
				line = fmt.Sprintf(`{"type":"session_info","id":"%s","parentId":%s,"timestamp":"%s","name":"named %d"}`, id, p, ts, i)
			case k == 15:
				line = fmt.Sprintf(`{"type":"branch_summary","id":"%s","parentId":%s,"timestamp":"%s","fromId":%s,"summary":"summary %d"}`, id, p, ts, p, i)
			case k == 16:
				line = fmt.Sprintf(`{"type":"thinking_level_change","id":"%s","parentId":%s,"timestamp":"%s","thinkingLevel":"high"}`, id, p, ts)
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
			probes = append(probes, branchProbe{name, leaf})
		}
	}
	assertBranchesMatchPi(t, root, probes, []string{`"type":"image"`, `"type":"custom"`, `"type":"custom_message"`, `"type":"session_info"`, `"type":"branch_summary"`, `"type":"thinking_level_change"`, `"type":"compaction"`, `"type":"label"`, "café"})
}

// branchProbe is one createBranchedSession call: the session file named Name under the probes' root, branched at Leaf.
type branchProbe struct {
	Name string `json:"name"`
	Leaf string `json:"leaf"`
}

// assertBranchesMatchPi branches each probe's session with Pi's SessionManager.createBranchedSession and with Session.CreateBranchedSession and compares the branched files'
// records after their headers, with the ids of the new label entries renamed L1, L2, ... in file order. Every shape must appear in some record of Pi's output.
func assertBranchesMatchPi(t *testing.T, root string, probes []branchProbe, shapes []string) {
	t.Helper()
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
	// The probes must reach every record shape the rewrite has to keep, or a match proves nothing about it.
	for _, shape := range shapes {
		reached := 0
		for _, result := range want {
			for _, line := range result.Lines {
				if strings.Contains(line, shape) {
					reached++
				}
			}
		}
		if reached == 0 {
			t.Errorf("no probe branch of Pi's output holds %s", shape)
		}
	}
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

// fixtureImage is a base64 payload of a few KiB, the shape of an attached screenshot.
func fixtureImage(rng *rand.Rand) string {
	raw := make([]byte, 1500+rng.Intn(2500))
	rng.Read(raw)
	return base64.StdEncoding.EncodeToString(raw)
}

// createBranchedSession removes the label entries from the path and rechains it, and a compaction's firstKeptEntryId that names a removed label becomes the entry that follows the label on the path (session-manager.ts:1655-1685). The sessions here put that rule through each of its cases: a label before the entries it precedes, a label directly before the compaction that keeps it (the compaction itself), two labels in a row, a compaction that keeps itself, one that keeps an entry that is not a label in a path with labels, one that keeps a label off the path, one that keeps an id nothing names, and two compactions that each keep a label. Each is branched at the compaction and at the entry before it, and the files are Pi's.
func TestCreateBranchedSessionCompactionBoundariesMatchPiOracle(t *testing.T) {
	message := func(id, parent, role string) string {
		p := "null"
		if parent != "" {
			p = `"` + parent + `"`
		}
		return fmt.Sprintf(`{"type":"message","id":"%s","parentId":%s,"timestamp":"2026-01-01T00:00:01.000Z","message":{"role":"%s","content":"text of %s","timestamp":1}}`, id, p, role, id)
	}
	label := func(id, parent, target, text string) string {
		return fmt.Sprintf(`{"type":"label","id":"%s","parentId":"%s","timestamp":"2026-01-01T00:00:02.000Z","targetId":"%s","label":"%s"}`, id, parent, target, text)
	}
	compaction := func(id, parent, kept string) string {
		return fmt.Sprintf(`{"type":"compaction","id":"%s","parentId":"%s","timestamp":"2026-01-01T00:00:03.000Z","summary":"summary %s","firstKeptEntryId":"%s","tokensBefore":100}`, id, parent, id, kept)
	}
	const header = `{"type":"session","version":3,"id":"%s","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/work/p"}`
	sessions := []struct {
		name   string
		leaves []string
		lines  []string
	}{
		{"label-before-entries", []string{"c1", "m3"}, []string{
			message("m1", "", "user"), message("m2", "m1", "assistant"), label("l1", "m2", "m1", "start"), message("m3", "l1", "user"), compaction("c1", "m3", "l1")}},
		{"label-directly-before-compaction", []string{"c1", "l1"}, []string{
			message("m1", "", "user"), message("m2", "m1", "assistant"), label("l1", "m2", "m1", "start"), compaction("c1", "l1", "l1")}},
		{"two-labels-in-a-row", []string{"c1", "l2"}, []string{
			message("m1", "", "user"), message("m2", "m1", "assistant"), label("l1", "m2", "m2", "a"), label("l2", "l1", "m1", "b"), compaction("c1", "l2", "l1")}},
		{"compaction-keeps-itself", []string{"c1"}, []string{
			message("m1", "", "user"), message("m2", "m1", "assistant"), compaction("c1", "m2", "c1")}},
		{"kept-entry-is-not-a-label", []string{"c1", "m3"}, []string{
			message("m1", "", "user"), message("m2", "m1", "assistant"), label("l1", "m2", "m2", "a"), message("m3", "l1", "user"), compaction("c1", "m3", "m2")}},
		{"kept-label-is-off-the-path", []string{"c1"}, []string{
			message("m1", "", "user"), message("m2", "m1", "assistant"), label("lx", "m1", "m1", "side"), message("m3", "m2", "user"), compaction("c1", "m3", "lx")}},
		{"kept-id-names-nothing", []string{"c1"}, []string{
			message("m1", "", "user"), message("m2", "m1", "assistant"), label("l1", "m2", "m1", "a"), message("m3", "l1", "user"), compaction("c1", "m3", "zzzz")}},
		{"two-compactions", []string{"c2", "m4", "c1"}, []string{
			message("m1", "", "user"), message("m2", "m1", "assistant"), label("l1", "m2", "m1", "a"), message("m3", "l1", "user"), compaction("c1", "m3", "l1"),
			label("l2", "c1", "m3", "b"), message("m4", "l2", "assistant"), compaction("c2", "m4", "l2")}},
	}
	root := t.TempDir()
	var probes []branchProbe
	for _, session := range sessions {
		lines := append([]string{fmt.Sprintf(header, session.name)}, session.lines...)
		if err := os.WriteFile(filepath.Join(root, session.name+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, leaf := range session.leaves {
			probes = append(probes, branchProbe{session.name, leaf})
		}
	}
	assertBranchesMatchPi(t, root, probes, []string{`"type":"compaction"`, `"type":"label"`, `"firstKeptEntryId":"m3"`, `"firstKeptEntryId":"c1"`, `"firstKeptEntryId":"zzzz"`, `"firstKeptEntryId":"lx"`})
}
