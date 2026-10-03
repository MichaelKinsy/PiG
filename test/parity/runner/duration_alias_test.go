//go:build parity

package runner

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// bash.ts:392 stores the command's measured wall clock rounded to 0.1 s, so two runs of one command
// report 0 or 0.1 depending on host load. The duration alias keeps the field's presence and Pi's one-decimal number spelling.
func TestJSONDurationAliasRetainsPresenceAndType(t *testing.T) {
	rules := []JSONAliasRule{{Paths: []string{"/**/wall_time_seconds"}, Kind: "duration", Reason: "Measured wall clock."}}
	record := func(value string) Result {
		return Result{Output: `{"type":"tool_execution_end","result":{"structuredContent":{"exit_code":3,"wall_time_seconds":` + value + `}}}`}
	}
	for _, tc := range []struct {
		name, pig, pi string
		equal         bool
	}{
		{"rounding step", "0", "0.1", true},
		{"slow run", "12.3", "0", true},
		{"same value", "0.2", "0.2", true},
		{"string", `"0"`, "0", false},
		{"null", "null", "0", false},
		{"negative", "-0.1", "0", false},
		{"boolean", "false", "0", false},
		{"unrounded", "0.123", "0.1", false},
		{"trailing zero", "0.10", "0.2", false},
		{"exponent", "1e-1", "0.2", false},
		{"negative zero", "-0", "0.1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := compareJSONResults(record(tc.pig), record(tc.pi), rules)
			if (err == nil) != tc.equal {
				t.Fatalf("equal=%t: %v", tc.equal, err)
			}
		})
	}
	absent := Result{Output: `{"type":"tool_execution_end","result":{"structuredContent":{"exit_code":3}}}`}
	if compareJSONResults(record("0"), absent, rules) == nil {
		t.Fatal("a missing duration field was accepted")
	}
	other := Result{Output: strings.Replace(record("0").Output, `"exit_code":3`, `"exit_code":4`, 1)}
	if compareJSONResults(record("0.1"), other, rules) == nil {
		t.Fatal("the alias hid a different exit code")
	}
	if err := compareJSONResults(record("0"), record("0"), []JSONAliasRule{{Paths: []string{"/**/wall_time_seconds"}, Kind: "duration"}}); err == nil {
		t.Fatal("an alias without a reason was accepted")
	}
}

// fauxBashPrompts returns the prompts for which the shared faux provider answers with a bash tool call.
func fauxBashPrompts(t *testing.T, root string) []string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join(root, "test/parity/testdata/test-faux-provider.ts"))
	if err != nil {
		t.Fatal(err)
	}
	includes := regexp.MustCompile(`lastText\.includes\("([^"]+)"\)`)
	var prompts []string
	for branch := range strings.SplitSeq(string(source), "\n  if (") {
		head, body, _ := strings.Cut(branch, "\n")
		if !strings.Contains(body, `toolName: "bash"`) {
			continue
		}
		for _, match := range includes.FindAllStringSubmatch(head, -1) {
			prompts = append(prompts, match[1])
		}
	}
	if len(prompts) == 0 {
		t.Fatal("no faux bash prompt found; the provider's branch layout changed")
	}
	return prompts
}

var excludesBash = regexp.MustCompile(`"--exclude-tools",\s*"bash"`)

// A full-record JSON comparison of a faux bash run carries bash's measured duration; each one must alias it.
func TestJSONScenariosAliasMeasuredDurations(t *testing.T) {
	root, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	prompts := fauxBashPrompts(t, root)
	scenarios, err := DiscoverScenarios(filepath.Join(root, "test/parity", "scenarios"))
	if err != nil {
		t.Fatal(err)
	}
	record := func(value string) Result {
		return Result{Output: `{"type":"tool_execution_end","result":{"structuredContent":{"exit_code":0,"wall_time_seconds":` + value + `}}}`}
	}
	// tools/17 drives bash through its own OpenAI-compatible server (tool-rpc-wire.py), not the faux provider.
	scriptDriven := []string{"17-tool-rpc-error-and-bash-wire"}
	checked := 0
	for _, sc := range scenarios {
		if !sc.Assert.JSONOutputEqual {
			continue
		}
		text, err := os.ReadFile(sc.SourcePath)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(scriptDriven, sc.Name) && !slices.ContainsFunc(prompts, func(prompt string) bool { return strings.Contains(string(text), prompt) }) {
			continue
		}
		// --exclude-tools bash leaves the faux call unresolved ("Tool bash not found"), so no duration is emitted.
		if excludesBash.Match(text) {
			continue
		}
		checked++
		if err := compareJSONResults(record("0"), record("0.1"), sc.Assert.JSONAliases); err != nil {
			t.Errorf("%s: %v", sc.Name, err)
		}
	}
	// tools/17, rpc/26 and rpc/41 compare complete records of a faux bash run.
	if checked < 3 {
		t.Fatalf("found %d full-record scenarios running faux bash; the discovery no longer sees the known ones", checked)
	}
}

var measuredDurationLiteral = regexp.MustCompile(`wall_time_seconds"?:\s*[0-9]`)

// A substring or regex that spells a measured duration's value passes on one host load and fails on another.
func TestScenarioAssertionsDoNotPinMeasuredDurations(t *testing.T) {
	root, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	scenarios, err := DiscoverScenarios(filepath.Join(root, "test/parity", "scenarios"))
	if err != nil {
		t.Fatal(err)
	}
	for _, sc := range scenarios {
		var expectations []string
		expectations = append(expectations, sc.Assert.BothContain...)
		expectations = append(expectations, sc.Assert.PigContains...)
		expectations = append(expectations, sc.Assert.PiContains...)
		expectations = append(expectations, sc.Diverge.PigContains...)
		expectations = append(expectations, sc.Diverge.PiContains...)
		for _, expectation := range expectations {
			if measuredDurationLiteral.MatchString(expectation) {
				t.Errorf("%s: expectation %q pins a measured duration; match its number shape with both_match_regex", sc.Name, expectation)
			}
		}
		for _, pattern := range sc.Assert.BothMatchRegex {
			if strings.Contains(pattern, "wall_time_seconds") && !strings.Contains(pattern, `[0-9]+(\.[0-9])?`) {
				t.Errorf("%s: regex %q must accept bash's 0.1 s rounding steps with [0-9]+(\\.[0-9])?", sc.Name, pattern)
			}
		}
	}
}
