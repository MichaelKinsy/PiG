package codingagent

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type trustMultiUpdate struct {
	Path     string `json:"path"`
	Decision *bool  `json:"decision"`
}

type trustMultiOp struct {
	Op      string             `json:"op"`
	Cwd     string             `json:"cwd"`
	Updates []trustMultiUpdate `json:"updates"`
}

type trustMultiCase struct {
	Initial *string        `json:"initial"`
	Ops     []trustMultiOp `json:"ops"`
}

// TestProjectTrustStoreMultiEntryFilesMatchPi extends TestProjectTrustStoreMatchesPi to files with several invalid values (the error names the first in Object.entries order), a symlinked cwd, astral and BMP-high keys (JSON.stringify of sorted keys orders by UTF-16 code unit) and root-relative ancestors. It runs core/trust-manager.ts ProjectTrustStore and PiG's NewProjectTrustStore over the same trust.json files and operation
// sequences (get, getEntry, set, setMany). Files are absent, empty, corrupt, byte-order-marked, non-object roots, objects whose values are true, false, null or
// not booleans, with keys that are the case's directories, a symlink to one, missing paths, integer-like and non-ASCII keys. Results, whether an operation
// threw, the text of an "Invalid trust store" error and the file bytes after each operation match. Not compared: the V8 text after "Failed to read trust store
// <path>: ", file modes and cross-process locking.
func TestProjectTrustStoreMultiEntryFilesMatchPi(t *testing.T) {
	rng := rand.New(rand.NewSource(20261122))
	pick := func(xs []string) string { return xs[rng.Intn(len(xs))] }
	paths := []string{"{R}", "{R}/a", "{R}/a/b", "{R}/a/b/c", "{R}/link", "{R}/link/c", "{R}/missing/x", "{R}/a/../a/b", "{R}/a/./b/", "/", "{R}/\U0001F600", "{R}/\uFF5E"}
	keys := append(append([]string{}, paths[:7]...), "{R}/\U0001F600", "{R}/\uFF5E", "/", "0", "10", "é", "{R}/a/b/c/d", "{R}/Z")
	values := []string{"true", "false", "null"}
	badValues := []string{"1", `"yes"`, "{}", "[]", "0"}
	initialFile := func() *string {
		switch rng.Intn(14) {
		case 0:
			return nil
		case 1:
			return new("")
		case 2:
			return new("{}")
		case 3:
			return new(pick([]string{"{", "not json", `{"a":`}))
		case 4:
			return new(pick([]string{"[]", "null", "5", "true", `"s"`, `[{"{R}":true}]`}))
		case 5:
			return new("\ufeff{\"{R}/a\":true}")
		}
		var members []string
		for range rng.Intn(6) {
			value := pick(values)
			if rng.Intn(12) == 0 {
				value = pick(badValues)
			}
			members = append(members, fmt.Sprintf("%q:%s", pick(keys), value))
		}
		return new("{" + strings.Join(members, ",") + "}")
	}
	decision := func() *bool {
		switch rng.Intn(3) {
		case 0:
			return nil
		case 1:
			return new(true)
		}
		return new(false)
	}
	var cases []trustMultiCase
	for range 1200 {
		c := trustMultiCase{Initial: initialFile()}
		for range 1 + rng.Intn(5) {
			op := trustMultiOp{Op: pick([]string{"get", "getEntry", "set", "setMany", "get", "getEntry"}), Cwd: pick(paths)}
			switch op.Op {
			case "setMany":
				for range 1 + rng.Intn(3) {
					op.Updates = append(op.Updates, trustMultiUpdate{Path: pick(paths), Decision: decision()})
				}
			case "set":
				op.Updates = []trustMultiUpdate{{Decision: decision()}}
			}
			c.Ops = append(c.Ops, op)
		}
		cases = append(cases, c)
	}

	var want [][]map[string]any
	pioracle.Run(t, `
const { ProjectTrustStore } = await load("pi-coding-agent/core/trust-manager.js");
const fs = await import("node:fs");
const os = await import("node:os");
const path = await import("node:path");
const out = [];
for (const c of input) {
	const root = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), "trustr-")));
	fs.mkdirSync(path.join(root, "a", "b", "c"), { recursive: true });
	fs.symlinkSync(path.join(root, "a", "b"), path.join(root, "link"));
	const agentDir = path.join(root, "agent");
	const file = path.join(agentDir, "trust.json");
	const sub = (s) => s.split("{R}").join(root);
	const escapedRoot = root.split("\\").join("\\\\");
	const subJSON = (s) => s.split("{R}").join(escapedRoot);
	const unsubValue = (v) => typeof v === "string" ? v.split(escapedRoot).join("{R}").split(root).join("{R}")
		: Array.isArray(v) ? v.map(unsubValue)
		: v !== null && typeof v === "object" ? Object.fromEntries(Object.entries(v).map(([k, x]) => [k, unsubValue(x)])) : v;
	if (c.initial !== null) { fs.mkdirSync(agentDir, { recursive: true }); fs.writeFileSync(file, subJSON(c.initial)); }
	const store = new ProjectTrustStore(agentDir);
	const rows = [];
	for (const op of c.ops) {
		const row = { op: op.op };
		try {
			if (op.op === "get") row.result = store.get(sub(op.cwd));
			else if (op.op === "getEntry") row.result = store.getEntry(sub(op.cwd));
			else if (op.op === "set") store.set(sub(op.cwd), op.updates[0].decision);
			else store.setMany(op.updates.map((u) => ({ path: sub(u.path), decision: u.decision })));
		} catch (e) { row.threw = true; row.error = e.message; }
		rows.push(unsubValue(JSON.parse(JSON.stringify({ ...row, file: fs.existsSync(file) ? fs.readFileSync(file, "utf8") : null }))));
	}
	out.push(rows);
	fs.rmSync(root, { recursive: true, force: true });
}
emit(out);`, cases, &want)

	asAny := func(v any) any {
		data, _ := json.Marshal(v)
		var out any
		_ = json.Unmarshal(data, &out)
		return out
	}
	// A parse error's tail is V8's; keep the "Failed to read trust store <path>:" head.
	clip := func(message string) string {
		if head, _, ok := strings.Cut(message, "Failed to read trust store "); ok {
			path, _, _ := strings.Cut(strings.TrimPrefix(message, head+"Failed to read trust store "), ": ")
			return head + "Failed to read trust store " + path
		}
		return message
	}
	failures := 0
	for i, c := range cases {
		root, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(root, "a", "b", "c"), 0o755); err != nil {
			t.Fatal(err)
		}
		testenv.RequireDirectoryLink(t, filepath.Join(root, "a", "b"), filepath.Join(root, "link"))
		agentDir := filepath.Join(root, "agent")
		file := filepath.Join(agentDir, "trust.json")
		sub := func(s string) string { return strings.ReplaceAll(s, "{R}", root) }
		escapedRoot := strings.ReplaceAll(root, `\`, `\\`)
		subJSON := func(s string) string { return strings.ReplaceAll(s, "{R}", escapedRoot) }
		var unsubValue func(v any) any
		unsubValue = func(v any) any {
			switch v := v.(type) {
			case string:
				return strings.ReplaceAll(strings.ReplaceAll(v, escapedRoot, "{R}"), root, "{R}")
			case []any:
				for i := range v {
					v[i] = unsubValue(v[i])
				}
			case map[string]any:
				for k := range v {
					v[k] = unsubValue(v[k])
				}
			}
			return v
		}
		if c.Initial != nil {
			_ = os.MkdirAll(agentDir, 0o755)
			_ = os.WriteFile(file, []byte(subJSON(*c.Initial)), 0o644)
		}
		store := NewProjectTrustStore(agentDir)
		var rows []map[string]any
		for _, op := range c.Ops {
			row := map[string]any{"op": op.Op}
			var opErr error
			switch op.Op {
			case "get":
				var d ProjectTrustDecision
				d, opErr = store.Get(sub(op.Cwd))
				row["result"] = nil
				if d != nil {
					row["result"] = *d
				}
			case "getEntry":
				var entry *ProjectTrustStoreEntry
				entry, opErr = store.GetEntry(sub(op.Cwd))
				row["result"] = nil
				if entry != nil {
					row["result"] = map[string]any{"path": entry.Path, "decision": entry.Decision}
				}
			case "set":
				opErr = store.Set(sub(op.Cwd), op.Updates[0].Decision)
			default:
				var updates []ProjectTrustUpdate
				for _, u := range op.Updates {
					updates = append(updates, ProjectTrustUpdate{Path: sub(u.Path), Decision: u.Decision})
				}
				opErr = store.SetMany(updates)
			}
			if opErr != nil {
				row = map[string]any{"op": op.Op, "threw": true, "error": clip(opErr.Error())}
			}
			if data, err := os.ReadFile(file); err == nil {
				row["file"] = string(data)
			} else {
				row["file"] = nil
			}
			rows = append(rows, row)
		}
		got := unsubValue(asAny(rows)) // the root appears backslash-escaped inside trust.json text on Windows, raw elsewhere
		wantRows := want[i]
		for _, w := range wantRows {
			if message, ok := w["error"].(string); ok {
				w["error"] = clip(message)
			}
		}
		if !reflect.DeepEqual(got, asAny(wantRows)) {
			failures++
			if failures <= 5 {
				goJSON, _ := json.Marshal(got)
				piJSON, _ := json.Marshal(wantRows)
				input, _ := json.Marshal(c)
				t.Errorf("case %d %s\n Go %.1500s\n Pi %.1500s", i, input, goJSON, piJSON)
			}
		}
	}
	t.Logf("%d cases, %d failures", len(cases), failures)
}
