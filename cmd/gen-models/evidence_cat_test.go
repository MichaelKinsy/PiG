package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func catEvidenceRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.FromSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// The README of internal/modelgen is the documentation of this generator: every generation flag it takes is documented, every
// flag the README names exists, and the files, make targets, scripts and tests it points at exist.
func TestModelgenReadmeDocumentsTheGeneratorAsBuilt(t *testing.T) {
	readme := catEvidenceRead(t, "../../internal/modelgen/README.md")
	source := catEvidenceRead(t, "main.go")

	defined := map[string]string{}
	for _, match := range regexp.MustCompile(`flag\.(?:String|Bool)\("([a-z-]+)", [^,]+, "([^"]*)"\)`).FindAllStringSubmatch(source, -1) {
		defined[match[1]] = match[2]
	}
	if len(defined) < 12 {
		t.Fatalf("found %d flags in main.go, want all of them", len(defined))
	}
	for name, usage := range defined {
		if strings.HasPrefix(usage, "generation:") && !regexp.MustCompile("(?:^|[\\s\x60])-"+name+"\\b").MatchString(readme) {
			t.Errorf("generation flag -%s is not documented in internal/modelgen/README.md", name)
		}
	}
	for _, match := range regexp.MustCompile("(?:^|[\\s\x60])-([a-z][a-z-]*)\\b").FindAllStringSubmatch(readme, -1) {
		// -run belongs to the `go test` command line the README quotes.
		if _, ok := defined[match[1]]; !ok && match[1] != "run" {
			t.Errorf("README documents -%s, which the generator does not define", match[1])
		}
	}
	for _, flagName := range []string{"src", "out"} {
		if !strings.Contains(readme, "-"+flagName+" ") {
			t.Errorf("README does not mention -%s", flagName)
		}
	}

	// Layout table: every Go path named under internal/modelgen exists (relative to that directory).
	var paths []string
	for _, row := range regexp.MustCompile("(?m)^\\| (`[^|]+`) \\|").FindAllStringSubmatch(readme, -1) {
		for _, name := range regexp.MustCompile("`([^`]+)`").FindAllStringSubmatch(row[1], -1) {
			paths = append(paths, name[1])
		}
	}
	if len(paths) < 8 {
		t.Fatalf("README layout table names %d paths", len(paths))
	}
	for _, name := range paths {
		if strings.ContainsAny(name, "<*") || !strings.ContainsAny(name, "./") {
			continue
		}
		if _, err := os.Stat(filepath.Join("../../internal/modelgen", name)); err != nil {
			t.Errorf("README layout names %s: %v", name, err)
		}
	}
	for _, name := range []string{"models_dev.go", "source_openrouter.go", "source_vercel.go", "sources.go", "family_anthropic.go", "define.go", "build.go", "reasoning.go", "providers/index.go"} {
		if !slices.Contains(paths, name) && !strings.Contains(readme, "`"+name+"`") && !strings.Contains(readme, "`family_*.go`") {
			t.Errorf("README layout does not list %s", name)
		}
	}

	makefile := catEvidenceRead(t, "../../Makefile")
	for _, target := range []string{"model-catalogs", "pin-bump"} {
		if !strings.Contains(readme, "`make "+target+"`") && !strings.Contains(readme, target) {
			t.Errorf("README does not name make %s", target)
		}
		if !regexp.MustCompile("(?m)^" + target + ":").MatchString(makefile) {
			t.Errorf("Makefile has no %s target", target)
		}
	}
	scriptDir := "../../test/parity/testdata/catalog-generation/"
	script := catEvidenceRead(t, scriptDir+"equivalence.sh")
	for _, command := range regexp.MustCompile(`equivalence\.sh ([a-z]+)\b`).FindAllStringSubmatch(readme, -1) {
		if !strings.Contains(script, command[1]) {
			t.Errorf("equivalence.sh has no %s command", command[1])
		}
	}
	if _, err := os.Stat(scriptDir + "prune.mjs"); err != nil || !strings.Contains(readme, "prune.mjs") {
		t.Errorf("prune.mjs: %v", err)
	}
	if !strings.Contains(catEvidenceRead(t, "catalog_matches_pi_test.go"), "func TestCatalogGenerationMatchesPi(") || !strings.Contains(readme, "-run TestCatalogGenerationMatchesPi") {
		t.Error("README names TestCatalogGenerationMatchesPi, which does not exist")
	}
}

// The statistics block Pi's generator prints counts tool-capable models and reasoning-capable ones, then lists each provider; the
// counts must be those of the generated chat catalog.
func TestGeneratorPrintsModelStatistics(t *testing.T) {
	api := map[string]any{"chat-completions": map[string]any{"base_url": "https://example.test/zen/v1"}}
	reasoner := sourceModel("thinker", api)
	reasoner["capabilities"] = map[string]any{"tools": map[string]any{"supported": true}, "reasoning": map[string]any{"supported": true}}
	output := filepath.Join(t.TempDir(), "catalog")
	status, stdout, stderr := runGenerator(t, providerCatalog("opencode", sourceModel("a", api), sourceModel("b", api), reasoner), "--json-only", "--json-output", output)
	requireSuccess(t, status, stdout, stderr)
	var catalog map[string]map[string]struct {
		Reasoning bool `json:"reasoning"`
	}
	if err := json.Unmarshal(mustRead(t, filepath.Join(output, "models.json")), &catalog); err != nil {
		t.Fatal(err)
	}
	total, reasoning := 0, 0
	for _, models := range catalog {
		for _, model := range models {
			total++
			if model.Reasoning {
				reasoning++
			}
		}
	}
	if reasoning == 0 || reasoning == total {
		t.Fatalf("fixture needs both reasoning and plain models: %d of %d", reasoning, total)
	}
	want := "\nModel Statistics:\n  Total tool-capable models: " + strconv.Itoa(total) + "\n  Reasoning-capable models: " + strconv.Itoa(reasoning) + "\n"
	if !strings.Contains(stdout, want) {
		t.Errorf("stdout lacks %q:\n%s", want, stdout)
	}
	if !strings.Contains(stdout, "\n  opencode: 3 chat models, 0 image models, ") {
		t.Errorf("stdout lacks the opencode line:\n%s", stdout)
	}
}

// main.go dispatches between the two jobs: generation flags alone generate, -src alone emits the Go catalogs, and mixing them
// (or giving neither) is a usage error with status 2.
func TestGeneratorModeDispatchRejectsMixedAndMissingModes(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"neither", nil, "gen-models: -src is required\n"},
		{"src with strict", []string{"-src", "x.ts", "-strict"}, "gen-models: -strict, -data-only, -json-only, -json-output, -data-output, -record and -replay generate catalog data and cannot be combined with -src\n"},
		{"src with data-output", []string{"-src", "x.ts", "-data-output", "d"}, "gen-models: -strict, -data-only, -json-only, -json-output, -data-output, -record and -replay generate catalog data and cannot be combined with -src\n"},
		{"src with replay", []string{"-src", "x.ts", "-replay", "d"}, "gen-models: -strict, -data-only, -json-only, -json-output, -data-output, -record and -replay generate catalog data and cannot be combined with -src\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, stdout, stderr := runGenerator(t, providerCatalog("opencode"), tc.args...)
			if status != 2 || stderr != tc.want {
				t.Errorf("status = %d, stderr = %q, want status 2 and %q\nstdout=%s", status, stderr, tc.want, stdout)
			}
		})
	}
}
