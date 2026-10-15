package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// ai/src/providers/model-schema.ts ModelPromptCacheSchema, used by model-config.ts for a model definition and a model
// override: only the retention tiers "short" and "long" may appear, each a number above zero. The text models.json
// reports for every shape of the object and of its two values, against the pinned Pi.
func TestModelsJSONPromptCacheSchemaMatchesPi(t *testing.T) {
	values := []string{
		`1`, `0`, `-1`, `-0`, `0.5`, `1e-300`, `5e-324`, `1e308`, `1e999`, `-1e999`, `120`, `1.5`, `null`, `true`, `"60"`, `""`, `[]`, `[60]`, `{}`, `{"a":1}`,
	}
	var promptCaches []string
	for _, v := range values {
		promptCaches = append(promptCaches, `{"short":`+v+`}`, `{"long":`+v+`}`, `{"short":60,"long":`+v+`}`, `{"short":`+v+`,"long":`+v+`}`)
	}
	promptCaches = append(promptCaches,
		`{}`, `{"short":60,"long":3600}`, `{"medium":60}`, `{"none":60}`, `{"short":60,"medium":60}`, `{"Short":60}`, `{"short ":60}`, `{"":60}`, `{"__proto__":60}`, `{"constructor":60}`,
		`{"toString":1}`, `[]`, `[{"short":60}]`, `null`, `5`, `"short"`, `true`, `{"short":60,"short":-1}`, `{"short":-1,"short":60}`,
	)
	var docs []string
	for _, cache := range promptCaches {
		docs = append(docs,
			`{"providers":{"x":{"baseUrl":"https://e.test/v1","api":"openai-completions","apiKey":"k","models":[{"id":"a","promptCache":`+cache+`}]}}}`,
			`{"providers":{"x":{"modelOverrides":{"m":{"promptCache":`+cache+`}}}}}`,
		)
	}

	dir := t.TempDir()
	for i, doc := range docs {
		if err := os.MkdirAll(filepath.Join(dir, fmt.Sprint(i)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprint(i), "models.json"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/model_config.mjs", pigversion.UpstreamPackageVersion, dir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []string
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for i, doc := range docs {
		got := NewModelRegistryWithModelsPath(filepath.Join(dir, fmt.Sprint(i), "models.json")).LoadError()
		if expected[i] == "" && strings.HasPrefix(got, "Provider ") {
			// A provider that fails composition is reported by the composer (applyModelsJson), not by ModelConfig.
			continue
		}
		if got != expected[i] {
			if failures++; failures <= 8 {
				t.Errorf("doc %d %s\n  Pig %q\n  Pi  %q", i, doc, got, expected[i])
			}
		}
	}
	if failures > 8 {
		t.Errorf("%d of %d documents differ from Pi", failures, len(docs))
	}
}
