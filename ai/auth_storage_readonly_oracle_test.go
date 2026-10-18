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

type readOnlyAuthOp struct {
	Op       string  `json:"op"`
	Provider string  `json:"provider"`
	Content  *string `json:"content"`
}

type readOnlyAuthCase struct {
	Initial *string          `json:"initial"`
	Ops     []readOnlyAuthOp `json:"ops"`
}

// TestReadOnlyAuthStorageMatchesPi runs core/auth-storage.ts ReadOnlyAuthStorage and PiG's NewReadOnlyAuthStorage over the same auth.json files: absent,
// empty, corrupt, byte-order-marked, non-object and valid files whose entries are valid or invalid credentials (an API key that is not a string, an env map
// with a non-string value, an OAuth credential missing a field or with a non-finite expiry, an entry that is null, an array or a scalar). Operations are read
// (a `!command` key is not resolved, other keys resolve against the credential's env), list, modify and delete (always refused), and a rewrite of the file between
// operations, which a loaded store does not see. Results and thrown messages match, including V8's JSON.parse SyntaxError text after "Failed to read auth.json: ".
func TestReadOnlyAuthStorageMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(20261120))
	pick := func(xs []string) string { return xs[rng.Intn(len(xs))] }
	providers := []string{"openai", "anthropic", "1", "0", "zz", "b"}
	credentials := []string{
		`{"type":"api_key","key":"sk-literal"}`, `{"type":"api_key","key":"$PIH4_KEY"}`, `{"type":"api_key","key":"${PIH4_KEY}"}`, `{"type":"api_key","key":"$PIH4_UNSET"}`,
		`{"type":"api_key","key":"$PIH4_KEY","env":{"PIH4_KEY":"from-credential-env"}}`, `{"type":"api_key","key":"!echo from-command"}`, `{"type":"api_key","key":""}`, `{"type":"api_key"}`,
		`{"type":"api_key","key":5}`, `{"type":"api_key","key":null}`, `{"type":"api_key","key":"k","env":{"A":1}}`, `{"type":"api_key","key":"k","env":[]}`, `{"type":"api_key","key":"k","env":null}`, `{"type":"api_key","key":"k","env":{}}`,
		`{"type":"oauth","access":"a","refresh":"r","expires":123}`, `{"type":"oauth","access":"a","refresh":"r","expires":1.5,"projectId":"p"}`, `{"type":"oauth","access":"a","refresh":"r"}`, `{"type":"oauth","access":"a","expires":1}`, `{"type":"oauth","refresh":"r","expires":1}`,
		`{"type":"oauth","access":"a","refresh":"r","expires":"1"}`, `{"type":"oauth","access":1,"refresh":"r","expires":1}`, `{"type":"oauth","access":"a","refresh":"r","expires":1e999}`,
		`{"type":"mystery","data":1}`, `{"data":1}`, `{}`, `5`, `"text"`, `null`, `[]`, `true`,
	}
	// The literal, reference and command API keys, the empty env map and the two OAuth credentials are valid; half the files hold only those so that a read reaches them.
	valid := append(append([]string{}, credentials[:8]...), credentials[13:16]...)
	validOnly := false
	initialFile := func() *string {
		if validOnly {
			var members []string
			for range 1 + rng.Intn(4) {
				members = append(members, fmt.Sprintf("%q:%s", pick(providers), pick(valid)))
			}
			return new("{" + strings.Join(members, ",") + "}")
		}
		switch rng.Intn(14) {
		case 0:
			return nil
		case 1:
			return new("")
		case 2:
			return new("{}")
		case 3:
			return new(pick([]string{"{", "not json", "{\"a\":"}))
		case 4:
			return new(pick([]string{"[]", "null", "5", "\"x\"", "true", "[{\"type\":\"api_key\"}]"}))
		case 5:
			return new("\ufeff{\"openai\":{\"type\":\"api_key\",\"key\":\"bom\"}}")
		}
		var members []string
		for range 1 + rng.Intn(4) {
			members = append(members, fmt.Sprintf("%q:%s", pick(providers), pick(credentials)))
		}
		return new("{" + strings.Join(members, ",") + "}")
	}
	var cases []readOnlyAuthCase
	for range 800 {
		validOnly = rng.Intn(2) == 0
		c := readOnlyAuthCase{Initial: initialFile()}
		for range 1 + rng.Intn(5) {
			op := readOnlyAuthOp{Op: pick([]string{"read", "read", "read", "list", "list", "modify", "delete", "rewrite"}), Provider: pick(providers)}
			if op.Op == "rewrite" {
				op.Content = new(`{"openai":{"type":"api_key","key":"rewritten"},"extra":{"type":"oauth","access":"a","refresh":"r","expires":1}}`)
			}
			c.Ops = append(c.Ops, op)
		}
		cases = append(cases, c)
	}

	t.Setenv("PIH4_KEY", "env-value")
	_ = os.Unsetenv("PIH4_UNSET")
	var want [][]map[string]any
	pioracle.Run(t, piCommandsWithoutTimeout+`
const { ReadOnlyAuthStorage } = await load("pi-coding-agent/core/auth-storage.js");
const fs = await import("node:fs");
const os = await import("node:os");
const path = await import("node:path");
const out = [];
for (const c of input) {
	const root = fs.mkdtempSync(path.join(os.tmpdir(), "authro-"));
	const file = path.join(root, "agent", "auth.json");
	fs.mkdirSync(path.dirname(file), { recursive: true });
	if (c.initial !== null) fs.writeFileSync(file, c.initial);
	const store = new ReadOnlyAuthStorage(file);
	const rows = [];
	for (const op of c.ops) {
		const row = { op: op.op };
		try {
			if (op.op === "read") row.result = (await store.read(op.provider)) ?? null;
			else if (op.op === "list") row.result = await store.list();
			else if (op.op === "delete") await store.delete(op.provider);
			else if (op.op === "modify") await store.modify(op.provider, async () => undefined);
			else fs.writeFileSync(file, op.content);
		} catch (e) { row.error = e.message; }
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
		store := NewReadOnlyAuthStorage(file)
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
					if info.Type != "" {
						entry["type"] = string(info.Type)
					}
					list = append(list, entry)
				}
				row["result"] = list
			case "delete":
				opErr = store.Delete(ctx, op.Provider)
			case "modify":
				_, opErr = store.Modify(ctx, op.Provider, func(*Credential) (*Credential, error) { return nil, nil })
			default:
				_ = os.WriteFile(file, []byte(*op.Content), 0o600)
			}
			if opErr != nil {
				message := opErr.Error()
				row = map[string]any{"op": op.Op, "error": message}
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
