package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// Not compared: a top-level value that is not an object (null, an array, a number, a boolean, a string; Pi spreads or throws a TypeError on them) an api_key credential whose key is not a string (Pi's resolveConfigValue throws "config.startsWith is not a function") and a null credential (Pi's list throws "Cannot read properties of null (reading 'type')").
// pi: packages/coding-agent/src/core/auth-storage.ts AuthStorage.create, list, read, modify, delete. A corrupted auth.json (invalid JSON, a non-object value, a BOM, wrong-typed credentials, a file rewritten between calls) gives the same results, error messages and final bytes.
func TestAuthStorageOnCorruptedFileMatchesPi(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatal("node is required: ", err)
	}
	rng := rand.New(rand.NewSource(20261010))
	pick := func(xs []string) string { return xs[rng.Intn(len(xs))] }
	providers := []string{"openai", "anthropic", "z"}
	valid := `{"openai":{"type":"api_key","key":"sk-1"},"anthropic":{"type":"oauth","refresh":"r","access":"a","expires":1700000000000}}`
	texts := []string{
		"", " ", "\n", "{}", valid, "\ufeff" + valid, "{", "}", `{"openai":`, `{"openai":5}`, `{"openai":"x"}`, `{"openai":[]}`, `{"openai":{}}`,
		`{"openai":{"type":"api_key"}}`, `{"openai":{"type":"nope","key":"k"}}`, `{"openai":{"type":"api_key","key":"k","env":{"A":"$B"}}}`,
		`{"openai":{"type":"api_key","key":"!echo hi"}}`, `{"openai":{"type":"api_key","key":"$HOME_NOT_SET_X"}}`, `{"a":1,"a":2}`, valid + "\n", valid + "garbage", "garbage" + valid, `{"openai":{"type":"oauth"}}`,
		`{"anthropic":{"type":"oauth","refresh":1,"access":2,"expires":"soon"}}`, `{"__proto__":{"type":"api_key","key":"p"}}`, `{"10":{"type":"api_key","key":"n"}}`,
	}
	type script struct {
		Initial *string          `json:"initial"`
		Ops     []map[string]any `json:"ops"`
	}
	var scripts []script
	for range 800 {
		s := script{}
		if rng.Intn(8) != 0 {
			text := pick(texts)
			s.Initial = &text
		}
		for range 1 + rng.Intn(6) {
			provider := pick(providers)
			switch rng.Intn(6) {
			case 0:
				s.Ops = append(s.Ops, map[string]any{"op": "list"})
			case 1:
				s.Ops = append(s.Ops, map[string]any{"op": "read", "provider": provider})
			case 2:
				s.Ops = append(s.Ops, map[string]any{"op": "modify", "provider": provider, "key": pick([]string{"k1", "é", "a b"})})
			case 3:
				s.Ops = append(s.Ops, map[string]any{"op": "delete", "provider": provider})
			case 4:
				s.Ops = append(s.Ops, map[string]any{"op": "overwrite", "text": pick(texts)})
			default:
				s.Ops = append(s.Ops, map[string]any{"op": "read", "provider": provider})
			}
		}
		scripts = append(scripts, s)
	}
	input, _ := json.Marshal(scripts)
	cmd := exec.CommandContext(t.Context(), "node", "testdata/auth_load.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []struct {
		Outcomes []string
		Text     *string
	}
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	var differences []string
	for i, s := range scripts {
		path := filepath.Join(t.TempDir(), "auth.json")
		if s.Initial != nil {
			if err := os.WriteFile(path, []byte(*s.Initial), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		storage, err := NewAuthStorage(path)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, op := range s.Ops {
			provider, _ := op["provider"].(string)
			var outcome string
			switch op["op"] {
			case "overwrite":
				_ = os.WriteFile(path, []byte(op["text"].(string)), 0o600)
				outcome = "ok"
			case "list":
				infos, lerr := storage.List(context.Background())
				if lerr != nil {
					outcome = "error " + lerr.Error()
					break
				}
				items := make([]map[string]any, 0, len(infos))
				for _, info := range infos {
					item := map[string]any{"providerId": info.ProviderID}
					if info.Type != "" {
						item["type"] = info.Type
					}
					items = append(items, item)
				}
				encoded, _ := json.Marshal(items)
				outcome = string(encoded)
			case "read":
				credential, rerr := storage.Read(context.Background(), provider)
				if rerr != nil {
					outcome = "error " + rerr.Error()
					break
				}
				outcome = marshalCredentialOutcome(credential)
			case "delete":
				if derr := storage.Delete(context.Background(), provider); derr != nil {
					outcome = "error " + derr.Error()
				} else {
					outcome = "ok"
				}
			default:
				credential, merr := storage.Modify(context.Background(), provider, func(*Credential) (*Credential, error) {
					return &Credential{Type: CredentialAPIKey, Key: op["key"].(string)}, nil
				})
				if merr != nil {
					outcome = "error " + merr.Error()
					break
				}
				outcome = marshalCredentialOutcome(credential)
			}
			got = append(got, outcome)
		}
		raw, readErr := os.ReadFile(path)
		text := string(raw)
		if readErr != nil {
			text = "<none>"
		}
		wantText := "<none>"
		if want[i].Text != nil {
			wantText = *want[i].Text
		}
		if strings.Join(got, "\n") != strings.Join(want[i].Outcomes, "\n") || text != wantText {
			differences = append(differences, fmt.Sprintf("script %d: initial %v ops %v\n  go: %q file %q\n  pi: %q file %q", i, deref2(s.Initial), s.Ops, got, text, want[i].Outcomes, wantText))
		}
	}
	if len(differences) > 0 {
		shown := differences
		if len(shown) > 8 {
			shown = shown[:8]
		}
		t.Errorf("%d of %d scripts differ from Pi:\n%s", len(differences), len(scripts), strings.Join(shown, "\n"))
	}
}

func marshalCredentialOutcome(credential *Credential) string {
	if credential == nil {
		return "null"
	}
	encoded, err := json.Marshal(credential)
	if err != nil {
		return "marshal error " + err.Error()
	}
	return string(encoded)
}

func deref2(s *string) string {
	if s == nil {
		return "<absent>"
	}
	return *s
}
