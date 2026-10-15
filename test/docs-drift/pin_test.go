package docsdrift

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Prose that states the pinned Pi release or commit, or shows a composite `pig --version` value, must agree with internal/coding/pigversion/pigversion.go. The public-claims gate checks "Pi <version>" wording; this checks the other phrasings a README or page uses for the pin.

var (
	pinConst       = regexp.MustCompile(`(?m)^const (UpstreamVersion|UpstreamCommit|PigVersion) = "([^"]+)"`)
	pinnedRelease  = regexp.MustCompile("(?i)pinned Pi (?:release|version|build) (?:is )?`?(\\d+\\.\\d+\\.\\d+(?:\\+[0-9a-f]{8})?)`?")
	pinnedCommit   = regexp.MustCompile("(?i)pinned(?:[^.\\n]|\\.\\d)*commit `([0-9a-f]{40})`")
	compositeValue = regexp.MustCompile("`(\\d+\\.\\d+\\.\\d+)\\+(\\d+\\.\\d+\\.\\d+(?:\\+[0-9a-f]{8})?)`")
)

func readPin(t *testing.T) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot, "internal", "coding", "pigversion", "pigversion.go"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, m := range pinConst.FindAllStringSubmatch(string(data), -1) {
		out[m[1]] = m[2]
	}
	if out["UpstreamVersion"] == "" || out["UpstreamCommit"] == "" || out["PigVersion"] == "" {
		t.Fatalf("pigversion.go no longer declares the pin constants: %v", out)
	}
	return out
}

func TestProseNamesTheCurrentPin(t *testing.T) {
	pin := readPin(t)
	checked := 0
	for _, src := range corpus(t) {
		if strings.HasSuffix(src.Rel, "bug-ledger.md") {
			continue
		}
		for i, line := range strings.Split(src.Body, "\n") {
			at := func(format string, args ...any) { t.Errorf(src.Rel+":"+itoa(i+1)+": "+format, args...) }
			for _, m := range pinnedRelease.FindAllStringSubmatch(line, -1) {
				checked++
				if m[1] != pin["UpstreamVersion"] {
					at("names the pinned Pi release %s, but the pin is %s", m[1], pin["UpstreamVersion"])
				}
			}
			for _, m := range pinnedCommit.FindAllStringSubmatch(line, -1) {
				checked++
				if m[1] != pin["UpstreamCommit"] {
					at("names the pinned commit %s, but the pin is %s", m[1], pin["UpstreamCommit"])
				}
			}
			for _, m := range compositeValue.FindAllStringSubmatch(line, -1) {
				if !strings.Contains(line, "--version") && !strings.Contains(line, "pig version") {
					continue
				}
				checked++
				if m[1] != pin["PigVersion"] || m[2] != pin["UpstreamVersion"] {
					at("shows the version %s+%s, but this release is %s+%s", m[1], m[2], pin["PigVersion"], pin["UpstreamVersion"])
				}
			}
		}
	}
	if checked < 2 {
		t.Fatalf("found only %d statements of the pin; the patterns are broken", checked)
	}
}

// The README sentence automation/pin/pin.py writes for a commit pin ("The pinned Pi build is <version>, commit `<sha>` of the main
// branch.") states both the version and the commit, so both patterns must read it; the dots of the version stay inside the sentence.
func TestPinPatternsReadTheCommitPinSentence(t *testing.T) {
	sha := strings.Repeat("b", 40)
	line := "The pinned Pi build is 1.1.0+bbbbbbbb, commit `" + sha + "` of the main branch. The pin names the behavior oracle."
	if m := pinnedRelease.FindStringSubmatch(line); m == nil || m[1] != "1.1.0+bbbbbbbb" {
		t.Errorf("pinnedRelease read %q from %q", m, line)
	}
	if m := pinnedCommit.FindStringSubmatch(line); m == nil || m[1] != sha {
		t.Errorf("pinnedCommit read %q from %q", m, line)
	}
	if m := pinnedCommit.FindStringSubmatch("The pin is pinned. Another commit `" + sha + "`."); m != nil {
		t.Errorf("pinnedCommit crossed a sentence: %q", m)
	}
}

func readTrim(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(data))
}

// The toolchain versions the build requirements name are the versions CI installs. The files that pin them are .go-version, .node-version, the npm toolchain manifest and RUST_VERSION in the CI workflow.
func TestBuildRequirementsNameTheCIToolchains(t *testing.T) {
	goVersion := readTrim(t, ".go-version")
	nodeVersion := readTrim(t, ".node-version")
	var npm struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal([]byte(readTrim(t, "automation/ci/npm-toolchain/package.json")), &npm); err != nil {
		t.Fatal(err)
	}
	rust := regexp.MustCompile(`(?m)^\s+RUST_VERSION: (\S+)`).FindStringSubmatch(readTrim(t, ".github/workflows/ci.yml"))
	if rust == nil || npm.Dependencies["npm"] == "" {
		t.Fatalf("could not read the pinned Rust or npm version (rust=%v npm=%q)", rust, npm.Dependencies["npm"])
	}
	want := []struct {
		name, pattern, version string
	}{
		{"Go", `(?m)^[-*] Go (\d+\.\d+\.\d+)\b`, goVersion},
		{"Node.js", `(?m)^[-*] Node\.js (\d+\.\d+\.\d+)\b`, nodeVersion},
		{"npm", `(?m)^[-*] Node\.js \d+\.\d+\.\d+ and npm (\d+\.\d+\.\d+)\b`, npm.Dependencies["npm"]},
		{"Rust", `(?m)^[-*] Rust (\d+\.\d+\.\d+)\b`, rust[1]},
		{"Go", "(?m)^Build the `pig` executable with Go (\\d+\\.\\d+\\.\\d+)\\b", goVersion},
		{"Go", `(?m)^Install Git and Go (\d+\.\d+\.\d+)\.`, goVersion},
	}
	found := map[string]bool{}
	for _, src := range corpus(t) {
		for _, w := range want {
			for _, m := range regexp.MustCompile(w.pattern).FindAllStringSubmatch(src.Body, -1) {
				found[w.name] = true
				if m[1] != w.version {
					t.Errorf("%s names %s %s, but CI installs %s", src.Rel, w.name, m[1], w.version)
				}
			}
		}
	}
	for _, name := range []string{"Go", "Node.js", "npm", "Rust"} {
		if !found[name] {
			t.Errorf("no page states the %s requirement; the patterns are broken", name)
		}
	}
}
