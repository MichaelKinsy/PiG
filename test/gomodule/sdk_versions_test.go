// SPDX-License-Identifier: MIT

package gomodule

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/automation/release/modulehash"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// The Python and TypeScript SDK packages publish at the PiG release version, which `make set-version` writes: a reader of `pip show pig-sdk` or the npm
// package sees the release that built it. This check fails when either manifest differs. The Rust crate is checked by
// TestRustSDKAcceptsEveryPinnedRequirement instead.
func TestSDKPackagesCarryThePigReleaseVersion(t *testing.T) {
	root := repoRoot(t)
	want := pigversion.PigVersion
	read := func(rel string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	check := func(rel, what string, re *regexp.Regexp) {
		t.Helper()
		match := re.FindStringSubmatch(read(rel))
		if match == nil {
			t.Errorf("%s: no %s found", rel, what)
			return
		}
		if match[1] != want {
			t.Errorf("%s: %s is %s, want the PiG release %s (make set-version VERSION=%s)", rel, what, match[1], want, want)
		}
	}
	check("extensions/sdk-py/pyproject.toml", "pig-sdk project version", regexp.MustCompile(`(?m)^\[project\]\r?\nname = "pig-sdk"\r?\nversion = "([^"]+)"`))
	var manifest struct{ Version string }
	if err := json.Unmarshal([]byte(read("extensions/sdk-ts/package.json")), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != want {
		t.Errorf("extensions/sdk-ts/package.json: version is %s, want the PiG release %s", manifest.Version, want)
	}
	var lock struct {
		Version  string
		Packages map[string]struct{ Version string }
	}
	if err := json.Unmarshal([]byte(read("extensions/sdk-ts/package-lock.json")), &lock); err != nil {
		t.Fatal(err)
	}
	if lock.Version != want || lock.Packages[""].Version != want {
		t.Errorf("extensions/sdk-ts/package-lock.json: versions %q and %q, want the PiG release %s", lock.Version, lock.Packages[""].Version, want)
	}

}

// Pig resolves an un-pathed `pig-sdk` requirement to the staged SDK through a crates-io patch (coding/extension/host/subprocess/builder.go buildRust,
// coding/extension/host/runtimecell/rust_packed.go), and Cargo applies a patch only when the patched crate's version satisfies the requirement. Every
// scaffolded Rust extension requires "0.1.0", so the crate stays on 0.1.x and does not follow the PiG release; this check fails when the crate, a
// Cargo.lock that records it or a tracked requirement would stop accepting it.
func TestRustSDKAcceptsEveryPinnedRequirement(t *testing.T) {
	root := repoRoot(t)
	read := func(rel string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	crate := regexp.MustCompile(`(?m)^\[package\]\r?\nname = "pig-sdk"\r?\nversion = "(0\.1\.[0-9]+)"`).FindStringSubmatch(read("extensions/sdk-rs/Cargo.toml"))
	if crate == nil {
		t.Fatal("extensions/sdk-rs/Cargo.toml: the pig-sdk crate version is not 0.1.x, which scaffolded `pig-sdk = \"0.1.0\"` requirements need")
	}
	files, err := modulehash.TrackedFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	lockEntry := regexp.MustCompile(`(?m)^name = "pig-sdk"\r?\nversion = "([^"]+)"`)
	requirement := regexp.MustCompile(`(?m)^pig-sdk = "([^"]+)"`)
	seen := 0
	for _, file := range files {
		file = strings.ReplaceAll(file, "\\", "/")
		switch path.Base(file) {
		case "Cargo.lock":
			if match := lockEntry.FindStringSubmatch(read(file)); match != nil {
				seen++
				if match[1] != crate[1] {
					t.Errorf("%s: pig-sdk lock entry is %s, want the crate version %s", file, match[1], crate[1])
				}
			}
		case "Cargo.toml":
			if match := requirement.FindStringSubmatch(read(file)); match != nil && file != "extensions/sdk-rs/Cargo.toml" {
				seen++
				if !caretAccepts(match[1], crate[1]) {
					t.Errorf("%s: pig-sdk requirement %s does not accept the crate version %s", file, match[1], crate[1])
				}
			}
		}
	}
	if seen == 0 {
		t.Error("no Cargo.lock or Cargo.toml records the pig-sdk crate; the check would pass vacuously")
	}
}

// The Rust and Python SDK manifests publish with the metadata a package index shows and requires: a description, a license, the project repository and a readme that
// exists next to the manifest. `cargo package` fails when the readme the manifest names is missing and warns about a missing repository; the Python manifest
// already carried these.
func TestSDKPackageManifestsCarryPublicationMetadata(t *testing.T) {
	const project = "https://github.com/MichaelKinsy/PiG"
	root := repoRoot(t)
	for _, manifest := range []struct {
		path       string
		fields     []string
		repository string
	}{
		{"extensions/sdk-rs/Cargo.toml", []string{"description", "license", "repository", "homepage", "readme", "keywords"}, "repository"},
		{"extensions/sdk-py/pyproject.toml", []string{"description", "license", "Repository", "Homepage", "readme", "keywords"}, "Repository"},
	} {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(manifest.path)))
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		for _, field := range manifest.fields {
			if !regexp.MustCompile(`(?m)^` + field + `\s*=`).MatchString(text) {
				t.Errorf("%s has no %s entry", manifest.path, field)
			}
		}
		if got := quotedValue(text, manifest.repository); got != project {
			t.Errorf("%s: %s = %q, want the project repository %q", manifest.path, manifest.repository, got, project)
		}
		readme := quotedValue(text, "readme")
		if readme == "" {
			t.Errorf("%s names no readme file", manifest.path)
			continue
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path.Dir(manifest.path)), filepath.FromSlash(readme))); err != nil {
			t.Errorf("%s: the readme it names, %s, is missing: %v", manifest.path, readme, err)
		}
	}
}

// caretAccepts reports whether Cargo's default (caret) requirement req accepts the 0.x version have: same minor, and a patch no lower than req's.
func caretAccepts(req, have string) bool {
	r, h := strings.Split(req, "."), strings.Split(have, ".")
	if len(r) < 2 || len(r) > 3 || len(h) != 3 || r[0] != "0" || h[0] != "0" || r[1] != h[1] {
		return false
	}
	if len(r) == 2 {
		return true
	}
	rp, err1 := strconv.Atoi(r[2])
	hp, err2 := strconv.Atoi(h[2])
	return err1 == nil && err2 == nil && hp >= rp
}

// quotedValue returns the double-quoted string assigned to key at the start of a line in a TOML document, or "" when there is none.
func quotedValue(text, key string) string {
	match := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `\s*=\s*"([^"]*)"`).FindStringSubmatch(text)
	if match == nil {
		return ""
	}
	return match[1]
}
