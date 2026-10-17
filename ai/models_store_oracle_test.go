package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type modelsStoreOp struct {
	Op       string          `json:"op"`
	Provider string          `json:"provider"`
	Entry    json.RawMessage `json:"entry,omitempty"`
	Text     string          `json:"text"`
}

type modelsStoreScenario struct {
	Initial *string         `json:"initial"`
	Nested  bool            `json:"nested"`
	Ops     []modelsStoreOp `json:"ops"`
}

type modelsStoreOut struct {
	Result any     `json:"result"`
	File   *string `json:"file"`
}

func modelsStoreScenarios() []modelsStoreScenario {
	rng := rand.New(rand.NewPCG(37, 3))
	pick := func(items []string) string { return items[rng.IntN(len(items))] }
	// Entries with members pi-ai does not read (a catalog entry's extra fields) are left out: Pig's typed entry returns only the members it knows, while the file keeps them.
	initials := []string{"", "{}", "{ }", "\ufeff{}", `{"a":{"models":[],"etag":"x"}}`, `{"a":{"models":[],"etag":"x"},"b":{"models":[],"checkedAt":1.5}}`, "{", `{"a":`, "\ufeff{\"z\":{\"models\":[]}}", `{"日本語":{"models":[]}}`, "{}\n", "  {\"a\":{\"models\":[]}}  ", `{"a":{"models":[],"lastModified":1e21}}`, `{"a":null}`, `{"a":{"models":[],"etag":"y"}}`}
	// "__proto__" and "constructor" are left out: Pi reads them from Object.prototype (an empty object, a function that structuredClone rejects).
	providers := []string{"a", "b", "openai", "日本語", "with space", "", "a\"b", "x/y"}
	// Entries are written in the member order pi-ai builds them (models, lastModified, checkedAt, etag), which JSON.stringify keeps.
	entries := []json.RawMessage{
		json.RawMessage(`{"models":[]}`),
		json.RawMessage(`{"models":[],"etag":"W/\"abc\""}`),
		json.RawMessage(`{"models":[],"lastModified":1700000000,"checkedAt":1700000100.5}`),
		json.RawMessage(`{"models":[],"etag":"日本語 \u2028 \"quoted\" \\ back\ttab"}`),
		json.RawMessage(`{"models":[],"lastModified":1e21,"checkedAt":0}`),
		json.RawMessage(`{"models":[],"lastModified":0.1,"checkedAt":-0,"etag":"x"}`),
	}
	var scenarios []modelsStoreScenario
	for range 500 {
		scenario := modelsStoreScenario{Nested: rng.IntN(6) == 0}
		if rng.IntN(5) > 0 {
			initial := pick(initials)
			scenario.Initial = &initial
		}
		for range 1 + rng.IntN(7) {
			switch rng.IntN(7) {
			case 0, 1:
				scenario.Ops = append(scenario.Ops, modelsStoreOp{Op: "read", Provider: pick(providers)})
			case 2, 3, 4:
				scenario.Ops = append(scenario.Ops, modelsStoreOp{Op: "write", Provider: pick(providers), Entry: entries[rng.IntN(len(entries))]})
			case 5:
				scenario.Ops = append(scenario.Ops, modelsStoreOp{Op: "delete", Provider: pick(providers)})
			default:
				scenario.Ops = append(scenario.Ops, modelsStoreOp{Op: "rewrite", Text: pick(initials)})
			}
		}
		scenarios = append(scenarios, scenario)
	}
	return scenarios
}

// FileModelsStore (coding-agent core/models-store.ts) against pinned Pi over 500 seeded sequences of read, write, delete and an outside rewrite of the file: absent, empty, corrupt, non-object and BOM-prefixed files, provider ids that are unicode, quoted or "__proto__", entries with etag, timestamps (fractional and 1e21) and escapes; what a read returns or the error it fails with, and the exact text of the file (indentation, key order, number and string escaping, the file created as "{}" when absent) after every operation, agree.
func TestFileModelsStoreMatchesPi(t *testing.T) {
	scenarios := modelsStoreScenarios()
	input, err := json.Marshal(scenarios)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/models_store.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]modelsStoreOut
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	failures := 0
	for i, scenario := range scenarios {
		dir := filepath.Join(root, "s"+strings.Repeat("x", 0), string(rune('a'+i%26)), itoa(i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "models-store.json")
		if scenario.Nested {
			path = filepath.Join(dir, "sub", "models-store.json")
		}
		if scenario.Initial != nil {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(*scenario.Initial), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		store := NewFileModelsStore(path)
		var got []modelsStoreOut
		for _, op := range scenario.Ops {
			var result any
			switch op.Op {
			case "read":
				entry, err := store.Read(context.Background(), op.Provider)
				switch {
				case err != nil:
					result = map[string]any{"thrown": strings.ReplaceAll(err.Error(), dir, "<dir>")}
				case entry != nil:
					raw, _ := json.Marshal(entry)
					var decoded any
					_ = json.Unmarshal(raw, &decoded)
					result = decoded
				}
			case "write":
				var entry ModelsStoreEntry
				if err := json.Unmarshal(op.Entry, &entry); err != nil {
					t.Fatal(err)
				}
				if err := store.Write(context.Background(), op.Provider, entry); err != nil {
					result = map[string]any{"thrown": strings.ReplaceAll(err.Error(), dir, "<dir>")}
				}
			case "delete":
				if err := store.Delete(context.Background(), op.Provider); err != nil {
					result = map[string]any{"thrown": strings.ReplaceAll(err.Error(), dir, "<dir>")}
				}
			case "rewrite":
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(op.Text), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			out := modelsStoreOut{Result: result}
			if data, err := os.ReadFile(path); err == nil {
				text := string(data)
				out.File = &text
			}
			got = append(got, out)
		}
		raw, _ := json.Marshal(got)
		var normalized []modelsStoreOut
		_ = json.Unmarshal(raw, &normalized)
		if reflect.DeepEqual(normalized, expected[i]) {
			continue
		}
		if failures++; failures > 6 {
			continue
		}
		for k, op := range scenario.Ops {
			if !reflect.DeepEqual(normalized[k], expected[i][k]) {
				initial := "<absent>"
				if scenario.Initial != nil {
					initial = *scenario.Initial
				}
				opJSON, _ := json.Marshal(scenario.Ops[:k+1])
				g, _ := json.Marshal(normalized[k])
				e, _ := json.Marshal(expected[i][k])
				_ = op
				t.Errorf("scenario %d initial %q nested %v\n  ops %.500s\n  Pig %.600s\n  Pi  %.600s", i, initial, scenario.Nested, opJSON, g, e)
				break
			}
		}
	}
	if failures > 6 {
		t.Errorf("%d of %d scenarios differ from Pi", failures, len(scenarios))
	}
}

func itoa(i int) string {
	return strings.TrimSpace(strings.Join([]string{string(rune('0' + i/100%10)), string(rune('0' + i/10%10)), string(rune('0' + i%10))}, ""))
}
