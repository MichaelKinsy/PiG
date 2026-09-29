//go:build parity

package runner

import (
	"fmt"
	"strings"
	"testing"
)

func BenchmarkJSONCompareRecords(b *testing.B) {
	var records strings.Builder
	for i := range 1000 {
		fmt.Fprintf(&records, "{\"type\":\"entry_appended\",\"entry\":{\"id\":\"entry-%d\",\"parentId\":\"entry-%d\",\"timestamp\":1790000000000,\"content\":[{\"type\":\"text\",\"text\":\"two  spaces\\n\"}]}}\n", i, i-1)
	}
	result := Result{Output: records.String()}
	rules := []JSONAliasRule{{Paths: []string{"/**/id", "/**/parentId"}, Kind: "id", Group: "entry", Reason: "Benchmark generated identities."}, {Paths: []string{"/**/timestamp"}, Kind: "timestamp", Reason: "Benchmark wall clocks."}}
	b.ReportAllocs()
	b.SetBytes(int64(len(result.Output)))
	for b.Loop() {
		if err := compareJSONResults(result, result, rules); err != nil {
			b.Fatal(err)
		}
	}
}

func TestJSONComparisonPreservesCompleteRecords(t *testing.T) {
	reference := `{"type":"tool_execution_end","isError":false,"result":{"content":[{"type":"text","text":"two  spaces\n"}],"details":{}},"n":9007199254740993}`
	for _, tc := range []struct {
		name, other string
		equal       bool
	}{
		{"object order", `{"n":9007199254740993,"result":{"details":{},"content":[{"text":"two  spaces\n","type":"text"}]},"isError":false,"type":"tool_execution_end"}`, true},
		{"number spelling", strings.Replace(reference, "9007199254740993", "9007199254740993.0", 1), true},
		{"extra nested field", strings.Replace(reference, `"details":{}`, `"details":{},"isError":false`, 1), false},
		{"null vs omitted", strings.Replace(reference, `,"details":{}`, `,"details":null`, 1), false},
		{"missing", strings.Replace(reference, `,"details":{}`, "", 1), false},
		{"boolean vs zero", strings.Replace(reference, `false`, `0`, 1), false},
		{"array vs null", strings.Replace(reference, `[{"type":"text","text":"two  spaces\n"}]`, `null`, 1), false},
		{"whitespace", strings.Replace(reference, "two  spaces", "two spaces", 1), false},
		{"precision", strings.Replace(reference, "9007199254740993", "9007199254740992", 1), false},
		{"extra record", reference + "\n{}", false},
		{"duplicate key", strings.Replace(reference, `"isError":false`, `"isError":true,"isError":false`, 1), false},
		{"invalid", reference + "\n\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := compareJSONResults(Result{Output: reference}, Result{Output: tc.other}, nil)
			if (err == nil) != tc.equal {
				t.Fatalf("equal=%t: %v", tc.equal, err)
			}
		})
	}
	if compareJSONResults(Result{Output: "{\"n\":1}\n{\"n\":2}"}, Result{Output: "{\"n\":2}\n{\"n\":1}"}, nil) == nil {
		t.Fatal("event order lost")
	}
}

func TestJSONIdentityAliasesPreserveReferencesAndPresence(t *testing.T) {
	rules := []JSONAliasRule{{Paths: []string{"/**/id", "/**/parentId"}, Kind: "id", Group: "entries", Reason: "Independent generated entry identities; references share the same bijection."}}
	left := Result{Output: "{\"id\":\"a\",\"parentId\":null}\n{\"id\":\"b\",\"parentId\":\"a\"}"}
	for _, tc := range []struct {
		other string
		equal bool
	}{
		{"{\"id\":\"x\",\"parentId\":null}\n{\"id\":\"y\",\"parentId\":\"x\"}", true},
		{"{\"id\":\"x\",\"parentId\":null}\n{\"id\":\"y\",\"parentId\":\"y\"}", false},
		{"{\"id\":\"x\",\"parentId\":null}\n{\"id\":\"x\",\"parentId\":\"x\"}", false},
		{"{\"id\":\"x\"}\n{\"id\":\"y\",\"parentId\":\"x\"}", false},
	} {
		err := compareJSONResults(left, Result{Output: tc.other}, rules)
		if (err == nil) != tc.equal {
			t.Fatalf("%s: %v", tc.other, err)
		}
	}
}

func TestJSONPathAliasesRetainSuffixAndText(t *testing.T) {
	rules := []JSONAliasRule{{Paths: []string{"/*/path"}, Kind: "path", Reason: "D2: only the exact isolated temporary root differs."}}
	left := Result{Output: `{"path":"/owned/a/session.html","text":"PiG two  spaces"}`, IdentityRoots: map[string]string{"temp": "/owned/a"}}
	right := Result{Output: `{"path":"/owned/b/session.html","text":"PiG two  spaces"}`, IdentityRoots: map[string]string{"temp": "/owned/b"}}
	if err := compareJSONResults(left, right, rules); err != nil {
		t.Fatal(err)
	}
	right.Output = strings.Replace(right.Output, "session.html", "wrong.html", 1)
	if compareJSONResults(left, right, rules) == nil {
		t.Fatal("wrong destination hidden")
	}
	right.Output = `{"path":"<temp>/session.html","text":"PiG two  spaces"}`
	if compareJSONResults(left, right, rules) == nil {
		t.Fatal("literal alias token impersonated a real path")
	}
	right.Output = `{"path":"/owned/bad/session.html","text":"PiG two  spaces"}`
	if compareJSONResults(left, right, rules) == nil {
		t.Fatal("prefix boundary hidden")
	}
}

func TestStderrComparisonDoesNotAcceptMissingCapture(t *testing.T) {
	for _, pi := range []Result{{}, {StderrCaptured: true}} {
		o := &ScenarioOutcome{Scenario: &Scenario{Assert: AssertSpec{StderrEqual: true}}, Pig: SystemResults{Runs: []Result{{Stderr: "diagnostic\n", StderrCaptured: true}}}, Pi: SystemResults{Runs: []Result{pi}}}
		EvaluateOutcome(o)
		if o.Passed() {
			t.Fatal("stderr was excluded")
		}
	}
}
