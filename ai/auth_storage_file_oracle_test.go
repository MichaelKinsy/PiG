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

type authFileOp struct {
	Op       string          `json:"op"`
	Provider string          `json:"provider"`
	Next     json.RawMessage `json:"next"`
}

type authFileCase struct {
	Initial *string      `json:"initial"`
	Ops     []authFileOp `json:"ops"`
}

// TestAuthStorageFileMatchesPi runs core/auth-storage.ts AuthStorage.create(path) and PiG's NewAuthStorage over the same auth.json files and operation
// sequences (read, list, modify that stores a credential or declines, delete): absent, empty, valid, byte-order-marked, corrupt and non-object files;
// api-key credentials whose key is a literal, a `$VAR`, a `${VAR}` reference resolved against the credential's own env, or a `!command`; OAuth and unknown
// credential types; entries that are not objects; and provider ids that are integer indexes, which Object.entries lists first. Every result (the credential
// read with its key resolved, the listed provider ids and types, the credential a modify callback saw and returned, whether an operation threw) and the auth.json
// bytes after each operation match, including a file whose JSON is null, an array or another scalar. Not compared: a string-valued file (JavaScript indexes its characters), the text of a JSON parse error (V8 wording), file modes, and cross-process locking.
func TestAuthStorageFileMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(20261119))
	pick := func(xs []string) string { return xs[rng.Intn(len(xs))] }
	providers := []string{"openai", "anthropic", "1", "0", "zz", "10", "b"}
	credentials := []string{
		`{"type":"api_key","key":"sk-literal"}`, `{"type":"api_key","key":"$PIH4_KEY"}`, `{"type":"api_key","key":"${PIH4_KEY}"}`,
		`{"type":"api_key","key":"$PIH4_UNSET"}`, `{"type":"api_key","key":"$PIH4_KEY","env":{"PIH4_KEY":"from-credential-env"}}`,
		`{"type":"api_key","key":"!echo from-command"}`, `{"type":"api_key","key":"!exit 1"}`, `{"type":"api_key","key":""}`, `{"type":"api_key"}`,
		`{"type":"api_key","key":"x","env":{"A":"1"},"extra":true}`,
		`{"type":"oauth","access":"a","refresh":"r","expires":123}`, `{"type":"oauth","access":"a","refresh":"r","expires":1760000000000.0}`, `{"type":"oauth","access":"a","refresh":"r","expires":1.76e12,"projectId":"p"}`, `{"type":"oauth","access":"a","refresh":"r","expires":1.5,"projectId":"p"}`,
		`{"type":"oauth","refresh":"r","access":"a","expires":0,"scope":"s","unknownField":{"x":[1,2]}}`,
		`{"type":"mystery","data":1}`, `5`, `"text"`, `null`, `[]`,
	}
	initialFile := func() *string {
		switch rng.Intn(18) {
		case 0:
			return nil
		case 1:
			return new("")
		case 2:
			return new("{}")
		case 3:
			return new("{")
		case 4:
			return new("[]")
		case 5:
			return new("null")
		case 7:
			return new(pick([]string{"5", "true", "0", "1.5", " 7 "}))
		case 8:
			return new(pick([]string{`[{"type":"api_key","key":"k"},null,5]`, `[{"type":"oauth","access":"a","refresh":"r","expires":1}]`, `[[1],"x"]`, `[null]`}))
		case 6:
			return new("\ufeff{\"openai\":{\"type\":\"api_key\",\"key\":\"bom\"}}")
		}
		var members []string
		for range rng.Intn(5) {
			members = append(members, fmt.Sprintf("%q:%s", pick(providers), pick(credentials)))
		}
		sep := ","
		if rng.Intn(2) == 0 {
			sep = ",\n  "
		}
		return new("{" + strings.Join(members, sep) + "}")
	}
	var cases []authFileCase
	for range 600 {
		c := authFileCase{Initial: initialFile()}
		for range 1 + rng.Intn(5) {
			op := authFileOp{Op: pick([]string{"read", "read", "list", "modify", "modify", "modify", "delete"}), Provider: pick(providers)}
			if op.Op == "modify" {
				if rng.Intn(5) != 0 {
					op.Next = json.RawMessage(pick(credentials[:13]))
				} else {
					op.Next = json.RawMessage("null")
				}
			}
			c.Ops = append(c.Ops, op)
		}
		cases = append(cases, c)
	}

	t.Setenv("PIH4_KEY", "env-value")
	_ = os.Unsetenv("PIH4_UNSET")
	var want [][]map[string]any
	pioracle.Run(t, piCommandsWithoutTimeout+`
const { AuthStorage } = await load("pi-coding-agent/core/auth-storage.js");
const fs = await import("node:fs");
const os = await import("node:os");
const path = await import("node:path");
const out = [];
for (const c of input) {
	const root = fs.mkdtempSync(path.join(os.tmpdir(), "authr-"));
	const file = path.join(root, "agent", "auth.json");
	fs.mkdirSync(path.dirname(file), { recursive: true });
	if (c.initial !== null) fs.writeFileSync(file, c.initial);
	const store = AuthStorage.create(file);
	const rows = [];
	for (const op of c.ops) {
		const row = { op: op.op };
		try {
			if (op.op === "read") row.result = (await store.read(op.provider)) ?? null;
			else if (op.op === "list") row.result = await store.list();
			else if (op.op === "delete") { await store.delete(op.provider); row.result = null; }
			else {
				row.result = (await store.modify(op.provider, async (current) => {
					row.seen = current ?? null;
					return op.next === null ? undefined : op.next;
				})) ?? null;
			}
		} catch (e) { row.threw = true; }
		row.file = fs.existsSync(file) ? fs.readFileSync(file, "utf8") : null;
		rows.push(JSON.parse(JSON.stringify(row)));
	}
	out.push(rows);
	fs.rmSync(root, { recursive: true, force: true });
}
emit(out);`, cases, &want, "PIH4_KEY=env-value")

	asAny := func(v any) any {
		data, _ := json.Marshal(v)
		var out any
		_ = json.Unmarshal(data, &out)
		return out
	}
	failures := 0
	for i, c := range cases {
		root := t.TempDir()
		file := filepath.Join(root, "agent", "auth.json")
		_ = os.MkdirAll(filepath.Dir(file), 0o755)
		if c.Initial != nil {
			_ = os.WriteFile(file, []byte(*c.Initial), 0o600)
		}
		store, err := NewAuthStorage(file)
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		var rows []map[string]any
		for _, op := range c.Ops {
			row := map[string]any{"op": op.Op}
			var opErr error
			switch op.Op {
			case "read":
				var credential *Credential
				credential, opErr = store.Read(ctx, op.Provider)
				row["result"] = nil
				if credential != nil {
					row["result"] = asAny(credential)
				}
			case "list":
				var infos []CredentialInfo
				infos, opErr = store.List(ctx)
				list := []any{}
				for _, info := range infos {
					entry := map[string]any{"providerId": info.ProviderID}
					if info.Type != "" { // an entry with no string type reads as undefined, which JSON.stringify omits
						entry["type"] = string(info.Type)
					}
					list = append(list, entry)
				}
				row["result"] = list
			case "delete":
				opErr = store.Delete(ctx, op.Provider)
				row["result"] = nil
			case "modify":
				var returned *Credential
				returned, opErr = store.Modify(ctx, op.Provider, func(current *Credential) (*Credential, error) {
					row["seen"] = nil
					if current != nil {
						row["seen"] = asAny(current)
					}
					if string(op.Next) == "null" {
						return nil, nil
					}
					var next Credential
					if err := json.Unmarshal(op.Next, &next); err != nil {
						return nil, err
					}
					return &next, nil
				})
				row["result"] = nil
				if returned != nil {
					row["result"] = asAny(returned)
				}
			}
			if opErr != nil {
				row = map[string]any{"op": op.Op, "threw": true}
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
				t.Errorf("case %d %s\n Go %.1800s\n Pi %.1800s", i, input, goJSON, piJSON)
			}
		}
	}
	t.Logf("%d cases, %d failures", len(cases), failures)
}
