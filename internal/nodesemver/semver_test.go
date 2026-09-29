package nodesemver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Pi package-manager.ts:59-65,1473-1478 keeps an installed package when validRange rejects the selector. node-semver 7.8.5 rejects Go-constraint syntax (!= and comma conjunctions) and ANDs space-separated comparators; these results were read from it.
func TestValidRangeRejectsNonNpmConstraintSyntax(t *testing.T) {
	for _, tc := range []struct {
		selector, want string
		valid          bool
	}{
		{"!=1.0.0", "", false},
		{">=1.0.0,<2.0.0", "", false},
		{">=1.0.0, <2.0.0", "", false},
		{"1.0.0 +b - 2.0.0", "", false},
		{"latest", "", false},
		{">=1.0.0 <2.0.0", ">=1.0.0 <2.0.0", true},
		{">=1.0.0\t<2.0.0", ">=1.0.0 <2.0.0", true},
		{">1.2", ">=1.3.0", true},
		{"<=1.2", "<1.3.0-0", true},
		{"1.2.3 +build <2", "1.2.3 <2.0.0-0", true},
		{"1.x || >=2.5.0 || 5.0.0 - 7.2.3", ">=1.0.0 <2.0.0-0||>=2.5.0||>=5.0.0 <=7.2.3", true},
		{"*", "*", true},
	} {
		got, valid := ValidRange(tc.selector)
		if got != tc.want || valid != tc.valid {
			t.Errorf("ValidRange(%q) = %q, %v; want %q, %v", tc.selector, got, valid, tc.want, tc.valid)
		}
	}
	for _, tc := range []struct {
		version, selector string
		want              bool
	}{
		{"1.0.0", ">=1.0.0 <2.0.0", true},
		{"2.0.0", ">=1.0.0 <2.0.0", false},
		{"1.2.5", ">=1.3.0", false},
		{"1.2.9", "<1.3.0-0", true},
		{"1.2.4-alpha", ">=1.2.3-beta.1", false},
		{"1.0.0-beta", "*", false},
	} {
		if got := Satisfies(tc.version, tc.selector); got != tc.want {
			t.Errorf("Satisfies(%q, %q) = %v, want %v", tc.version, tc.selector, got, tc.want)
		}
	}
}

func oracleRanges() []string {
	var ranges []string
	operands := []string{"*", "x", "X", "1", "1.x", "1.X.x", "1.2", "1.2.*", "1.2.3", "0", "0.x", "0.0", "0.0.x", "0.0.1", "0.1.2", "0.0.0", "1.2.3-beta.1", "0.0.1-rc.0", "0.1.2-0", "1.x.3", "x.1", "1.2.3+build.7", "01.2.3", "1.2.3-01", "9007199254740991.0.0", "9007199254740990.0.0", "1.9007199254740991", "1.2.9007199254740991", "1.2.3-alpha.9007199254740992", "latest"}
	for _, operator := range []string{"", "=", "<", ">", "<=", ">=", "~", "~>", "^", "v", "> ", ">= ", "~ ", "^ ", "!=", "=="} {
		for _, operand := range operands {
			ranges = append(ranges, operator+operand)
		}
	}
	ends := []string{"*", "1", "1.2", "1.2.3", "1.2.3-beta.1", "2", "2.x", "2.3", "2.3.4", "2.3.4-rc.1", "v2.3.4"}
	for _, from := range ends {
		for _, to := range ends {
			ranges = append(ranges, from+" - "+to)
		}
	}
	return append(ranges,
		"!=1.0.0", ">=1.0.0,<2.0.0", ">=1.0.0, <2.0.0", "1.0.0,2.0.0", "~1.2.3,<1.2.5", "=>1.0.0", "=<1.0.0",
		">=1.0.0 <2.0.0", ">=1.0.0\t<2.0.0", " >=1.0.0   <2.0.0 ", "> 1.0.0 < 2.0.0", ">= 1.2.3 <= 2.0.0", "1.2.3 2.3.4", ">1.0.0 <1.0.0", "<1.0.0 >2.0.0", ">=1.0.0 >=1.0.0 <2.0.0",
		"^1.0.0 || ^2.0.0", "1.x || >=2.5.0 || 5.0.0 - 7.2.3", "||", "1.0.0 ||", "|| 1.0.0", "* || 1.0.0", "<0.0.0-0 || 1.0.0", ">x || <x", ">x || 1.0.0", "1.0.0||2.0.0", "1.0.0 | 2.0.0", "1.0.0 ||| 2.0.0",
		"1.2.3 +build <2", "1.0.0 +b - 2.0.0", "^1.2.3+build", "1.2.3+b.1 - 2.0.0+c", "1.2.3 + 2",
		"**", ">=*", "<*", "*.*", "*.1", "* *", "x x", "* 1.2.3", ">=0.0.0", ">= 0.0.0", ">=0.0.0-0", "<0.0.0-0", "<0.0.0", ">=0.0.0 <1.0.0",
		"~>1.2", "~> 1.2", "~ >1.2", "~1.2.3-beta.2", "~0", "~ 1", "^ 1", "^ ^1", "~~1", "^0.0.1-beta", "^0.1.2-beta", "^1.2.x", "^0.0.x", "^0.x",
		"\u00a0^1.0.0\ufeff", "^1.0.0\u00a0<1.5.0", ">=1.0.0\v<2.0.0", "1.0.0 -  2.0.0", "1.0.0  - 2.0.0", "1.0.0-2.0.0", "1.0.0\u2003-\u20032.0.0",
		"", " ", "beta", "next-1", "v", "=", ">", "<=", "1.2.3.4", "1.2.3-", "1.2.3-+b", "a.b.c", "1.2.3-alpha..1", "=1.2.3", "== 1.2.3", "v1.2.3 - v2.3.4", "=1.2.3 - 2.0.0", "1 - 2 - 3", "v 1.2.3", "=v1.2.3",
		strings.Repeat("9", 20)+".0.0", "^"+strings.Repeat("9", 17), "1.2.3-"+strings.Repeat("a", 250), "1.2.3-"+strings.Repeat("a", 251), "1.2.3+"+strings.Repeat("b", 260), "<=1."+strings.Repeat("1", 257), ">1."+strings.Repeat("1", 256), "~9007199254740990.9007199254740990", "^9007199254740990",
		"1.2.x-"+strings.Repeat("a", 250), "1.2.x-"+strings.Repeat("a", 251), "1.2.x-"+strings.Repeat("a", 252), "^1.2.x-"+strings.Repeat("a", 300), "~1.x.x-"+strings.Repeat("a", 300),
		"1.2.x-"+strings.Repeat("1", 256), "1.2.x-"+strings.Repeat("1", 257), "1.2.x-"+strings.Repeat("1", 258), ">=1.2.x-"+strings.Repeat("7", 300), "1.2.x-a"+strings.Repeat("1", 256), "1.2.x-a"+strings.Repeat("1", 257),
	)
}

func oracleVersions() []string {
	return []string{
		"0.0.0", "0.0.1", "0.0.2", "0.1.0", "0.1.2", "0.1.2-0", "0.2.0", "1.0.0", "1.0.0-0", "1.0.0-rc.1", "1.0.0-rc.9", "1.0.0-rc.10", "1.0.0-rc.1a",
		"1.2.0", "1.2.2", "1.2.3", "1.2.3-alpha", "1.2.3-beta.1", "1.2.3-beta.2", "1.2.4", "1.2.4-alpha", "1.3.0", "1.3.0-0", "1.5.0",
		"2.0.0", "2.0.0-0", "2.0.0-beta.1", "2.3.4", "2.3.4-rc.1", "2.3.5", "2.5.0", "3.0.0", "6.0.0",
		"v1.2.3", " 1.2.3 ", "\u00a01.2.3\ufeff", "\u00851.2.3", "=1.2.3", "1.2", "01.2.3", "1.2.3+build.5", "1.2.3-01",
		"9007199254740990.0.0", "9007199254740991.0.0", "9007199254740992.0.0", "1.2.3-alpha.9007199254740991", "1.2.3-alpha.9007199254740992",
		"1.2.3-alpha.99999999999999999999", "1.2.3-alpha.99999999999999999998", "", "latest", "1.2.3-" + strings.Repeat("a", 250), "1.2.3-" + strings.Repeat("a", 260),
	}
}

type oracleRange struct {
	Valid     *string `json:"valid"`
	Satisfies []bool  `json:"satisfies"`
	Direct    []bool  `json:"direct"`
	Max       *string `json:"max"`
}

type oracleResult struct {
	Semver  string        `json:"semver"`
	Valid   []*string     `json:"valid"`
	Compare [][]string    `json:"compare"`
	Ranges  []oracleRange `json:"ranges"`
}

func optional(value string, ok bool) *string {
	if !ok {
		return nil
	}
	return &value
}

func show(value *string) string {
	if value == nil {
		return "null"
	}
	return strconv.Quote(*value)
}

// Every Pi call (valid, compare as used by gt and rcompare, validRange, satisfies on the validated range as Pi composes it, satisfies on the raw selector, and maxSatisfying) returns what Pi's own locked semver returns across operators, partial versions, prereleases, hyphen ranges, unions, whitespace, build metadata and numeric bounds.
func TestMatchesPinnedNodeSemver(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required for the semver oracle: %v", err)
	}
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	piPackage := filepath.Join(repo, "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent", "package.json")
	if _, err := os.Stat(piPackage); err != nil {
		t.Fatalf("pinned Pi package missing (run python automation/ci/npm-locked.py extensions/sdk-ts): %v", err)
	}
	upstreamManifest, err := os.ReadFile(filepath.Join(repo, ".upstream", "current", "packages", "coding-agent", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var upstream struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal(upstreamManifest, &upstream); err != nil || upstream.Dependencies["semver"] == "" {
		t.Fatalf("upstream semver dependency: %v %v", upstream.Dependencies, err)
	}
	ranges, versions := oracleRanges(), oracleVersions()
	input, err := json.Marshal(map[string][]string{"ranges": ranges, "versions": versions})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "--input-type=module", "--eval", `
import { createRequire } from "node:module";
import { readFileSync } from "node:fs";
const require = createRequire(process.argv[1]);
const semver = require("semver");
const input = JSON.parse(readFileSync(0, "utf8"));
const attempt = (f) => { try { return String(f()); } catch { return "throw"; } };
process.stdout.write(JSON.stringify({
  semver: require("semver/package.json").version,
  valid: input.versions.map((v) => semver.valid(v)),
  compare: input.versions.map((a) => input.versions.map((b) => attempt(() => semver.compare(a, b)))),
  ranges: input.ranges.map((r) => {
    const valid = semver.validRange(r);
    return {
      valid,
      satisfies: valid === null ? null : input.versions.map((v) => semver.satisfies(v, valid)),
      direct: input.versions.map((v) => semver.satisfies(v, r)),
      max: valid === null ? null : semver.maxSatisfying(input.versions, valid),
    };
  }),
}));`, piPackage)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("node semver oracle: %v\n%s", err, stderr.String())
	}
	var want oracleResult
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	if want.Semver != upstream.Dependencies["semver"] {
		t.Fatalf("oracle semver %s, Pi pins %s", want.Semver, upstream.Dependencies["semver"])
	}
	if len(want.Valid) != len(versions) || len(want.Compare) != len(versions) || len(want.Ranges) != len(ranges) {
		t.Fatalf("oracle shape: %d valid, %d compare, %d ranges", len(want.Valid), len(want.Compare), len(want.Ranges))
	}
	var diffs []string
	parsed := make([]*SemVer, len(versions))
	for i, version := range versions {
		v, err := Parse(version)
		var got *string
		if err == nil {
			parsed[i], got = v, &v.version
		}
		if show(got) != show(want.Valid[i]) {
			diffs = append(diffs, fmt.Sprintf("valid(%q) = %s, want %s", version, show(got), show(want.Valid[i])))
		}
	}
	for i, a := range versions {
		for j, b := range versions {
			got := "throw"
			if parsed[i] != nil && parsed[j] != nil {
				got = strconv.Itoa(parsed[i].Compare(parsed[j]))
			}
			if got != want.Compare[i][j] {
				diffs = append(diffs, fmt.Sprintf("compare(%q, %q) = %s, want %s", a, b, got, want.Compare[i][j]))
			}
		}
	}
	for i, r := range ranges {
		expected := want.Ranges[i]
		valid := optional(ValidRange(r))
		if show(valid) != show(expected.Valid) {
			diffs = append(diffs, fmt.Sprintf("validRange(%q) = %s, want %s", r, show(valid), show(expected.Valid)))
			continue
		}
		// Satisfies parses its range on every call; one parse per range keeps the corpus fast.
		direct, directErr := newRange(r)
		for j, version := range versions {
			if got := directErr == nil && direct.Test(version); got != expected.Direct[j] {
				diffs = append(diffs, fmt.Sprintf("satisfies(%q, %q) = %v, want %v", version, r, got, expected.Direct[j]))
			}
		}
		if valid == nil {
			continue
		}
		composed, composedErr := newRange(*valid)
		for j, version := range versions {
			if got := composedErr == nil && composed.Test(version); got != expected.Satisfies[j] {
				diffs = append(diffs, fmt.Sprintf("satisfies(%q, validRange(%q)=%q) = %v, want %v", version, r, *valid, got, expected.Satisfies[j]))
			}
		}
		if got := optional(MaxSatisfying(versions, *valid)); show(got) != show(expected.Max) {
			diffs = append(diffs, fmt.Sprintf("maxSatisfying(versions, %q) = %s, want %s", *valid, show(got), show(expected.Max)))
		}
	}
	if len(diffs) > 0 {
		if len(diffs) > 40 {
			diffs = append(diffs[:40], fmt.Sprintf("... %d more", len(diffs)-40))
		}
		t.Fatalf("%d ranges x %d versions differ from node-semver %s:\n%s", len(ranges), len(versions), want.Semver, strings.Join(diffs, "\n"))
	}
}

func BenchmarkSatisfiesComparatorSet(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if !Satisfies("1.2.3", ">=1.0.0 <2.0.0-0") {
			b.Fatal("1.2.3 must satisfy the comparator set")
		}
	}
}
