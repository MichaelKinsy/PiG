package nodesemver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"strings"
	"testing"
)

// pi: the `semver` package Pi's package manager calls (satisfies, validRange, maxSatisfying, parse) from the installed pi-coding-agent dependencies.
func TestSemverMatchesNodeSemver(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatal("node is required: ", err)
	}
	rng := rand.New(rand.NewSource(20261010))
	pick := func(xs []string) string { return xs[rng.Intn(len(xs))] }
	num := []string{"0", "1", "2", "3", "10", "12", "999", "x", "X", "*", "01", "9007199254740993"}
	pre := []string{"", "", "", "-alpha", "-alpha.1", "-beta.2", "-rc.1", "-0", "-1.2", "-a.b-c", "+build", "-rc.1+b5", "-01", "-"}
	version := func() string {
		return pick([]string{"", "", "v", "=", " "}) + pick(num[:5]) + "." + pick(num[:5]) + "." + pick(num[:5]) + pick(pre)
	}
	var versions []string
	for range 60 {
		versions = append(versions, version())
	}
	versions = append(versions, "1.2", "1", "a.b.c", "1.2.3.4", "1.2.3-", "", " 1.2.3 ", "1.2.3\n", "01.2.3", "1.02.3", "9007199254740993.0.0")
	ops := []string{"", "", "^", "~", ">", ">=", "<", "<=", "=", "~>", "v", ">=v"}
	partial := func() string {
		wild := num[:5]
		if rng.Intn(4) == 0 {
			wild = num
		}
		parts := []string{pick(wild)}
		for range rng.Intn(3) {
			parts = append(parts, pick(wild))
		}
		suffix := ""
		if rng.Intn(6) == 0 {
			suffix = pick(pre)
		}
		return strings.Join(parts, ".") + suffix
	}
	var ranges []string
	for range 400 {
		var set []string
		for range 1 + rng.Intn(3) {
			var comps []string
			for range 1 + rng.Intn(3) {
				comps = append(comps, pick(ops)+partial())
			}
			switch rng.Intn(6) {
			case 0:
				comps = []string{partial() + " - " + partial()}
			case 1:
				comps = append(comps, comps[0])
			}
			set = append(set, strings.Join(comps, pick([]string{" ", " ", "  ", ", "})))
		}
		ranges = append(ranges, strings.Join(set, pick([]string{" || ", "||", " ||  "})))
	}
	ranges = append(ranges, "", "*", "x", "latest", "next", "1.x || >=2.5.0 || 5.0.0 - 7.2.3", "^0.0.1", "^0.1", "~0", ">=1.0.0 <2.0.0-0", "1.2.3 - 2", "<1.2.3 || >2", "^1.2.3-beta.2", ">1 <1", "||", "1.2.3 ||", ">=", "^", "~>1", "1.*", "v1.2.3", "=1.2.3", "01.2.3")
	in, _ := json.Marshal(map[string]any{"versions": versions, "ranges": ranges})
	cmd := exec.CommandContext(t.Context(), "node", "testdata/semver.mjs")
	cmd.Stdin = bytes.NewReader(in)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, stderr.String())
	}
	var want struct {
		Parse         []any
		Compare       []any
		ValidRange    []any
		Satisfies     [][]any
		MaxSatisfying []any
	}
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	var differences []string
	diff := func(format string, args ...any) { differences = append(differences, fmt.Sprintf(format, args...)) }
	for i, v := range versions {
		parsed, err := Parse(v)
		switch w := want.Parse[i].(type) {
		case string:
			if err == nil || !strings.HasPrefix(w, "throws") {
				if err != nil || parsed.version != w {
					diff("Parse(%q): Go %v %v, semver %q", v, parsed, err, w)
				}
			}
		case nil:
			if err == nil {
				diff("Parse(%q) = %s, semver null", v, parsed.version)
			}
		}
		next, nextErr := Parse(versions[(i+1)%len(versions)])
		if c, ok := want.Compare[i].(float64); ok && err == nil && nextErr == nil && parsed.Compare(next) != int(c) {
			diff("Compare(%q, %q) = %d, semver %v", v, versions[(i+1)%len(versions)], parsed.Compare(next), c)
		}
	}
	valid, satisfied := 0, 0
	for i, r := range ranges {
		got, ok := ValidRange(r)
		switch w := want.ValidRange[i].(type) {
		case string:
			valid++
			if !ok || got != w {
				diff("ValidRange(%q) = %q %v, semver %q", r, got, ok, w)
			}
		case nil:
			if ok {
				diff("ValidRange(%q) = %q, semver null", r, got)
			}
		}
		for j, v := range versions {
			w, _ := want.Satisfies[i][j].(bool)
			if w {
				satisfied++
			}
			if _, isString := want.Satisfies[i][j].(string); isString {
				continue
			}
			if got := Satisfies(v, r); got != w {
				diff("Satisfies(%q, %q) = %v, semver %v", v, r, got, w)
			}
		}
		best, ok := MaxSatisfying(versions, r)
		switch w := want.MaxSatisfying[i].(type) {
		case string:
			if !strings.HasPrefix(w, "throws") && (!ok || best != w) {
				diff("MaxSatisfying(%q) = %q %v, semver %q", r, best, ok, w)
			}
		case nil:
			if ok {
				diff("MaxSatisfying(%q) = %q, semver null", r, best)
			}
		}
	}
	if valid < len(ranges)/3 || satisfied < 500 {
		t.Fatalf("corpus is too weak: %d valid ranges of %d, %d satisfied pairs", valid, len(ranges), satisfied)
	}
	if len(differences) > 0 {
		shown := differences
		if len(shown) > 15 {
			shown = shown[:15]
		}
		t.Errorf("%d differences from semver:\n%s", len(differences), strings.Join(shown, "\n"))
	}
}
