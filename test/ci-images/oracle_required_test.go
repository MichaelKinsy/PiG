// SPDX-License-Identifier: MIT

package ciimages

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// oracleBuiltFromSource names the jobs of ci.yml that install the Pi oracle with `pin.py install-oracle` and never mirror the pinned source with `make upstream-mirror`. They do not set PIG_REQUIRE_PI_ORACLE: the published pi-ai data that TestPublishedModelDataValidates and the catalog tests read is not installed there, so their skips stay skips.
var oracleBuiltFromSource = map[string]string{
	"windows": "builds Pi from source with install-oracle; no published pi-ai package and no upstream mirror step",
	"macos":   "builds Pi from source with install-oracle; no published pi-ai package and no upstream mirror step",
}

// ciJobs splits ci.yml into its jobs: job key -> the lines of the job.
func ciJobs(t *testing.T) map[string][]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	key := regexp.MustCompile(`^  ([a-z0-9-]+):\s*$`)
	jobs := map[string][]string{}
	cur := ""
	inJobs := false
	for line := range strings.SplitSeq(string(data), "\n") {
		if line == "jobs:" {
			inJobs = true
			continue
		}
		if !inJobs {
			continue
		}
		if m := key.FindStringSubmatch(line); m != nil {
			cur = m[1]
		}
		if cur != "" && !strings.HasPrefix(strings.TrimSpace(line), "#") {
			jobs[cur] = append(jobs[cur], line)
		}
	}
	return jobs
}

// A job that installs the Pi oracle must say so to the tests, so that a test that cannot find its Pi source fails instead of skipping: a new job that mirrors the source without setting PIG_REQUIRE_PI_ORACLE fails here.
func TestEveryJobThatInstallsThePiOracleRequiresIt(t *testing.T) {
	jobs := ciJobs(t)
	if len(jobs) < 10 {
		t.Fatalf("ci.yml has %d jobs, want the workflow's jobs", len(jobs))
	}
	seen := 0
	for name, lines := range jobs {
		text := strings.Join(lines, "\n")
		mirrors := strings.Contains(text, "make upstream-mirror")
		builds := strings.Contains(text, "pin.py install-oracle")
		if !mirrors && !builds {
			continue
		}
		seen++
		if why, ok := oracleBuiltFromSource[name]; ok {
			if mirrors {
				t.Errorf("job %s now mirrors the Pi source (%s is stale): remove it from oracleBuiltFromSource and set PIG_REQUIRE_PI_ORACLE", name, why)
			}
			continue
		}
		// The job-level env sits above the steps.
		head := text
		if i := strings.Index(head, "\n    steps:"); i >= 0 {
			head = head[:i]
		}
		if !regexp.MustCompile(`(?m)^      PIG_REQUIRE_PI_ORACLE: "1"$`).MatchString(head) {
			t.Errorf("job %s installs the Pi oracle but does not set PIG_REQUIRE_PI_ORACLE: \"1\" in its job env: a test that cannot find its Pi source would skip there and pass", name)
		}
	}
	if seen < 4 {
		t.Fatalf("only %d jobs install the Pi oracle; the scan reads the workflow wrongly", seen)
	}
	for name := range oracleBuiltFromSource {
		if _, ok := jobs[name]; !ok {
			t.Errorf("oracleBuiltFromSource names job %s, which ci.yml does not have", name)
		}
	}
}

// make model-data-check is the named gate of TestPublishedModelDataValidates: make check reaches it, it sets the variable, it fails when the test did not pass, and the one opt-out is printed and set by no gate.
func TestModelDataCheckIsAGateThatFailsWithoutTheOracle(t *testing.T) {
	root := repoRoot(t)
	makefile := readMakeSources(t, root)
	core := regexp.MustCompile(`(?m)^check-core:([^\n]*)$`).FindStringSubmatch(makefile)
	if core == nil || !regexp.MustCompile(`(^|\s)model-data-check(\s|$)`).MatchString(core[1]) {
		t.Errorf("check-core does not run model-data-check: make check would not reach the published model data")
	}
	recipe := regexp.MustCompile(`(?m)^model-data-check:[^\n]*\n(?:\t[^\n]*\n)+`).FindString(makefile)
	for _, want := range []string{"PIG_REQUIRE_PI_ORACLE=1", "TestPublishedModelDataValidates", "--- PASS: TestPublishedModelDataValidates", "--- SKIP", "MODEL_DATA_ORACLE", "NOT checked"} {
		if !strings.Contains(recipe, want) {
			t.Errorf("the model-data-check recipe lacks %q", want)
		}
	}
	for _, dir := range []string{".github/workflows", "automation"} {
		_ = filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if data, err := os.ReadFile(path); err == nil && strings.Contains(string(data), "MODEL_DATA_ORACLE=skip") && !strings.HasSuffix(path, ".mk") {
				t.Errorf("%s sets MODEL_DATA_ORACLE=skip: no gate may opt out of the model-data check", path)
			}
			return nil
		})
	}
}

// The tests that skip when the Pi oracle's source or published data is absent end through pioracle.Missing. A copy of the old skip would let the test pass in a gate that installed the oracle.
func TestOracleDependentTestsEndThroughPioracleMissing(t *testing.T) {
	root := repoRoot(t)
	for _, file := range []string{
		"cmd/check-model-data/main_published_test.go",
		"ai/catalog_oracle_data_test.go",
		"ai/registry_test.go",
		"test/docs-drift/adapted_headings_test.go",
		"test/parity/upstreampackages/upstreampackages_test.go",
		"test/parity/interface-closure/autobind/reach_libraries_test.go",
		"test/upstream-parity/parser_test.go",
	} {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "pioracle.Missing(") {
			t.Errorf("%s does not end through pioracle.Missing when its Pi source is absent", file)
		}
	}
}
