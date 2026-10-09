// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"

	"github.com/MichaelKinsy/PiG/automation/release/modulehash"
)

const fixtureModule = "example.com/product"

func put(t *testing.T, root, path, text string) {
	t.Helper()
	path = filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func get(t *testing.T, root, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, key := range []string{"HOME", "PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR"} {
		t.Setenv(key, t.TempDir())
	}
	for path, text := range map[string]string{
		"internal/coding/pigversion/pigversion.go": "package pigversion\n\nconst PigVersion = \"0.2.1\"\nconst UpstreamVersion = \"0.87.1\"\n",
		"piglets/standard/pig-standard.yaml":       "name: standard\nrelease:\n  version: 0.2.1-dev\n",
		"CHANGELOG.md":                             "# Changelog\n\n## [Unreleased]\n\n### Fixed\n\n- Pending fix.\n\n## [0.2.1] - 2026-09-26\n\n- Candidate fix.\n\n## [0.2.0] - 2026-09-25\n\n- Published fix.\n",
		"go.mod":                                   "module " + fixtureModule + "\n\ngo 1.26.0\n\nrequire (\n " + fixtureModule + "/sdk v0.2.1\n " + fixtureModule + "/app v0.2.1\n example.com/external v0.2.1\n)\n",
		"go.sum":                                   fixtureModule + "/sdk v0.2.1 h1:stale\n" + fixtureModule + "/sdk v0.2.1/go.mod h1:stale\nexample.com/external v0.2.1 h1:external\n",
		"sdk/go.mod":                               "module " + fixtureModule + "/sdk\n\ngo 1.26.0\n",
		"sdk/sdk.go":                               "package sdk\nconst Answer = 42\n",
		"app/go.mod":                               "module " + fixtureModule + "/app\n\ngo 1.26.0\n\nrequire " + fixtureModule + "/sdk v0.2.1\n",
		"app/go.sum":                               fixtureModule + "/sdk v0.2.1 h1:stale\n",
		"testdata/consumer/go.mod":                 "module consumer\n\ngo 1.26.0\nrequire " + fixtureModule + "/sdk v0.0.0\nreplace " + fixtureModule + "/sdk => ../../sdk\n",
	} {
		put(t, root, path, text)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "--", "internal", "piglets", "CHANGELOG.md", "go.mod", "go.sum", "sdk", "app", "testdata"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// Neither untracked manifests nor untracked SDK files can enter a tag.
	put(t, root, "sdk/ignored.go", "not valid Go and not tracked\n")
	put(t, root, "untracked/go.mod", "malformed module\n")
	return root
}

func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	paths, err := modulehash.TrackedFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, path := range paths {
		out[path] = get(t, root, path)
	}
	return out
}

func assertSnapshot(t *testing.T, root string, want map[string]string) {
	t.Helper()
	for path, before := range want {
		if got := get(t, root, path); got != before {
			t.Errorf("%s changed unexpectedly", path)
		}
	}
}

func assertPins(t *testing.T, root string) {
	t.Helper()
	for _, path := range []string{"go.mod", "app/go.mod", "testdata/consumer/go.mod"} {
		file, err := modfile.Parse(path, []byte(get(t, root, path)), nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, req := range file.Require {
			want := "v0.3.0"
			if req.Mod.Path == "example.com/external" {
				want = "v0.2.1"
			}
			if req.Mod.Version != want {
				t.Errorf("%s: %s = %s, want %s", path, req.Mod.Path, req.Mod.Version, want)
			}
		}
	}
	// This is exactly the shared computation used by test/gomodule, now
	// reading the applied tree rather than the command's planned overlay.
	for _, dir := range []string{"sdk", "app"} {
		mod := module.Version{Path: fixtureModule + "/" + dir, Version: "v0.3.0"}
		zipHash, modHash, err := modulehash.Hashes(root, dir, mod, nil)
		if err != nil {
			t.Fatal(err)
		}
		paths := []string{"go.sum"}
		if dir == "sdk" {
			paths = append(paths, "app/go.sum")
		}
		for _, path := range paths {
			sum := get(t, root, path)
			for _, want := range []string{mod.Path + " " + mod.Version + " " + zipHash + "\n", mod.Path + " " + mod.Version + "/go.mod " + modHash + "\n"} {
				if !strings.Contains(sum, want) {
					t.Errorf("%s missing %s", path, want)
				}
			}
			if strings.Contains(sum, fixtureModule+"/sdk v0.2.1") {
				t.Errorf("%s retains obsolete checksum", path)
			}
		}
	}
}

func TestSetVersionPreservesDependencyPinsAndChecksums(t *testing.T) {
	root := fixture(t)
	before := snapshot(t, root)
	// Published module hashes describe tagged bytes, not the SDK under development.
	put(t, root, "sdk/sdk.go", "package sdk\nconst Answer = 999\n")
	if err := run(root, options{version: "0.3.0", date: "2026-09-27"}, io.Discard, func(string, io.Writer) error { return nil }); err != nil {
		t.Fatal(err)
	}
	for path, want := range before {
		if filepath.Base(path) == "go.mod" || filepath.Base(path) == "go.sum" {
			if got := get(t, root, path); got != want {
				t.Errorf("ordinary version bump changed %s before dependency publication:\n%s", path, got)
			}
		}
	}
	if !strings.Contains(get(t, root, "internal/coding/pigversion/pigversion.go"), `const PigVersion = "0.3.0"`) {
		t.Fatal("release version was not updated")
	}
}

func TestSetVersionBumpDryRunHashesAndIdempotence(t *testing.T) {
	root := fixture(t)
	opt := options{version: "0.3.0", date: "2026-09-27", dryRun: true, releaseModules: true}
	before := snapshot(t, root)
	var output bytes.Buffer
	checks := 0
	check := func(root string, _ io.Writer) error {
		checks++
		assertPins(t, root)
		return nil
	}
	if err := run(root, opt, &output, check); err != nil {
		t.Fatal(err)
	}
	assertSnapshot(t, root, before)
	if checks != 0 || !strings.Contains(output.String(), "+const PigVersion = \"0.3.0\"") || !strings.Contains(output.String(), "+## [0.3.0] - 2026-09-27") {
		t.Fatalf("dry-run did not show the planned changes without checks: checks=%d\n%s", checks, &output)
	}
	opt.dryRun = false
	output.Reset()
	if err := run(root, opt, &output, check); err != nil {
		t.Fatal(err)
	}
	if checks != 1 {
		t.Fatal("apply must run consistency checks after updating pins")
	}
	changelog := get(t, root, "CHANGELOG.md")
	if !strings.Contains(changelog, "- Pending fix.\n\n## [0.3.0] - 2026-09-27\n\n## [0.2.1]") {
		t.Fatalf("default bump moved Unreleased entries or removed prior release:\n%s", changelog)
	}
	if got := get(t, root, "piglets/standard/pig-standard.yaml"); !strings.Contains(got, "version: 0.3.0-dev") {
		t.Fatal(got)
	}
	after := snapshot(t, root)
	opt.date = "2026-09-28"
	output.Reset()
	if err := run(root, opt, &output, check); err != nil {
		t.Fatal(err)
	}
	assertSnapshot(t, root, after)
	if output.Len() != 0 || checks != 2 {
		t.Fatalf("repeat must produce no diff and still check consistency: %s, checks=%d", &output, checks)
	}
}

func TestSetVersionRefreshesSameVersionAndKeepsGoSumOrder(t *testing.T) {
	root := fixture(t)
	opt := options{version: "0.3.0", date: "2026-09-27", releaseModules: true}
	check := func(string, io.Writer) error { return nil }
	if err := run(root, opt, io.Discard, check); err != nil {
		t.Fatal(err)
	}
	before := get(t, root, "go.sum")
	changelog := get(t, root, "CHANGELOG.md")
	put(t, root, "sdk/sdk.go", "package sdk\nconst Answer = 999\n")
	// Go sorts versions semantically, not lexically: 1.9 precedes 1.10.
	external := "example.com/ordered v1.9.0 h1:nine\nexample.com/ordered v1.9.0/go.mod h1:mod\nexample.com/ordered v1.10.0 h1:ten\n"
	put(t, root, "go.sum", before+external)
	if err := run(root, opt, io.Discard, check); err != nil {
		t.Fatal(err)
	}
	assertPins(t, root)
	after := get(t, root, "go.sum")
	if after == before+external || !strings.Contains(after, external) {
		t.Fatalf("same-version refresh left stale hashes or reordered external versions:\n%s", after)
	}
	if got := get(t, root, "CHANGELOG.md"); got != changelog {
		t.Fatalf("checksum refresh changed changelog: %s", got)
	}
}

func TestSetVersionRejectsInvalidAndLowerWithoutWrites(t *testing.T) {
	for _, version := range []string{"", "v0.3.0", "0.3", "0.03.0", "0.3.0-dev", "0.3.0+meta", "0.2.0", "-1.0.0", "0.3.0\n"} {
		t.Run(fmt.Sprintf("%q", version), func(t *testing.T) {
			root := fixture(t)
			before := snapshot(t, root)
			if err := run(root, options{version: version}, io.Discard, func(string, io.Writer) error { t.Fatal("checks ran on invalid input"); return nil }); err == nil {
				t.Fatal("accepted invalid or lower version")
			}
			assertSnapshot(t, root, before)
		})
	}
}

func TestSetVersionRenameMoveAndEmptyIdempotence(t *testing.T) {
	for _, rename := range []bool{false, true} {
		t.Run(fmt.Sprint(rename), func(t *testing.T) {
			root := fixture(t)
			opt := options{version: "0.3.0", date: "2026-09-27", moveUnreleased: true, renameCurrent: rename}
			check := func(string, io.Writer) error { return nil }
			if err := run(root, opt, io.Discard, check); err != nil {
				t.Fatal(err)
			}
			text := get(t, root, "CHANGELOG.md")
			if !strings.Contains(text, "## [Unreleased]\n\n## [0.3.0] - 2026-09-27\n\n### Fixed\n\n- Pending fix.") || strings.Contains(text, "## [0.2.1]") == rename {
				t.Fatal(text)
			}
			after := snapshot(t, root)
			if err := run(root, opt, io.Discard, check); err != nil {
				t.Fatal(err)
			}
			assertSnapshot(t, root, after)
		})
	}
}

func TestSetVersionCheckFailureIsActionable(t *testing.T) {
	root := fixture(t)
	failure := errors.New("guard failed")
	err := run(root, options{version: "0.3.0", date: "2026-09-27", releaseModules: true}, io.Discard, func(string, io.Writer) error { return failure })
	if !errors.Is(err, failure) || !strings.Contains(err.Error(), "changes retained for inspection") {
		t.Fatalf("lost check failure: %v", err)
	}
	assertPins(t, root)
}

func TestSetVersionMalformedChangelogDoesNotPartiallyWrite(t *testing.T) {
	root := fixture(t)
	put(t, root, "CHANGELOG.md", "# No release sections\n")
	before := snapshot(t, root)
	if err := run(root, options{version: "0.3.0"}, io.Discard, func(string, io.Writer) error { return nil }); err == nil {
		t.Fatal("accepted malformed changelog")
	}
	assertSnapshot(t, root, before)
}
