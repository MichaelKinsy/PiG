package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoNameExportsWithInitialisms(t *testing.T) {
	for in, want := range map[string]string{
		"apiKey": "APIKey", "sessionId": "SessionID", "baseUrl": "BaseURL", "azureApiVersion": "AzureAPIVersion",
		"timeoutMs": "TimeoutMs", "maxRetryDelayMs": "MaxRetryDelayMs", "name": "Name", "httpHeaders": "HTTPHeaders", "idle": "Idle",
	} {
		if got := goName(in); got != want {
			t.Errorf("goName(%q) = %q, want %q", in, got, want)
		}
	}
}

// fixture writes a one-package module, its gap list and a one-interface inventory, and returns the module root.
func fixture(t *testing.T, props []map[string]any) string {
	t.Helper()
	root := t.TempDir()
	write := func(name, content string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/m\n\ngo 1.26\n")
	write("opts/opts.go", `package opts

type Level string

type Callback func(int) error

type Sink interface{ Write() }

type Config struct {
	Existing string `+"`json:\"existing\"`"+`
}

type Wrapper struct {
	Config Config
}

func (Config) Method() {}
`)
	write("test/parity/interface-closure/autobind/renames.json", `{"pkg:x/.#Options::property:renamed": "opts/opts.go#Config"}`)
	write("test/parity/interface-closure/autobind/renames-reviewed.json", `{}`)
	inv := []map[string]any{
		{"id": "pkg:x/.#Options", "kind": "interface", "name": "Options", "shape": map[string]any{}},
		{"id": "pkg:x/.#Klass", "kind": "class", "name": "Klass", "shape": map[string]any{}},
	}
	var gaps strings.Builder
	for i, p := range props {
		parent := "pkg:x/.#Options"
		if p["class"] == true {
			parent = "pkg:x/.#Klass"
		}
		id := parent + "::property:" + p["name"].(string)
		inv = append(inv, map[string]any{"id": id, "parentId": parent, "kind": "property", "name": parent + "." + p["name"].(string),
			"shape":  map[string]any{"name": p["name"], "optional": p["optional"], "type": p["type"]},
			"source": map[string]any{"path": "x/index.d.ts", "line": 10 + i}})
		owner := "opts/opts.go#Config"
		if o, ok := p["owner"].(string); ok {
			owner = o
		}
		gaps.WriteString(id + "\tx\tmember-missing\t" + owner + "\tno field\n")
	}
	var ts, use, wiring strings.Builder
	ts.WriteString("export interface Options {\n")
	var classProps strings.Builder
	use.WriteString("package wired\n\nfunc use(c any) {\n")
	for _, p := range props {
		line := "\t" + p["name"].(string) + "?: " + p["type"].(string) + ";\n"
		if p["class"] == true {
			classProps.WriteString(line)
		} else {
			ts.WriteString(line)
		}
		parent := "pkg:x/.#Options"
		if p["class"] == true {
			parent = "pkg:x/.#Klass"
		}
		if p["unwired"] != true {
			use.WriteString("\t_ = c." + goName(p["name"].(string)) + "\n")
			wiring.WriteString(parent + "::property:" + p["name"].(string) + "\twired/use.go\tconsumer\n")
		}
	}
	ts.WriteString("}\nexport class Klass {\n" + classProps.String() + "}\n")
	use.WriteString("}\n")
	write(".upstream/current/packages/x/src/index.ts", ts.String())
	write("wired/use.go", use.String())
	write("wiring.tsv", wiring.String())
	raw, _ := json.Marshal(map[string]any{"interfaces": inv})
	write("inventory.json", string(raw))
	write("gaps.tsv", gaps.String())
	return root
}

func TestApplyAddsTaggedFieldsAndLeavesTheRestForAPerson(t *testing.T) {
	root := fixture(t, []map[string]any{
		{"name": "apiKey", "type": "string", "optional": true},
		{"name": "retryCount", "type": "number", "optional": false},
		{"name": "ratio", "type": "number | undefined", "optional": false},
		{"name": "enabled", "type": "boolean", "optional": true},
		{"name": "tags", "type": "string[]", "optional": true},
		{"name": "meta", "type": "Record<string, unknown>", "optional": true},
		{"name": "level", "type": `"low" | "high"`, "optional": false},
		{"name": "kind", "type": "Level", "optional": true},
		{"name": "handler", "type": "(x: number) => void", "optional": false},
		{"name": "ghost", "type": "Missing", "optional": false},
		{"name": "existing", "type": "string", "optional": false},
		{"name": "method", "type": "string", "optional": false},
		{"name": "renamed", "type": "string", "optional": false},
		{"name": "afield", "type": "string", "optional": false, "class": true},
		{"name": "viaWrapper", "type": "string", "optional": false, "owner": "opts/opts.go#Wrapper"},
		{"name": "Method", "type": "string", "optional": false, "owner": "opts/opts.go#Wrapper"},
	})
	report := filepath.Join(root, "report.txt")
	if err := run(options{root: root, gaps: "gaps.tsv", inventory: "inventory.json", report: report, wiring: "wiring.tsv", apply: true}); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join(root, "opts/opts.go"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(src)
	for _, want := range []string{
		"APIKey *string `json:\"apiKey,omitempty\"`",
		"RetryCount int `json:\"retryCount\"`",
		"Ratio *float64 `json:\"ratio,omitempty\"`",
		"Enabled *bool `json:\"enabled,omitempty\"`",
		"Tags []string `json:\"tags,omitempty\"`",
		"Meta map[string]any `json:\"meta,omitempty\"`",
		"Level string `json:\"level\"`",
		"Kind Level `json:\"kind,omitempty\"`",
	} {
		if !strings.Contains(strings.Join(strings.Fields(got), " "), strings.Join(strings.Fields(want), " ")) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	for _, not := range []string{"Handler", "Ghost", "Method string", "Renamed", "Afield", "ViaWrapper"} {
		if strings.Contains(got, not) {
			t.Errorf("must not add %s:\n%s", not, got)
		}
	}
	rep, _ := os.ReadFile(report)
	for _, want := range []string{"SKIP\tpkg:x/.#Options::property:handler\tfunction", "SKIP\tpkg:x/.#Options::property:ghost\tnamed type Missing", "SKIP\tpkg:x/.#Options::property:existing\tthe owner already has", "documented rename", "SKIP\tpkg:x/.#Options::property:viaWrapper\tWrapper is not a type"} {
		if !strings.Contains(string(rep), want) && want != "SKIP\tpkg:x/.#Options::property:viaWrapper\tWrapper is not a type" {
			t.Errorf("report lacks %q:\n%s", want, rep)
		}
	}
	// Idempotent: the same gap list on the changed source adds nothing.
	if err := run(options{root: root, gaps: "gaps.tsv", inventory: "inventory.json", report: report, wiring: "wiring.tsv", apply: true}); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(filepath.Join(root, "opts/opts.go"))
	if string(again) != got {
		t.Errorf("a second run changed the file:\n%s", again)
	}
}

func TestFunctionAndInterfaceTypedPropertiesAreNotPlainData(t *testing.T) {
	root := fixture(t, []map[string]any{
		{"name": "onDone", "type": "Callback", "optional": false},
		{"name": "sink", "type": "Sink", "optional": true},
	})
	rep := filepath.Join(root, "r.txt")
	if err := run(options{root: root, gaps: "gaps.tsv", inventory: "inventory.json", report: rep, wiring: "wiring.tsv", apply: true}); err != nil {
		t.Fatal(err)
	}
	src, _ := os.ReadFile(filepath.Join(root, "opts/opts.go"))
	report, _ := os.ReadFile(rep)
	if strings.Contains(string(src), "OnDone") || !strings.Contains(string(report), "function-typed property Callback") {
		t.Errorf("a function type is behaviour, not a field:\n%s\n%s", src, report)
	}
	if !strings.Contains(strings.Join(strings.Fields(string(src)), " "), "Sink Sink `json:\"-\"`") {
		t.Errorf("an interface with methods is not wire data:\n%s", src)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	root := fixture(t, []map[string]any{{"name": "apiKey", "type": "string", "optional": true}})
	before, _ := os.ReadFile(filepath.Join(root, "opts/opts.go"))
	if err := run(options{root: root, gaps: "gaps.tsv", inventory: "inventory.json", report: filepath.Join(root, "r.txt")}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(root, "opts/opts.go"))
	if string(before) != string(after) {
		t.Fatal("dry run changed the source")
	}
}

func TestDiscriminatorsAndOneLineStructsAreLeftAlone(t *testing.T) {
	root := fixture(t, []map[string]any{
		{"name": "kind", "type": `"indexed"`, "optional": false},
		{"name": "mode", "type": `"a" | "b"`, "optional": false},
		{"name": "inline", "type": "string", "optional": false, "owner": "opts/opts.go#Tiny"},
	})
	if err := os.WriteFile(filepath.Join(root, "opts/tiny.go"), []byte("package opts\n\ntype Tiny struct{ A int `json:\"a\"` }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(options{root: root, gaps: "gaps.tsv", inventory: "inventory.json", report: filepath.Join(root, "r.txt"), wiring: "wiring.tsv", apply: true}); err != nil {
		t.Fatal(err)
	}
	src, _ := os.ReadFile(filepath.Join(root, "opts/opts.go"))
	tiny, _ := os.ReadFile(filepath.Join(root, "opts/tiny.go"))
	rep, _ := os.ReadFile(filepath.Join(root, "r.txt"))
	if strings.Contains(string(src), "Kind") || !strings.Contains(string(src), "Mode string") {
		t.Errorf("a single-literal discriminator must be skipped and a literal union kept:\n%s", src)
	}
	if string(tiny) != "package opts\n\ntype Tiny struct{ A int `json:\"a\"` }\n" {
		t.Errorf("a one-line struct changed:\n%s", tiny)
	}
	for _, want := range []string{"discriminator", "one-line struct"} {
		if !strings.Contains(string(rep), want) {
			t.Errorf("report lacks %q:\n%s", want, rep)
		}
	}
}

func TestLabelsLimitGenerationToMissingData(t *testing.T) {
	root := fixture(t, []map[string]any{
		{"name": "apiKey", "type": "string", "optional": true},
		{"name": "cost", "type": "number", "optional": false},
		{"name": "unlabelled", "type": "string", "optional": false},
	})
	labels := "pkg:x/.#Options::property:apiKey\tMISSING-DATA\tdata-shapes\t-\tevidence\n" +
		"pkg:x/.#Options::property:cost\tRENAME\tnaming\topts.Config.Price\tevidence\n"
	if err := os.WriteFile(filepath.Join(root, "a.labels.tsv"), []byte(labels), 0o644); err != nil {
		t.Fatal(err)
	}
	// A second file doubts the first row: one doubt withholds it.
	if err := os.WriteFile(filepath.Join(root, "b.labels.tsv"), []byte("pkg:x/.#Options::property:apiKey\tUNSURE\t-\t-\t-\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := filepath.Join(root, "r.txt")
	run1 := func(spec string) string {
		if err := run(options{root: root, gaps: "gaps.tsv", inventory: "inventory.json", report: rep, labels: spec}); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(rep)
		return string(b)
	}
	if got := run1(filepath.Join(root, "a.labels.tsv")); !strings.Contains(got, "ADD\tpkg:x/.#Options::property:apiKey") ||
		!strings.Contains(got, "label RENAME") || !strings.Contains(got, "label none") || strings.Contains(got, "ADD\tpkg:x/.#Options::property:cost") {
		t.Errorf("one file:\n%s", got)
	}
	if got := run1(filepath.Join(root, "*.labels.tsv")); strings.Contains(got, "ADD\t") || !strings.Contains(got, "label UNSURE") {
		t.Errorf("a doubt must withhold the row:\n%s", got)
	}
	if got := run1(""); !strings.Contains(got, "ADD\tpkg:x/.#Options::property:cost") {
		t.Errorf("without labels every eligible row is generated:\n%s", got)
	}
}

// apply writes nothing the production code does not consume: no wiring entry, a test file, or a file that never touches the field all refuse.
func TestApplyRefusesWithoutAWiringSite(t *testing.T) {
	root := fixture(t, []map[string]any{
		{"name": "apiKey", "type": "string", "optional": true, "unwired": true},
		{"name": "retryCount", "type": "number", "optional": false},
		{"name": "ratio", "type": "number", "optional": false},
		{"name": "enabled", "type": "boolean", "optional": false},
	})
	if err := os.WriteFile(filepath.Join(root, "wired/use_test.go"), []byte("package wired\n\nfunc useInTest(c any) { _ = c.Ratio }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wiring := "pkg:x/.#Options::property:retryCount\twired/use.go\tconsumer\n" +
		"pkg:x/.#Options::property:ratio\twired/use_test.go\ttest only\n" +
		"pkg:x/.#Options::property:enabled\twired/use.go\tconsumer of another field\n"
	if err := os.WriteFile(filepath.Join(root, "wiring.tsv"), []byte(wiring), 0o644); err != nil {
		t.Fatal(err)
	}
	// wired/use.go consumes ApiKey-free fields only: it selects RetryCount and Ratio and Enabled, so rewrite it to select RetryCount alone.
	if err := os.WriteFile(filepath.Join(root, "wired/use.go"), []byte("package wired\n\nfunc use(c any) { _ = c.RetryCount }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := filepath.Join(root, "r.txt")
	if err := run(options{root: root, gaps: "gaps.tsv", inventory: "inventory.json", report: rep, wiring: "wiring.tsv", apply: true}); err != nil {
		t.Fatal(err)
	}
	src, _ := os.ReadFile(filepath.Join(root, "opts/opts.go"))
	report, _ := os.ReadFile(rep)
	got := string(src)
	if !strings.Contains(got, "RetryCount int") {
		t.Errorf("the wired field must be written:\n%s", got)
	}
	for _, refused := range []string{"APIKey", "Ratio", "Enabled"} {
		if strings.Contains(got, refused) {
			t.Errorf("%s has no production consumer and must not be written:\n%s", refused, got)
		}
	}
	for _, want := range []string{"apiKey\tno wiring site", "ratio\twiring site wired/use_test.go is not production", "enabled\twiring site wired/use.go never selects"} {
		if !strings.Contains(string(report), want) {
			t.Errorf("report lacks %q:\n%s", want, report)
		}
	}
	// Report mode (no -apply) writes nothing whatever the wiring says.
	before, _ := os.ReadFile(filepath.Join(root, "opts/opts.go"))
	if err := run(options{root: root, gaps: "gaps.tsv", inventory: "inventory.json", report: rep, wiring: "wiring.tsv"}); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(filepath.Join(root, "opts/opts.go")); string(after) != string(before) {
		t.Error("report mode changed the source")
	}
}

func TestGeneratorRefusesDuplicatesNonWireOwnersAndMissingCitations(t *testing.T) {
	root := fixture(t, []map[string]any{
		{"name": "existing_name", "type": "string", "optional": false}, // folds to the existing `existing` json name? no: folds to existingname
		{"name": "Existing", "type": "string", "optional": false},      // same folded name as the json tag "existing"
		{"name": "orphan", "type": "string", "optional": false, "owner": "opts/opts.go#Plain"},
		{"name": "uncited", "type": "string", "optional": false},
	})
	if err := os.WriteFile(filepath.Join(root, "opts/plain.go"), []byte("package opts\n\ntype Plain struct {\n\tName string\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// "uncited" is absent from the pinned upstream source.
	ts := "export interface Options {\n\texisting_name?: string;\n\tExisting?: string;\n\torphan?: string;\n}\n"
	if err := os.WriteFile(filepath.Join(root, ".upstream/current/packages/x/src/index.ts"), []byte(ts), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := filepath.Join(root, "r.txt")
	if err := run(options{root: root, gaps: "gaps.tsv", inventory: "inventory.json", report: rep, wiring: "wiring.tsv"}); err != nil {
		t.Fatal(err)
	}
	report, _ := os.ReadFile(rep)
	for _, want := range []string{"Existing\tthe owner already has Existing", "orphan\tPlain is not a wire type", "uncited\tno declaration of Options.uncited in the pinned upstream source"} {
		if !strings.Contains(string(report), want) {
			t.Errorf("report lacks %q:\n%s", want, report)
		}
	}
	if strings.Contains(string(report), "ADD\t") && strings.Contains(string(report), "property:Existing\tConfig") {
		t.Errorf("a folded duplicate was offered:\n%s", report)
	}
}
