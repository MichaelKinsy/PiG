package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/test/parity/upstreampackages"
)

func TestDiscoverAsyncSources(t *testing.T) {
	root := asyncSourceFixture(t)
	writeAsyncTestFile(t, root, "packages/ai/src/awaited.ts", "export async function load() {}")
	writeAsyncTestFile(t, root, "packages/agent/src/promise.ts", "type Loader = () => Promise<void>;")
	writeAsyncTestFile(t, root, "packages/tui/src/sync.ts", "export function render() {}")
	writeAsyncTestFile(t, root, "packages/coding-agent/src/ignored.test.ts", "async function test() {}")
	writeAsyncTestFile(t, root, "packages/codemode/src/runtime/host.ts", "export async function run() {}")
	writeAsyncTestFile(t, root, "packages/mcp/src/transports/streamable-http.ts", "const fetchOnce = (): Promise<void> => Promise.resolve();")

	got, err := discoverAsyncSources(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"packages/agent/src/promise.ts",
		"packages/ai/src/awaited.ts",
		"packages/codemode/src/runtime/host.ts",
		"packages/mcp/src/transports/streamable-http.ts",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("discoverAsyncSources() = %v, want %v", got, want)
	}
}

func TestDiscoverAsyncSourcesRejectsMisleadingMirror(t *testing.T) {
	for _, listed := range upstreampackages.All() {
		pkg := listed.Key
		t.Run(pkg, func(t *testing.T) {
			for _, test := range []struct {
				name  string
				field string
				value any
			}{
				{name: "wrong-name", field: "name", value: "@example/not-pi"},
				{name: "other-tracked-name", field: "name", value: "@earendil-works/pi-" + map[string]string{"agent": "ai", "ai": "tui", "coding-agent": "agent-core", "tui": "coding-agent", "codemode": "mcp", "mcp": "codemode"}[pkg] + "-not-listed"},
				{name: "missing-name", field: "name"},
				{name: "version-prefix", field: "version", value: "v" + coding.UpstreamPackageVersion},
				{name: "version-range", field: "version", value: "^" + coding.UpstreamPackageVersion},
				{name: "version-prerelease", field: "version", value: coding.UpstreamPackageVersion + "-rc.1"},
				{name: "version-build", field: "version", value: coding.UpstreamPackageVersion + "+local"},
				{name: "version-whitespace", field: "version", value: coding.UpstreamPackageVersion + " "},
				{name: "missing-version", field: "version"},
				{name: "numeric-version", field: "version", value: 1},
			} {
				t.Run(test.name, func(t *testing.T) {
					root := asyncSourceFixture(t)
					manifestPath := filepath.Join(root, "packages", pkg, "package.json")
					body, err := os.ReadFile(manifestPath)
					if err != nil {
						t.Fatal(err)
					}
					var manifest map[string]any
					if err := json.Unmarshal(body, &manifest); err != nil {
						t.Fatal(err)
					}
					manifest[test.field] = test.value
					body, err = json.Marshal(manifest)
					if err != nil {
						t.Fatal(err)
					}
					writeAsyncTestFile(t, root, "packages/"+pkg+"/package.json", string(body))
					if _, err := discoverAsyncSources(root); err == nil || !strings.Contains(err.Error(), manifestPath) {
						t.Fatalf("discoverAsyncSources() error = %v, want rejection naming %s", err, manifestPath)
					}
				})
			}
		})
	}
}

func asyncSourceFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range upstreampackages.All() {
		pkg, name := p.Key, p.Name
		body, err := json.Marshal(map[string]string{"name": name, "version": coding.UpstreamPackageVersion})
		if err != nil {
			t.Fatal(err)
		}
		writeAsyncTestFile(t, root, "packages/"+pkg+"/package.json", string(body))
		writeAsyncTestFile(t, root, "packages/"+pkg+"/src/sync.ts", "export function sync() {}")
	}
	return root
}

func TestAC35RejectsMissingAndPendingAsyncContracts(t *testing.T) {
	paths := []string{"packages/ai/src/added.ts", "packages/ai/src/existing.ts"}
	manifest := asyncManifest{
		Version: coding.UpstreamVersion,
		Files: []asyncAudit{{
			Path:        "packages/ai/src/added.ts",
			Disposition: "pending",
		}},
	}
	problems := problemLines(validateManifest(manifest, paths, t.TempDir()))
	for _, want := range []string{"packages/ai/src/added.ts remains pending", "missing file audit packages/ai/src/existing.ts"} {
		if !strings.Contains(problems, want) {
			t.Fatalf("problems %q do not contain %q", problems, want)
		}
	}
}

func TestValidateManifestAcceptsTranslatedAndExplicitNonRuntimeAsync(t *testing.T) {
	repo := t.TempDir()
	writeAsyncTestFile(t, repo, "ai/async_test.go", "package ai\n\nimport \"testing\"\n\nfunc TestAwaited(t *testing.T) {}\n")
	paths := []string{"packages/ai/src/runtime.ts", "packages/ai/src/types.ts"}
	manifest := asyncManifest{
		Version: coding.UpstreamVersion,
		Files: []asyncAudit{
			{
				Path:        "packages/ai/src/runtime.ts",
				Disposition: "translated",
				Contracts:   []string{"awaited", "cancellable"},
				Evidence:    []string{"test:ai/async_test.go#TestAwaited"},
			},
			{
				Path:        "packages/ai/src/types.ts",
				Disposition: "no-runtime-async",
				Rationale:   "Promise appears only in a TypeScript declaration",
			},
		},
	}
	if problems := validateManifest(manifest, paths, repo); len(problems) != 0 {
		t.Fatalf("validateManifest() problems = %v", problems)
	}
}

// evidenceFixtureFiles is a repository with one file of every evidence kind the ledger takes. evidenceRow is the row that
// cites them.
const evidenceRow = "packages/ai/src/runtime.ts"

var evidenceFixtureFiles = map[string]string{
	"ai/async.go": "package ai\n",
	"ai/async_test.go": `package ai

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func TestAwaited(t *testing.T) {
	t.Run("settles in call order", func(t *testing.T) {
		t.Run("inner", func(t *testing.T) {})
	})
	for _, name := range []string{"computed"} {
		t.Run(name, func(t *testing.T) {})
	}
	t.Run("skipped sub", func(t *testing.T) {
		t.Skip("not yet")
	})
}

func TestSkipsFirst(t *testing.T) {
	t.Skip("blocked")
}

func TestLiveOnly(t *testing.T) {
	key := testenv.RequireLiveEnv(t, "API_KEY")
	_ = key
}

func TestSkipsOnACondition(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
}

func FuzzAwaited(f *testing.F) {}

func helperNotATest(t *testing.T) {}

func BenchmarkAwaited(b *testing.B) {}
`,
	"durable/host.test.ts": `import { describe, it, test } from "vitest";

describe("host", () => {
	it("replies in any order", async () => {});
	it.skip("waits for the remote", async () => {});
	it.skipIf(process.platform === "win32")("closes the socket", () => {});
	test('single quoted case', () => {});
	const helper = "not a case";
});
`,
	"durable/host.ts": "export const host = 1;\n",
	"sdk-py/tests/test_order.py": `import sys

import pytest


def test_handlers_start_in_arrival_order():
    pass


@pytest.mark.skip(reason="blocked")
def test_skipped_by_decorator():
    pass


@pytest.mark.skipif(sys.platform == "win32", reason="posix")
def test_skipped_on_a_condition():
    pass


def helper():
    pass


class TestOrder:
    def test_method(self):
        pass
`,
	"sdk-rs/src/order.rs": `fn helper() {}

#[cfg(test)]
mod tests {
    #[test]
    fn a_later_request_waits() {}

    #[tokio::test(flavor = "multi_thread")]
    async fn an_async_request_waits() {}

    #[test]
    #[ignore = "blocked"]
    fn an_ignored_request() {}
}
`,
	"sdk-lisp/tests/order-tests.lisp": `(deftest handlers-start-in-order
  (check t))

(defun helper () nil)
`,
	"test/parity/scenarios/rpc/01-order.toml": "name = \"order\"\ncovers = [\"" + evidenceRow + "\"]\n",
	"test/parity/scenarios/rpc/02-other.toml": "name = \"other\"\ncovers = [\"packages/ai/src/other.ts\"]\n",
	"ai/01-order.toml":                        "name = \"order\"\ncovers = [\"" + evidenceRow + "\"]\n",
	"docs/findings/order.md":                  "# Order probe\n\n## Arrival order\n\nThe host starts handlers in arrival order.\n",
}

// evidenceProblems validates one translated row that cites evidence in the fixture repository.
func evidenceProblems(t *testing.T, evidence ...string) []problem {
	t.Helper()
	repo := t.TempDir()
	for path, body := range evidenceFixtureFiles {
		writeAsyncTestFile(t, repo, path, body)
	}
	manifest := asyncManifest{
		Version: coding.UpstreamVersion,
		Files:   []asyncAudit{{Path: evidenceRow, Disposition: "translated", Contracts: []string{"awaited"}, Evidence: evidence}},
	}
	return validateManifest(manifest, []string{evidenceRow}, repo)
}

func problemLines(problems []problem) string {
	lines := make([]string, len(problems))
	for i, p := range problems {
		lines[i] = "[" + p.Class + "] " + p.Text
	}
	return strings.Join(lines, "\n")
}

// An evidence entry names a case the checker finds in the cited file: a declared Go test (and a subtest that is a string
// literal of a t.Run in it), a TypeScript case title, a Python test function, a Rust #[test] function, a Lisp deftest form,
// a scenario that covers the row, or a document (and a heading of it).
func TestEvidenceThatNamesAnExistingCaseIsAccepted(t *testing.T) {
	for _, evidence := range []string{
		"test:ai/async_test.go#TestAwaited",
		"test:ai/async_test.go#FuzzAwaited",
		"test:ai/async_test.go#TestAwaited/settles in call order",
		"test:ai/async_test.go#TestAwaited/settles_in_call_order",
		"test:ai/async_test.go#TestAwaited/settles_in_call_order/inner",
		"test:ai/async_test.go#TestSkipsOnACondition",
		"test:durable/host.test.ts#replies in any order",
		"test:durable/host.test.ts#single quoted case",
		"test:durable/host.test.ts#closes the socket",
		"test:durable/host.test.ts#host",
		"test:sdk-py/tests/test_order.py#test_handlers_start_in_arrival_order",
		"test:sdk-py/tests/test_order.py#test_skipped_on_a_condition",
		"test:sdk-py/tests/test_order.py#TestOrder::test_method",
		"test:sdk-rs/src/order.rs#a_later_request_waits",
		"test:sdk-rs/src/order.rs#tests::an_async_request_waits",
		"test:sdk-lisp/tests/order-tests.lisp#handlers-start-in-order",
		"scenario:test/parity/scenarios/rpc/01-order.toml",
		"probe:docs/findings/order.md",
		"probe:docs/findings/order.md#Arrival order",
	} {
		t.Run(evidence, func(t *testing.T) {
			if problems := evidenceProblems(t, evidence); len(problems) != 0 {
				t.Fatalf("problems = %s, want none", problemLines(problems))
			}
		})
	}
}

// Each rule of an evidence entry has its own class: the gate counts by it.
func TestEvidenceRulesRejectWhatDoesNotResolve(t *testing.T) {
	for _, test := range []struct {
		evidence string
		class    string
		text     string
	}{
		// (a) A Go test file is cited with the name of a test declared in it.
		{"test:ai/async_test.go", "unnamed-test", "names no test"},
		{"test:ai/async_test.go#", "unnamed-test", "names no test"},
		{"test:ai/async_test.go#TestRenamed", "name-not-found", "declares no test TestRenamed"},
		{"test:ai/async_test.go#helperNotATest", "name-not-found", "declares no test helperNotATest"},
		{"test:ai/async_test.go#BenchmarkAwaited", "name-not-found", "declares no test BenchmarkAwaited"},
		{"test:ai/async_test.go#TestAwaited/computed", "subtest-not-found-statically", `subtest "computed"`},
		{"test:ai/async_test.go#TestAwaited/inner", "subtest-not-found-statically", `subtest "inner"`},
		{"test:ai/async_test.go#TestAwaited/settles_in_call_order/missing", "subtest-not-found-statically", `subtest "missing"`},
		// (b) A test that only skips is not evidence.
		{"test:ai/async_test.go#TestSkipsFirst", "skipped-evidence", "TestSkipsFirst begins with t.Skip"},
		{"test:ai/async_test.go#TestLiveOnly", "skipped-evidence", "TestLiveOnly begins with testenv.RequireLiveEnv"},
		{"test:ai/async_test.go#TestAwaited/skipped_sub", "skipped-evidence", "begins with t.Skip"},
		// (c) Evidence in another language names a case of the file.
		{"test:durable/host.test.ts", "unnamed-test", "names no test"},
		{"test:durable/host.test.ts#no such case", "name-not-found", "no it, test or describe call titled"},
		{"test:durable/host.test.ts#not a case", "name-not-found", "no it, test or describe call titled"},
		{"test:durable/host.test.ts#waits for the remote", "skipped-evidence", "it.skip"},
		{"test:sdk-py/tests/test_order.py", "unnamed-test", "names no test"},
		{"test:sdk-py/tests/test_order.py#test_missing", "name-not-found", "no def test_missing"},
		{"test:sdk-py/tests/test_order.py#helper", "name-not-found", "not a test"},
		{"test:sdk-py/tests/test_order.py#TestMissing::test_method", "name-not-found", "no class TestMissing"},
		{"test:sdk-py/tests/test_order.py#test_skipped_by_decorator", "skipped-evidence", "@pytest.mark.skip"},
		{"test:sdk-rs/src/order.rs", "unnamed-test", "names no test"},
		{"test:sdk-rs/src/order.rs#missing", "name-not-found", "no #[test] fn missing"},
		{"test:sdk-rs/src/order.rs#helper", "name-not-found", "no #[test] fn helper"},
		{"test:sdk-rs/src/order.rs#an_ignored_request", "skipped-evidence", "#[ignore"},
		{"test:sdk-lisp/tests/order-tests.lisp", "unnamed-test", "names no test"},
		{"test:sdk-lisp/tests/order-tests.lisp#helper", "name-not-found", "no (deftest helper"},
		{"scenario:test/parity/scenarios/rpc/02-other.toml", "scenario-not-covering", "does not cover the audited path"},
		{"scenario:test/parity/scenarios/rpc/01-order.toml#order", "invalid-evidence", "a scenario is cited as a whole file"},
		{"probe:docs/findings/order.md#No such heading", "name-not-found", "no heading"},
		{"test:ai/gone_test.go#TestAwaited", "missing-file", "ai/gone_test.go"},
		{"scenario:test/parity/scenarios/rpc/99-gone.toml", "missing-file", "99-gone.toml"},
		// (d) An entry the checker cannot resolve is an error that names its kind.
		{"test:ai/async.go#TestAwaited", "unresolvable-kind", `test evidence in a ".go" file that is not a test file`},
		{"test:durable/host.ts#host", "unresolvable-kind", `test evidence in a ".ts" file that is not a test file`},
		{"test:docs/findings/order.md#Arrival order", "unresolvable-kind", `test evidence in a ".md" file`},
		{"test:ai", "unresolvable-kind", "test evidence in a directory"},
		{"probe:ai/async_test.go#TestAwaited", "unresolvable-kind", `probe evidence in a ".go" file`},
		{"scenario:docs/findings/order.md", "unresolvable-kind", `scenario evidence in a ".md" file`},
		{"scenario:ai/01-order.toml", "unresolvable-kind", "outside test/parity/scenarios"},
		{"bench:ai/async_test.go#BenchmarkAwaited", "unresolvable-kind", `evidence kind "bench"`},
		{"ai/async_test.go#TestAwaited", "invalid-evidence", "is not kind:path"},
	} {
		t.Run(test.evidence, func(t *testing.T) {
			problems := evidenceProblems(t, test.evidence)
			if len(problems) != 1 || problems[0].Class != test.class || !strings.Contains(problems[0].Text, test.text) || !strings.Contains(problems[0].Text, evidenceRow) {
				t.Fatalf("problems = %q, want one [%s] problem of %s containing %q", problemLines(problems), test.class, evidenceRow, test.text)
			}
		})
	}
}

// The gate prints every violation with its class and ends with the count per class.
func TestReportCountsViolationsPerClass(t *testing.T) {
	got := report([]problem{{"name-not-found", "a.ts evidence x"}, {"skipped-evidence", "b.ts evidence y"}, {"name-not-found", "c.ts evidence z"}})
	want := "async-contracts: [name-not-found] a.ts evidence x\n" +
		"async-contracts: [skipped-evidence] b.ts evidence y\n" +
		"async-contracts: [name-not-found] c.ts evidence z\n" +
		"async-contracts: 3 violations: name-not-found=2 skipped-evidence=1\n"
	if got != want {
		t.Fatalf("report printed\n%s\nwant\n%s", got, want)
	}
}

func writeAsyncTestFile(t *testing.T, root, relativePath, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
