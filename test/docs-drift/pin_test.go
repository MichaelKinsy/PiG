package docsdrift

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Prose that states the pinned Pi release or commit, or shows a composite version beside `pig version` or `--version`, must agree with internal/coding/pigversion/pigversion.go. The public-claims gate checks "Pi <version>" wording; this checks the other phrasings a README or page uses for the pin.

var (
	pinConst      = regexp.MustCompile(`(?m)^const (UpstreamVersion|UpstreamCommit|PigVersion) = "([^"]+)"`)
	pinnedRelease = regexp.MustCompile("(?i)pinned Pi (?:release|version|build) (?:is )?`?(\\d+\\.\\d+\\.\\d+(?:\\+[0-9a-f]{8})?)`?")
	pinnedCommit  = regexp.MustCompile("(?i)pinned(?:[^.\\n]|\\.\\d)*commit `([0-9a-f]{40})`")
	// compositeValue reads a `<PiG release>+<build metadata>` example, with either joiner of a commit pin's metadata (`.` is the binary's, `+` is
	// the wrong form), so that the wrong form is read and compared, not skipped.
	compositeValue = regexp.MustCompile("`(\\d+\\.\\d+\\.\\d+)\\+(\\d+\\.\\d+\\.\\d+(?:[.+][0-9a-f]{8})?)`")
	versionConst   = regexp.MustCompile(`(?m)^const Version = PigVersion \+ "\+([^"]+)"`)
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
	if m := versionConst.FindStringSubmatch(string(data)); m != nil {
		out["BuildMetadata"] = m[1]
	}
	if out["UpstreamVersion"] == "" || out["UpstreamCommit"] == "" || out["PigVersion"] == "" || out["BuildMetadata"] == "" {
		t.Fatalf("pigversion.go no longer declares the pin constants: %v", out)
	}
	return out
}

// compositeProblem says what is wrong with the `<PiG release>+<build metadata>` example m of a line, "" when it is the string the binary
// prints: pigversion.Version, PigVersion plus "+" plus the pin's build metadata (one plus: a commit pin's version joins its commit with a dot).
func compositeProblem(m []string, pin map[string]string) string {
	if m[1] == pin["PigVersion"] && m[2] == pin["BuildMetadata"] {
		return ""
	}
	return "shows the version " + m[1] + "+" + m[2] + ", but the binary prints " + pin["PigVersion"] + "+" + pin["BuildMetadata"]
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
				if problem := compositeProblem(m, pin); problem != "" {
					at("%s", problem)
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

// The README example of the composed version is the binary's string: the wrong form of a commit pin, two pluses, must be read and refused
// (docs-drift once stayed green on `0.7.0+1.5.2+2443c687` while the binary prints `0.7.0+1.5.2.2443c687`).
func TestCompositeExampleIsTheBinaryString(t *testing.T) {
	pin := map[string]string{"PigVersion": "0.8.0", "UpstreamVersion": "1.5.2+2443c687", "BuildMetadata": "1.5.2.2443c687"}
	for _, c := range []struct {
		example string
		ok      bool
	}{
		{"`0.8.0+1.5.2.2443c687`", true},
		{"`0.8.0+1.5.2+2443c687`", false},
		{"`0.8.0+1.5.2`", false},
		{"`0.7.0+1.5.2.2443c687`", false},
	} {
		m := compositeValue.FindStringSubmatch(c.example)
		if m == nil {
			t.Fatalf("compositeValue does not read %s", c.example)
		}
		if got := compositeProblem(m, pin) == ""; got != c.ok {
			t.Errorf("%s accepted = %v, want %v", c.example, got, c.ok)
		}
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
