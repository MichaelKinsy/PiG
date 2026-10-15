package packagecontent

import (
	"bytes"
	"encoding/json"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// minimatchOracle runs minimatch(path, pattern) with minimatch's default options in Node for each pair and returns the result, or the message of the error it throws. Node's bundled minimatch, reachable only with --expose-internals (the test skips without it), equals Pi's npm minimatch 10.2.6 except in brace expansion, where it bundles an older brace-expansion. The pairs here expand alike in both; glob_braces_npm_test.go records the patterns that do not, from Pi's copy.
func minimatchOracle(t *testing.T, pairs any) []any {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	input, err := json.Marshal(pairs)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "--expose-internals", "--eval", `
let mm;
try { mm = require("internal/deps/minimatch/index"); } catch { process.stdout.write("null"); process.exit(0); }
const pairs = JSON.parse(require("fs").readFileSync(0, "utf8"));
process.stdout.write(JSON.stringify(pairs.map((p) => { try { return mm.minimatch(p.Path, p.Pattern); } catch (e) { return String(e.message); } })));
`)
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = append(os.Environ(), "NODE_NO_WARNINGS=1")
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	var want []any
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatalf("node output %q: %v", output, err)
	}
	if want == nil {
		t.Skip("node's bundled minimatch is not reachable")
	}
	return want
}

// minimatchMatch is minimatch(path, pattern): false for a comment, else new Minimatch(pattern).match(path), or the error the constructor throws.
func minimatchMatch(path, pattern string) any {
	m, err := newMinimatchFilter(pattern)
	if err != nil {
		return err.Error()
	}
	return m.match(path)
}

// Pi's filters call minimatch(path, pattern) with its default options (package-manager.ts:661-685): globstars that do not cross dot parts, braces, extglobs, classes and POSIX classes, "#" comments, "!" negation, ".." folding, trailing slashes, and on Windows backslash paths, drives and "//?/" UNC roots. Random patterns and paths against Node's bundled minimatch, errors compared by message.
func TestMinimatchFilterMatchesMinimatch(t *testing.T) {
	atoms := []string{"a", "b", "x", "K", "\u212A", "*", "?", "**", ".", "..", "-", ",", "#", "!", "[ab]", "[!a]", "[[:alpha:]]", "[A-Z]", "{a,b}", "{1..2}",
		"@(a|b)", "!(a)", "+(x)", "*(a|b)", "?(a)", ".md", "md", "/", "//", "C:", "c:", "//?/C:/", "\U0001F600"}
	parts := []string{"a", "b", "x", "K", "k", "\u212A", ".a", "a.md", "x.md", "ab", "a,b", "1", "C:", "c:", "", "\U0001F600", "-"}
	rng := rand.New(rand.NewPCG(31, 37))
	type pair struct{ Path, Pattern string }
	var pairs []pair
	for range 6000 {
		var pattern strings.Builder
		for range 1 + rng.IntN(5) {
			pattern.WriteString(atoms[rng.IntN(len(atoms))])
		}
		for range 3 {
			var path strings.Builder
			for i := range 1 + rng.IntN(4) {
				if i > 0 {
					path.WriteString([]string{"/", "/", `\`, "//"}[rng.IntN(4)])
				}
				path.WriteString(parts[rng.IntN(len(parts))])
			}
			if rng.IntN(8) == 0 {
				pairs = append(pairs, pair{"//?/C:/" + path.String(), pattern.String()})
				continue
			}
			pairs = append(pairs, pair{path.String(), pattern.String()})
		}
	}
	want := minimatchOracle(t, pairs)
	failures, matched, thrown := 0, 0, 0
	for i, p := range pairs {
		switch want[i] {
		case true:
			matched++
		case false:
		default:
			thrown++
		}
		if got := minimatchMatch(p.Path, p.Pattern); got != want[i] {
			if failures++; failures <= 20 {
				t.Errorf("minimatch(%q, %q) = %v, want Node's %v", p.Path, p.Pattern, got, want[i])
			}
		}
	}
	t.Logf("%d pairs: %d match, %d that Node rejects", len(pairs), matched, thrown)
}

// Globstar patterns against deeper paths: heads, tails, sections between globstars, dot parts a globstar does not cross, trailing empty parts, and "." and ".." parts that the first optimization level keeps or folds.
func TestMinimatchFilterGlobstarsMatchMinimatch(t *testing.T) {
	segments := []string{"**", "**", "a", "b", "*", "?", "*.md", ".x", "x*", "{a,b}", "@(a|x)", "..", ".", ""}
	parts := []string{"a", "b", "x", "ab", "a.md", ".x", ".", "..", "x.md", ""}
	rng := rand.New(rand.NewPCG(41, 43))
	type pair struct{ Path, Pattern string }
	var pairs []pair
	for range 5000 {
		var pattern []string
		for range 1 + rng.IntN(6) {
			pattern = append(pattern, segments[rng.IntN(len(segments))])
		}
		for range 3 {
			var path []string
			for range 1 + rng.IntN(6) {
				path = append(path, parts[rng.IntN(len(parts))])
			}
			pairs = append(pairs, pair{strings.Join(path, "/"), strings.Join(pattern, "/")})
		}
	}
	want := minimatchOracle(t, pairs)
	failures, matched := 0, 0
	for i, p := range pairs {
		if want[i] == true {
			matched++
		}
		if got := minimatchMatch(p.Path, p.Pattern); got != want[i] {
			if failures++; failures <= 20 {
				t.Errorf("minimatch(%q, %q) = %v, want Node's %v", p.Path, p.Pattern, got, want[i])
			}
		}
	}
	t.Logf("%d pairs: %d match", len(pairs), matched)
}

// mustApplyPatterns is ApplyPatterns for patterns minimatch compiles.
func mustApplyPatterns(t testing.TB, allPaths, patterns []string, baseDir string, kind Kind) []string {
	t.Helper()
	kept, err := ApplyPatterns(allPaths, patterns, baseDir, kind)
	if err != nil {
		t.Fatal(err)
	}
	return kept
}

// mustResourceEnabled is ResourceEnabled for patterns minimatch compiles.
func mustResourceEnabled(t testing.TB, relativePath string, patterns []string) bool {
	t.Helper()
	enabled, err := ResourceEnabled(relativePath, patterns)
	if err != nil {
		t.Fatal(err)
	}
	return enabled
}

// mustAutoloadStates is ApplyAutoloadDisabledPatterns for patterns minimatch compiles.
func mustAutoloadStates(t testing.TB, allPaths, patterns []string, baseDir string, kind Kind) []PatternState {
	t.Helper()
	states, err := ApplyAutoloadDisabledPatterns(allPaths, patterns, baseDir, kind)
	if err != nil {
		t.Fatal(err)
	}
	return states
}

// A manifest's "!" entries go through minimatch as Pi's addManifestEntries applies them (package-manager.ts:2358-2376), so a brace set excludes each of its members. Probed with Pi 1.1.0: "prompts/*.md" with "!prompts/{a,b}.md" lists only c.
func TestManifestOverridesFollowMinimatch(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"p","pi":{"prompts":["prompts/*.md","!prompts/{a,b}.md"]}}`)
	for _, name := range []string{"a", "b", "c"} {
		writeTestFile(t, filepath.Join(root, "prompts", name+".md"), "x\n")
	}
	resources, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{filepath.Join(root, "prompts", "c.md")}; !slices.Equal(resources.PromptFiles, want) {
		t.Fatalf("prompts = %q, want %q", resources.PromptFiles, want)
	}
}

// Fixed pairs against Node's minimatch: an empty pattern matches only an empty path, a "//?/<drive>:" UNC pattern keeps its "?" literal and lines its drive up with a drive path (ignoring case) and the other way round, and adjacent globstars act as one.
func TestMinimatchFilterEdgeRowsMatchMinimatch(t *testing.T) {
	type pair struct{ Path, Pattern string }
	pairs := []pair{
		{"", ""}, {"/", ""}, {"a", ""},
		{"C:/x/a.md", "//?/C:/x/*.md"}, {"c:/x/a.md", "//?/C:/x/*.md"}, {"//?/C:/x/a.md", "C:/x/*.md"}, {"//?/c:/x/a.md", "//?/C:/x/a.md"},
		{"//y/C:/x", "//?/C:/x"}, {"//?/C:/x", "//?/C:/x"}, {"D:/x/a.md", "//?/C:/x/*.md"}, {`C:\x\a.md`, "C:/x/*.md"},
		{"a/b", "a/**/**/b"}, {"a/x/b", "a/**/**/b"}, {"a/.x/b", "a/**/**/b"}, {"x/a/y/b", "**/a/**/**/b"},
	}
	want := minimatchOracle(t, pairs)
	for i, p := range pairs {
		if got := minimatchMatch(p.Path, p.Pattern); got != want[i] {
			t.Errorf("minimatch(%q, %q) = %v, want Node's %v", p.Path, p.Pattern, got, want[i])
		}
	}
}

// Several globstars between short literals against long paths, where a later section can run out of room after an earlier one matched: minimatch then stops trying the earlier section's later positions.
func TestMinimatchFilterGlobstarSectionsMatchMinimatch(t *testing.T) {
	segments := []string{"**", "**", "**", "a", "b", "c", "*"}
	parts := []string{"a", "b", "c", "x"}
	rng := rand.New(rand.NewPCG(53, 59))
	type pair struct{ Path, Pattern string }
	var pairs []pair
	for range 4000 {
		var pattern []string
		for range 3 + rng.IntN(5) {
			pattern = append(pattern, segments[rng.IntN(len(segments))])
		}
		for range 3 {
			var path []string
			for range 3 + rng.IntN(7) {
				path = append(path, parts[rng.IntN(len(parts))])
			}
			pairs = append(pairs, pair{strings.Join(path, "/"), strings.Join(pattern, "/")})
		}
	}
	want := minimatchOracle(t, pairs)
	failures, matched := 0, 0
	for i, p := range pairs {
		if want[i] == true {
			matched++
		}
		if got := minimatchMatch(p.Path, p.Pattern); got != want[i] {
			if failures++; failures <= 20 {
				t.Errorf("minimatch(%q, %q) = %v, want Node's %v", p.Path, p.Pattern, got, want[i])
			}
		}
	}
	t.Logf("%d pairs: %d match", len(pairs), matched)
}
