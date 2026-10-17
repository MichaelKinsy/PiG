package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

type modelsFileOp struct {
	Op       string          `json:"op"`
	Provider string          `json:"provider"`
	Entry    json.RawMessage `json:"entry"`
}

type modelsFileCase struct {
	Initial *string        `json:"initial"`
	Ops     []modelsFileOp `json:"ops"`
}

// TestFileModelsStoreNonObjectRootsAndIndexKeysMatchPi extends TestFileModelsStoreMatchesPi to roots and keys that test leaves out. It runs core/models-store.ts FileModelsStore and PiG's NewFileModelsStore over the same models-store.json files and operation sequences
// (read, write, delete): absent, empty, corrupt, byte-order-marked, null, array and scalar files, valid files whose entries are catalogs, null or not objects, and
// integer-index provider ids, which JSON.parse and JSON.stringify order first. Results (the entry read), whether an operation threw, and the file bytes after each
// operation match, with the text of each thrown error, including V8's JSON.parse SyntaxError for a corrupt file. Not compared: file modes and cross-process locking.
func TestFileModelsStoreNonObjectRootsAndIndexKeysMatchPi(t *testing.T) {
	rng := rand.New(rand.NewSource(20261121))
	pick := func(xs []string) string { return xs[rng.Intn(len(xs))] }
	providers := []string{"openai", "anthropic", "1", "0", "zz", "10", "b"}
	entries := []string{
		`{"models":[]}`, `{"models":[],"checkedAt":1700000000.5,"lastModified":5,"etag":"\"abc\""}`,
		`{"models":[{"id":"m","name":"M","api":"openai-completions","provider":"p","baseUrl":"https://x.test/v1","reasoning":false,"input":["text"],"cost":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0},"contextWindow":1000,"maxTokens":100}]}`,
		`{"models":[{"id":"future","api":"future-api","extra":"x"}],"etag":"e"}`,
	}
	// A stored value that is not a catalog (a number, string, array or an object with no models) reads back as itself in Pi, which PiG's typed entry cannot hold,
	// so a file holding one is only written and deleted, never read.
	stored := entries
	weird := false
	odd := append(append([]string{}, entries...), `null`, `5`, `"text"`, `[]`, `{}`)
	initialFile := func() *string {
		switch rng.Intn(14) {
		case 0:
			return nil
		case 1:
			return new("")
		case 2:
			return new("{}")
		case 3:
			return new(pick([]string{"{", "not json"}))
		case 4:
			return new(pick([]string{"[]", "null", "5", "true", "[null]", "[null,null]", `[{"models":[]}]`, "1.5", "-0"}))
		case 5:
			return new("\ufeff{\"openai\":" + entries[0] + "}")
		}
		stored = entries
		if weird {
			stored = odd
		}
		var members []string
		for range rng.Intn(5) {
			members = append(members, fmt.Sprintf("%q:%s", pick(providers), pick(stored)))
		}
		return new("{" + strings.Join(members, ",") + "}")
	}
	var cases []modelsFileCase
	for range 900 {
		weird = rng.Intn(3) == 0
		c := modelsFileCase{Initial: initialFile()}
		for range 1 + rng.Intn(5) {
			op := modelsFileOp{Op: pick([]string{"read", "read", "write", "write", "delete"}), Provider: pick(providers)}
			for weird && op.Op == "read" {
				op.Op = pick([]string{"write", "delete"})
			}
			if op.Op == "write" {
				op.Entry = json.RawMessage(pick(entries))
			}
			c.Ops = append(c.Ops, op)
		}
		cases = append(cases, c)
	}

	var want [][]map[string]any
	pioracle.Run(t, `
const { FileModelsStore } = await load("pi-coding-agent/core/models-store.js");
const fs = await import("node:fs");
const os = await import("node:os");
const path = await import("node:path");
const out = [];
for (const c of input) {
	const root = fs.mkdtempSync(path.join(os.tmpdir(), "modelsr-"));
	const file = path.join(root, "agent", "models-store.json");
	fs.mkdirSync(path.dirname(file), { recursive: true });
	if (c.initial !== null) fs.writeFileSync(file, c.initial);
	const store = new FileModelsStore(file);
	const rows = [];
	for (const op of c.ops) {
		const row = { op: op.op };
		try {
			if (op.op === "read") row.result = (await store.read(op.provider)) ?? null;
			else if (op.op === "write") await store.write(op.provider, op.entry);
			else await store.delete(op.provider);
		} catch (e) { row.threw = true; row.error = e.message; }
		row.file = fs.existsSync(file) ? fs.readFileSync(file, "utf8") : null;
		rows.push(JSON.parse(JSON.stringify(row)));
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
	failures := 0
	for i, c := range cases {
		root := t.TempDir()
		file := filepath.Join(root, "agent", "models-store.json")
		_ = os.MkdirAll(filepath.Dir(file), 0o755)
		if c.Initial != nil {
			_ = os.WriteFile(file, []byte(*c.Initial), 0o600)
		}
		store := NewFileModelsStore(file)
		ctx := context.Background()
		var rows []map[string]any
		for _, op := range c.Ops {
			row := map[string]any{"op": op.Op}
			var opErr error
			switch op.Op {
			case "read":
				var entry *ModelsStoreEntry
				entry, opErr = store.Read(ctx, op.Provider)
				row["result"] = nil
				if entry != nil {
					row["result"] = asAny(entry)
				}
			case "write":
				var entry ModelsStoreEntry
				if err := json.Unmarshal(op.Entry, &entry); err != nil {
					t.Fatal(err)
				}
				opErr = store.Write(ctx, op.Provider, entry)
			default:
				opErr = store.Delete(ctx, op.Provider)
			}
			if opErr != nil {
				message := opErr.Error()
				row = map[string]any{"op": op.Op, "threw": true, "error": message}
			}
			if data, err := os.ReadFile(file); err == nil {
				row["file"] = string(data)
			} else {
				row["file"] = nil
			}
			rows = append(rows, row)
		}
		got := asAny(rows)
		if !reflect.DeepEqual(got, asAny(want[i])) {
			failures++
			if failures <= 5 {
				goJSON, _ := json.Marshal(got)
				piJSON, _ := json.Marshal(want[i])
				input, _ := json.Marshal(c)
				t.Errorf("case %d %s\n Go %.1500s\n Pi %.1500s", i, input, goJSON, piJSON)
			}
		}
	}
	t.Logf("%d cases, %d failures", len(cases), failures)
}
