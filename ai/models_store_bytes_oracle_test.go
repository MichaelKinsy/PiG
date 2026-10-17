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
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// pi: packages/ai/src/models-store.ts

// The bytes of a models store file after writes and deletes (FileModelsStore: `JSON.stringify({ ...current, [provider]: entry }, null, 2)`): the member
// order of every catalog model as the catalog sent it, unknown members and unknown model types kept, the optional entry members, and the provider order.
// The pinned FileModelsStore and PiG's write the same entries over the same starting files.
func TestModelsStoreFileBytesMatchPi(t *testing.T) {
	rng := rand.New(rand.NewSource(71))
	// number spells an integer the way JSON.parse reads it back unchanged: plain, 12.0, or 12E0.
	number := func(n int) string {
		return []string{strconv.Itoa(n), strconv.Itoa(n) + ".0", strconv.Itoa(n) + "E0", strconv.Itoa(n) + "0e-1"}[rng.Intn(4)]
	}
	quote := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	chunks := []string{"a", "é", "😀", "\u2028", "\u2029", "<&>", "\\", "\"", "\n", "\u007f", "x y", "/"}
	str := func() string {
		var b strings.Builder
		for range 1 + rng.Intn(4) {
			b.WriteString(chunks[rng.Intn(len(chunks))])
		}
		return b.String()
	}
	model := func() string {
		members := []string{
			`"id":` + quote(str()),
			`"name":` + quote(str()),
			`"api":"openai-completions"`,
			`"provider":` + quote(str()),
			`"baseUrl":"https://example.test/v1"`,
			`"reasoning":` + []string{"true", "false"}[rng.Intn(2)],
			`"input":["text"` + []string{"", `,"image"`}[rng.Intn(2)] + `]`,
			fmt.Sprintf(`"cost":{"input":%d,"output":%d,"cacheRead":0,"cacheWrite":0}`, rng.Intn(10), rng.Intn(10)),
			fmt.Sprintf(`"contextWindow":%s`, number(1000+rng.Intn(100000))),
			fmt.Sprintf(`"maxTokens":%s`, number(100+rng.Intn(10000))),
		}
		rng.Shuffle(len(members), func(i, j int) { members[i], members[j] = members[j], members[i] })
		if rng.Intn(3) == 0 {
			members = append(members, `"zzz":{"b":1,"a":`+quote(str())+`}`)
		}
		if rng.Intn(4) == 0 {
			members = append(members, `"aaa":[1,2.5,null]`)
		}
		return "{" + strings.Join(members, ",") + "}"
	}
	entry := func() string {
		var models []string
		for range rng.Intn(4) {
			if rng.Intn(6) == 0 {
				models = append(models, `{"id":"future","api":"future-api","extra":`+quote(str())+`}`)
				continue
			}
			models = append(models, model())
		}
		// The member order of the entry literals in remote-catalog-provider.ts.
		members := []string{`"models":[` + strings.Join(models, ",") + `]`}
		if rng.Intn(2) == 0 {
			members = append(members, `"checkedAt":1700000000.5`)
		}
		if rng.Intn(2) == 0 {
			members = append(members, fmt.Sprintf(`"lastModified":%d`, rng.Intn(2000000000)))
		}
		if rng.Intn(2) == 0 {
			members = append(members, `"etag":`+quote(str()))
		}
		return "{" + strings.Join(members, ",") + "}"
	}
	providers := []string{"openai", "anthropic", "10", "2", "z", "a", "é", "<&>"}
	type script struct {
		Initial *string          `json:"initial"`
		Ops     []map[string]any `json:"ops"`
	}
	var scripts []script
	for range 300 {
		s := script{}
		switch rng.Intn(4) {
		case 1:
			text := "{}"
			s.Initial = &text
		case 2:
			text := fmt.Sprintf(`{"zeta":%s,"10":%s}`, entry(), entry())
			s.Initial = &text
		case 3:
			text := "\ufeff" + fmt.Sprintf(`{"openai":%s}`, entry())
			s.Initial = &text
		}
		for range 1 + rng.Intn(5) {
			provider := providers[rng.Intn(len(providers))]
			if rng.Intn(4) == 0 {
				s.Ops = append(s.Ops, map[string]any{"op": "delete", "provider": provider})
			} else {
				s.Ops = append(s.Ops, map[string]any{"op": "write", "provider": provider, "entry": entry()})
			}
		}
		scripts = append(scripts, s)
	}
	input, err := json.Marshal(scripts)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/models_store_bytes.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []*string
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for i, s := range scripts {
		path := filepath.Join(t.TempDir(), "models-store.json")
		if s.Initial != nil {
			if err := os.WriteFile(path, []byte(*s.Initial), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		store := NewFileModelsStore(path)
		for _, op := range s.Ops {
			provider := op["provider"].(string)
			if op["op"] == "delete" {
				err = store.Delete(context.Background(), provider)
			} else {
				var entry ModelsStoreEntry
				if err = json.Unmarshal([]byte(op["entry"].(string)), &entry); err == nil {
					err = store.Write(context.Background(), provider, entry)
				}
			}
			if err != nil {
				t.Fatalf("script %d: %v", i, err)
			}
		}
		raw, readErr := os.ReadFile(path)
		got, expected := string(raw), "<none>"
		if readErr != nil {
			got = "<none>"
		}
		if want[i] != nil {
			expected = *want[i]
		}
		if got != expected {
			if failures++; failures <= 3 {
				t.Errorf("script %d: initial %v ops %v\n PiG %q\n Pi  %q", i, s.Initial, s.Ops, got, expected)
			}
		}
	}
	if failures > 3 {
		t.Errorf("%d of %d models stores differ from Pi", failures, len(scripts))
	}
}
