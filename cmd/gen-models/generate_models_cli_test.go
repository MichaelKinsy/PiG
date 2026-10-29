package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/modelgen/modeldata"
)

// idOrderCatalog is an opencode catalog whose ids sort differently under integer-key enumeration and localeCompare.
func idOrderCatalog() map[string]any {
	api := map[string]any{"chat-completions": map[string]any{"base_url": "https://example.test/zen/v1"}}
	var models []map[string]any
	for _, id := range []string{"B", "a", "a-1", "10", "9"} {
		models = append(models, sourceModel(id, api))
	}
	return providerCatalog("opencode", models...)
}

func runGenerator(t *testing.T, modelsDev any, args ...string) (status int, stdout, stderr string) {
	t.Helper()
	status, stdout, stderr = runGeneratorSubprocess(t, t.TempDir(), modelsDev, args...)
	return status, stdout, stderr
}

func requireSuccess(t *testing.T, status int, stdout, stderr string) {
	t.Helper()
	if status != 0 {
		t.Fatalf("generator status = %d, want 0\n%s\n%s", status, stdout, stderr)
	}
}

// objectKeys lists the keys of the JSON object at the given path in document order.
func objectKeys(t *testing.T, data []byte, path ...string) []string {
	t.Helper()
	raw := json.RawMessage(data)
	for _, name := range path {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			t.Fatal(err)
		}
		raw = object[name]
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		t.Fatalf("not an object: %s (%v)", raw, err)
	}
	var keys []string
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, token.(string))
		var skipped json.RawMessage
		if err := decoder.Decode(&skipped); err != nil {
			t.Fatal(err)
		}
	}
	return keys
}

func TestGeneratorFlagValidation(t *testing.T) {
	root := t.TempDir()
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{"json-only without json-output", []string{"--json-only"}, "--json-only requires --json-output"},
		{"data-only with json output", []string{"--data-only", "--json-output", filepath.Join(root, "json"), "--data-output", filepath.Join(root, "data")}, "--data-only cannot be combined with JSON catalog output"},
		{"missing data-output", []string{"--json-output", filepath.Join(root, "json")}, "--data-output is required unless --json-only"},
		{"json-only with data-output", []string{"--json-only", "--json-output", filepath.Join(root, "json"), "--data-output", filepath.Join(root, "data")}, "--json-only cannot be combined with --data-output"},
	} {
		t.Run(test.name, func(t *testing.T) {
			status, stdout, stderr := runGenerator(t, providerCatalog("opencode"), test.args...)
			if status != 1 || stderr != "Error: "+test.want+"\n" {
				t.Errorf("status = %d, stderr = %q, want status 1 and %q\nstdout=%s", status, stderr, "Error: "+test.want+"\n", stdout)
			}
		})
	}
	for _, name := range []string{"json", "data"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Errorf("%s was created by a rejected invocation (%v)", name, err)
		}
	}
}

func TestGeneratorJSONOutputTree(t *testing.T) {
	output := filepath.Join(t.TempDir(), "catalog")
	status, stdout, stderr := runGenerator(t, idOrderCatalog(), "--json-only", "--json-output", output)
	requireSuccess(t, status, stdout, stderr)
	if !strings.Contains(stdout, "Generated JSON model catalog under "+output+"\n") {
		t.Errorf("stdout = %q, want the catalog location", stdout)
	}
	var providers []string
	data, err := os.ReadFile(filepath.Join(output, "providers.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &providers); err != nil {
		t.Fatal(err)
	}
	if !slices.IsSorted(providers) || !slices.Contains(providers, "opencode") {
		t.Fatalf("providers.json = %v, want a sorted list containing opencode", providers)
	}
	want := []string{"models.all.json", "models.json", "providers.json", "providers"}
	for _, id := range providers {
		want = append(want, filepath.Join("providers", id+".json"), filepath.Join("providers", id+".all.json"))
	}
	var got []string
	err = filepath.WalkDir(output, func(path string, entry os.DirEntry, err error) error {
		if err == nil && path != output {
			relative, _ := filepath.Rel(output, path)
			got = append(got, relative)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("output tree = %v, want %v", got, want)
	}
	// The keyed catalog and the `.all` array agree for the chat models of a provider.
	keyed := decodeJSONObject(t, filepath.Join(output, "providers", "opencode.json"))
	var all []map[string]json.RawMessage
	data, err = os.ReadFile(filepath.Join(output, "providers", "opencode.all.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &all); err != nil {
		t.Fatal(err)
	}
	// The `.all` array adds the classifier models (the mock's decision model) after the chat models.
	if len(keyed) != 5 || len(all) != 7 {
		t.Errorf("opencode has %d keyed and %d .all models, want 5 and 7", len(keyed), len(all))
	}
	if models := decodeJSONObject(t, filepath.Join(output, "models.json")); !bytes.Equal(models["opencode"], mustRead(t, filepath.Join(output, "providers", "opencode.json"))) {
		t.Errorf("models.json opencode catalog differs from providers/opencode.json")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.TrimRight(data, "\n")
}

// Models are keyed by id sorted with localeCompare; a JavaScript object enumerates integer-like keys first, ascending. Pi's
// generate.ts builds each provider with Object.fromEntries(sortedById), so `9` and `10` come first, then a, a-1, B.
func TestGeneratorOrdersModelsLikeLocaleCompareAndObjectKeys(t *testing.T) {
	output := filepath.Join(t.TempDir(), "catalog")
	status, stdout, stderr := runGenerator(t, idOrderCatalog(), "--json-only", "--json-output", output)
	requireSuccess(t, status, stdout, stderr)
	want := []string{"9", "10", "a", "a-1", "B"}
	if got := objectKeys(t, mustRead(t, filepath.Join(output, "providers", "opencode.json"))); !reflect.DeepEqual(got, want) {
		t.Errorf("providers/opencode.json keys = %v, want %v", got, want)
	}
	if got := objectKeys(t, mustRead(t, filepath.Join(output, "models.json")), "opencode"); !reflect.DeepEqual(got, want) {
		t.Errorf("models.json opencode keys = %v, want %v", got, want)
	}
	var all []struct{ ID string }
	if err := json.Unmarshal(mustRead(t, filepath.Join(output, "providers", "opencode.all.json")), &all); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, model := range all {
		ids = append(ids, model.ID)
	}
	if wantAll := append(slices.Clone(want), "jev-1.13", "jev-1.13-free"); !reflect.DeepEqual(ids, wantAll) {
		t.Errorf("providers/opencode.all.json ids = %v, want %v", ids, wantAll)
	}
}

func TestGeneratorPrettyIndentsJSON(t *testing.T) {
	root := t.TempDir()
	compact, pretty := filepath.Join(root, "compact"), filepath.Join(root, "pretty")
	for output, extra := range map[string][]string{compact: nil, pretty: {"--pretty"}} {
		status, stdout, stderr := runGenerator(t, idOrderCatalog(), append([]string{"--json-only", "--json-output", output}, extra...)...)
		requireSuccess(t, status, stdout, stderr)
	}
	compactText, prettyText := string(mustRead(t, filepath.Join(compact, "providers.json"))), string(mustRead(t, filepath.Join(pretty, "providers.json")))
	if strings.Contains(compactText, "\n") || !strings.HasPrefix(compactText, `["`) {
		t.Errorf("compact providers.json = %q, want a single line", compactText)
	}
	if !strings.HasPrefix(prettyText, "[\n  \"") || !strings.Contains(prettyText, "\",\n  \"") {
		t.Errorf("pretty providers.json = %q, want two-space indentation", prettyText)
	}
	prettyModels := string(mustRead(t, filepath.Join(pretty, "providers", "opencode.json")))
	if !strings.HasPrefix(prettyModels, "{\n  \"9\": {\n    \"") {
		t.Errorf("pretty providers/opencode.json starts %q, want nested two-space indentation", prettyModels[:min(len(prettyModels), 40)])
	}
	// Pretty printing changes only the whitespace.
	var a, b any
	if err := json.Unmarshal(mustRead(t, filepath.Join(compact, "models.json")), &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(mustRead(t, filepath.Join(pretty, "models.json")), &b); err != nil || !reflect.DeepEqual(a, b) {
		t.Errorf("pretty models.json differs from the compact one (%v)", err)
	}
}

// dataStructure reads the structure (provider -> `type:id` -> api) of the provider data files in dir.
func dataStructure(t *testing.T, dir string) modeldata.ModelDataStructure {
	t.Helper()
	structure := modeldata.ModelDataStructure{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name, isData := strings.CutSuffix(entry.Name(), ".json")
		if !isData || entry.Name() == modeldata.ModelDataManifestFile {
			continue
		}
		models := map[string]string{}
		for api, group := range decodeJSONObject(t, filepath.Join(dir, entry.Name())) {
			var identities map[string]json.RawMessage
			if err := json.Unmarshal(group, &identities); err != nil {
				t.Fatal(err)
			}
			for identity := range identities {
				models[identity] = api
			}
		}
		structure[name] = models
	}
	return structure
}

func TestGeneratorWritesValidModelDataDirectory(t *testing.T) {
	for _, pretty := range []bool{false, true} {
		name := map[bool]string{false: "compact", true: "pretty"}[pretty]
		t.Run(name, func(t *testing.T) {
			dataDir := filepath.Join(t.TempDir(), "data")
			args := []string{"--data-output", dataDir}
			if pretty {
				args = append(args, "--pretty")
			}
			status, stdout, stderr := runGenerator(t, idOrderCatalog(), args...)
			requireSuccess(t, status, stdout, stderr)
			if !strings.Contains(stdout, "Generated JSON model values under "+dataDir+"\n") {
				t.Errorf("stdout = %q, want the data location", stdout)
			}
			structure := dataStructure(t, dataDir)
			if _, ok := structure["opencode"]["chat:10"]; !ok {
				t.Fatalf("opencode structure = %v, want chat:10", structure["opencode"])
			}
			if err := modeldata.ValidateModelDataDirectory(structure, dataDir); err != nil {
				t.Errorf("generated data directory is invalid: %v", err)
			}
			if _, err := os.Stat(filepath.Join(dataDir, modeldata.ModelDataManifestFile)); err != nil {
				t.Error(err)
			}
			text := string(mustRead(t, filepath.Join(dataDir, "opencode.json")))
			if indented := strings.Contains(text, "\n  \""); indented != pretty {
				t.Errorf("opencode.json indented = %v, want %v", indented, pretty)
			}
		})
	}
}

func TestGeneratorDataOnlyRegeneratesTheProvidersInTheDataDirectory(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	status, stdout, stderr := runGeneratorSubprocess(t, root, idOrderCatalog(), "--data-output", dataDir)
	requireSuccess(t, status, stdout, stderr)
	original := mustRead(t, filepath.Join(dataDir, "opencode.json"))
	providers := dataStructure(t, dataDir)
	if len(providers) < 2 {
		t.Fatalf("data providers = %v, want opencode and at least one other", providers)
	}

	// Another provider's file is left out of the hydration and a stale opencode.json is regenerated.
	var other string
	for id := range providers {
		if id != "opencode" {
			other = id
		}
	}
	if err := os.Remove(filepath.Join(dataDir, other+".json")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dataDir, "opencode.json"), "{}\n")
	status, stdout, stderr = runGeneratorSubprocess(t, root, idOrderCatalog(), "--data-only", "--data-output", dataDir)
	requireSuccess(t, status, stdout, stderr)
	if !strings.Contains(stdout, "Hydrated JSON model values under "+dataDir+"\n") {
		t.Errorf("stdout = %q, want the hydration message", stdout)
	}
	if got := mustRead(t, filepath.Join(dataDir, "opencode.json")); !bytes.Equal(got, original) {
		t.Errorf("opencode.json = %s, want the regenerated %s", got, original)
	}
	if _, err := os.Stat(filepath.Join(dataDir, other+".json")); !os.IsNotExist(err) {
		t.Errorf("%s.json was regenerated although it was not in the data directory (%v)", other, err)
	}
	structure := dataStructure(t, dataDir)
	if _, ok := structure[other]; ok || len(structure) != len(providers)-1 {
		t.Errorf("data providers after --data-only = %v, want %d providers without %s", structure, len(providers)-1, other)
	}
	if err := modeldata.ValidateModelDataDirectory(structure, dataDir); err != nil {
		t.Errorf("hydrated data directory is invalid: %v", err)
	}

	// A provider the catalog no longer builds cannot be hydrated, and the directory stays as it was.
	writeFile(t, filepath.Join(dataDir, "ghost.json"), "{}\n")
	before := snapshotTree(t, root)
	status, stdout, stderr = runGeneratorSubprocess(t, root, idOrderCatalog(), "--data-only", "--data-output", dataDir)
	if want := "Error: Cannot hydrate missing providers: ghost\n"; status != 1 || stderr != want {
		t.Errorf("status = %d, stderr = %q, want status 1 and %q\nstdout=%s", status, stderr, want, stdout)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
		t.Error("a failed hydration changed the data directory")
	}
}

func TestGeneratorRecordAndReplay(t *testing.T) {
	root := t.TempDir()
	fixtures := filepath.Join(root, "fixtures")
	recorded, replayed := filepath.Join(root, "recorded"), filepath.Join(root, "replayed")
	status, stdout, stderr := runGeneratorSubprocess(t, root, idOrderCatalog(), "--record", fixtures, "--json-only", "--json-output", recorded)
	requireSuccess(t, status, stdout, stderr)

	var index fixtureIndex
	if err := json.Unmarshal(mustRead(t, filepath.Join(fixtures, fixtureIndexFile)), &index); err != nil {
		t.Fatal(err)
	}
	var urls, wantURLs []string
	for _, entry := range index {
		urls = append(urls, entry.URL)
		if entry.Status != 200 {
			t.Errorf("%s recorded with status %d", entry.URL, entry.Status)
		}
	}
	for url := range upstreamMockResponses(nil) {
		wantURLs = append(wantURLs, url)
	}
	slices.Sort(urls)
	slices.Sort(wantURLs)
	if !reflect.DeepEqual(urls, wantURLs) {
		t.Errorf("recorded urls = %v, want %v", urls, wantURLs)
	}

	// The replay serves the recorded bytes: the subprocess mock now serves an empty catalog, so equal output proves the replay won.
	status, stdout, stderr = runGeneratorSubprocess(t, root, map[string]any{}, "--replay", fixtures, "--json-only", "--json-output", replayed)
	requireSuccess(t, status, stdout, stderr)
	if first, second := snapshotTree(t, recorded), snapshotTree(t, replayed); !reflect.DeepEqual(first, second) || len(first) == 0 {
		t.Errorf("replayed catalog differs from the recorded run (%d vs %d files)", len(second), len(first))
	}

	t.Run("replay serves recorded bytes and fails other URLs", func(t *testing.T) {
		previous := http.DefaultClient.Transport
		t.Cleanup(func() { http.DefaultClient.Transport = previous })
		if err := replayResponses(fixtures); err != nil {
			t.Fatal(err)
		}
		for _, entry := range index {
			response, err := http.DefaultClient.Get(entry.URL)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if want, _ := os.ReadFile(filepath.Join(fixtures, entry.File)); !bytes.Equal(body, want) {
				t.Errorf("%s replayed %d bytes, want the %d recorded", entry.URL, len(body), len(want))
			}
		}
		_, err := http.DefaultClient.Get("https://example.test/unrecorded")
		if err == nil || !strings.Contains(err.Error(), "Unexpected fetch: https://example.test/unrecorded") {
			t.Errorf("unrecorded fetch error = %v, want Unexpected fetch", err)
		}
	})

	t.Run("record and replay cannot be combined", func(t *testing.T) {
		status, stdout, stderr := runGeneratorSubprocess(t, root, idOrderCatalog(), "--record", fixtures, "--replay", fixtures, "--json-only", "--json-output", filepath.Join(root, "x"))
		if want := "Error: -record and -replay cannot be combined\n"; status != 1 || stderr != want {
			t.Errorf("status = %d, stderr = %q, want status 1 and %q\nstdout=%s", status, stderr, want, stdout)
		}
	})
}
