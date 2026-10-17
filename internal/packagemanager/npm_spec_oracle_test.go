package packagemanager

import (
	"math/rand"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// pi: packages/coding-agent/src/core/package-manager.ts

// parseSource of an "npm:" source: parseNpmSpec's name and version, getNpmVersionRange and isExactNpmVersion, over spellings of scoped and unscoped names, tags,
// exact versions, ranges (with spaces, tabs, unions, hyphen ranges, x-ranges, carets, tildes), prerelease and build suffixes, v prefixes, extra @ signs, and
// JavaScript line terminators (the dot of Pi's version group does not match \r, \u2028 or \u2029; it does match \v, \f and \u0085).
func TestNpmSourceSpecMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(233))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	name := func() string {
		return pick("pkg", "@scope/pkg", "@scope", "a/b", "@a/b/c", "pkg.js", "p-q_r", "@s/p-q", "UPPER", "é", "@", "@@x", "x/", "/x", "@/x", "a b", "a\tb", "a\u00a0b", "a\nb", "\u00a0")
	}
	version := func() string {
		return pick("1.2.3", "v1.2.3", "=1.2.3", "1.2", "1", "latest", "next", "^1.2.3", "~1.2", ">=1 <2", ">=1.0.0  <2.0.0", ">=1\t<2", "1 - 2", "1.x", "*", "x", "1.2.3-beta.1", "1.2.3+build", "1.2.3-0", "01.2.3", "1.2.3.4", "1.2.3 ", " 1.2.3", "^1.2.3 || ^2", "||", "<1.0.0 || >=2", "1.2.3@4", "@1", "", "a\rb", "1\r", "1\u2028", "1\u2029", "1\v2", "1\f2", "1\u00852", "1\n2", "1.2.3-é", "9007199254740992.0.0", "99999999999999999999", "workspace:*", "latest-1", "^", "~", ">", "<=", "1.2.3-", "1.2.3--")
	}
	var specs []string
	for range 3000 {
		spec := name()
		if rng.Intn(4) != 0 {
			spec += "@" + version()
		}
		if rng.Intn(15) == 0 {
			spec = pick(" ", "\t", "\n", "\u00a0", "\ufeff") + spec + pick(" ", "\t", "\n", "\u00a0")
		}
		specs = append(specs, spec)
	}
	type result struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Range   string `json:"range"`
		Pinned  bool   `json:"pinned"`
	}
	var want []result
	pioracle.Run(t, `
const { DefaultPackageManager } = await load("pi-coding-agent/core/package-manager.js");
const manager = new DefaultPackageManager({ cwd: "/work", agentDir: "/agent", settingsManager: {} });
emit(input.map((spec) => {
	const parsed = manager.parseSource("npm:" + spec);
	return { name: parsed.name, version: parsed.version ?? "", range: parsed.range ?? "", pinned: parsed.pinned };
}));`, specs, &want)
	failures := 0
	for i, spec := range specs {
		// The "npm:" prefix is followed by the trimmed spec, as in Pi.
		trimmed := jsstring.Trim(spec)
		name, version := parseNpmSpec(trimmed)
		got := result{Name: name, Version: version, Range: GetNpmVersionRange(version), Pinned: isExactNpmVersion(version)}
		if got != want[i] {
			if failures++; failures <= 12 {
				t.Errorf("npm:%q: PiG %+v, Pi %+v", spec, got, want[i])
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d specs differ", failures, len(specs))
	}
}
