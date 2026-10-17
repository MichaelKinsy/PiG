package cli

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

// Review GF03-REVIEW-001: Pi package-manager.ts:59-65,1446-1457,1473-1478 derives an npm source's range with node-semver validRange and keeps an installed package whose selector is not a valid npm range. Expectations were read from Pi's locked semver 7.8.5 through that composition: != and comma conjunctions are unsupported, space-separated comparators are ANDed, partial versions expand as npm does, and a prerelease matches only its own major.minor.patch.
func TestEnsureConfiguredNpmSelectorUsesNodeSemver(t *testing.T) {
	for _, local := range []bool{false, true} {
		for _, tc := range []struct {
			name, selector, installed string
			install                   bool
		}{
			{"not-equal-unsupported", "!=1.0.0", "1.0.0", false},
			{"comma-unsupported", ">=1.0.0,<2.0.0", "2.5.0", false},
			{"comma-space-unsupported", ">=1.0.0, <2.0.0", "2.5.0", false},
			{"space-comparator-set", ">=1.0.0 <2.0.0", "1.5.0", false},
			{"partial-greater-than", ">1.2", "1.2.5", true},
			{"partial-at-most", "<=1.2", "1.2.9", false},
			{"partial-hyphen", "1.0.0 - 2", "2.9.0", false},
			{"prerelease-other-patch", ">=1.2.3-beta.1", "1.2.4-alpha", true},
		} {
			scope := "user/"
			if local {
				scope = "project/"
			}
			t.Run(scope+tc.name, func(t *testing.T) {
				source := "npm:example@" + tc.selector
				cwd, settings, log := installedVersionFixture(t, source, `{"version":"`+tc.installed+`"}`, local)
				reinstalled, missing := packagemanager.EnsureConfiguredPackagesInstalled(cwd, settings.AgentDir(), settings)
				var want []string
				if tc.install {
					want = []string{source}
				}
				if !slices.Equal(reinstalled, want) || len(missing) != 0 {
					t.Fatalf("installed %s with %q: reinstalled=%v missing=%v, want %v", tc.installed, tc.selector, reinstalled, missing, want)
				}
				data, err := os.ReadFile(log)
				if err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
				installs := 0
				for line := range strings.SplitSeq(string(data), "\n") {
					if strings.HasPrefix(line, "view ") {
						t.Fatalf("resolve queried registry metadata: %s", data)
					}
					if strings.HasPrefix(line, "install ") {
						installs++
					}
				}
				if installs != len(want) {
					t.Fatalf("install calls=%d, want %d: %s", installs, len(want), data)
				}
			})
		}
	}
}

// Pi package-manager.ts:1511-1531 selects maxSatisfying for a valid range and otherwise the highest version by rcompare.
func TestLatestNpmVersionUsesNodeSemverRange(t *testing.T) {
	for _, tc := range []struct{ response, selector, want string }{
		{`["1.0.0", "2.5.0", "1.5.0"]`, ">=1.0.0,<2.0.0", "2.5.0"},
		{`["1.0.0", "2.5.0", "1.5.0"]`, "!=2.5.0", "2.5.0"},
		{`["1.2.9", "1.3.0-0", "1.2.3"]`, "<=1.2", "1.2.9"},
		{`["2.0.0-beta.1", "1.9.0"]`, ">=1.0.0", "1.9.0"},
		{`["1.2.4-alpha", "1.2.3-beta.2", "1.2.3"]`, ">=1.2.3-beta.1", "1.2.3"},
		{`["1.2.4-alpha", "1.2.3-beta.2"]`, ">=1.2.3-beta.1", "1.2.3-beta.2"},
	} {
		t.Run(tc.selector+tc.response, func(t *testing.T) {
			got, err := packagemanager.LatestNpmVersionFromJSON([]byte(tc.response), tc.selector)
			if err != nil || got != tc.want {
				t.Fatalf("latest=%q err=%v, want %q", got, err, tc.want)
			}
		})
	}
}

// Pi package-manager.ts:59-61 pins a source only when node-semver valid accepts its version, which rejects components above Number.MAX_SAFE_INTEGER.
func TestIsPinnedNpmUsesNodeSemverValid(t *testing.T) {
	for _, tc := range []struct {
		source string
		want   bool
	}{
		{"npm:foo@9007199254740991.0.0", true},
		{"npm:foo@9007199254740992.0.0", false},
		{"npm:foo@1.2.3+build", true},
		{"npm:foo@V1.2.3", false},
	} {
		if got := packagemanager.IsPinnedNpm(tc.source); got != tc.want {
			t.Errorf("packagemanager.IsPinnedNpm(%q) = %v, want %v", tc.source, got, tc.want)
		}
	}
}
