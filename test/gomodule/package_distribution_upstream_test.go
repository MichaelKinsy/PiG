// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package gomodule

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// Equivalence port of packages/coding-agent/test/package-distribution.test.ts (Pi 1.0.0). Pi asserts that its npm
// package runs a bundle as the executable, exposes modular output to libraries, and keeps its experimental
// entrypoints (./client, ./experimental/plugin) source-only (#9132). PiG ships the same two channels differently:
// the executable is the @pi-in-go/pig npm launcher plus one native binary package per release target, and the
// library is the Go module. The invariants are the same: every supported os/arch reaches an executable, the
// published packages contain exactly what their manifests list, and no experimental surface is a published,
// importable runtime entrypoint.

const packageDistributionTest = "packages/coding-agent/test/package-distribution.test.ts"

// npm platform and cpu names of Go's release targets (pack_npm.py TARGETS, Node's process.platform/arch).
var npmOS = map[string]string{"android": "android", "darwin": "darwin", "linux": "linux", "windows": "win32"}
var npmCPU = map[string]string{"amd64": "x64", "arm64": "arm64"}

// releaseTargets reads the goos/goarch matrix of the release-candidate binary job: the archives npm packs.
func releaseTargets(t *testing.T, root string) [][2]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release-candidate.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Strategy struct {
				Matrix struct {
					Include []struct {
						GOOS   string `yaml:"goos"`
						GOARCH string `yaml:"goarch"`
					} `yaml:"include"`
				} `yaml:"matrix"`
			} `yaml:"strategy"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	var targets [][2]string
	for _, target := range workflow.Jobs["binary"].Strategy.Matrix.Include {
		targets = append(targets, [2]string{target.GOOS, target.GOARCH})
	}
	if len(targets) == 0 {
		t.Fatal("release-candidate.yml binary job has no goos/goarch matrix")
	}
	return targets
}

// writeReleaseArchives writes the release archives pack_npm.py consumes, with SHA256SUMS.
func writeReleaseArchives(t *testing.T, dir, version string, targets [][2]string) {
	t.Helper()
	var sums strings.Builder
	for _, target := range targets {
		goos, goarch := target[0], target[1]
		prefix := "pig-" + version + "-" + goos + "-" + goarch
		binary := "pig"
		if goos == "windows" {
			binary = "pig.exe"
		}
		files := map[string]string{binary: "binary " + goos + "/" + goarch, "LICENSE": "MIT\n", "NOTICE": "notice\n", "THIRD_PARTY_NOTICES.md": "third party\n", "LICENSES/MIT.txt": "mit\n"}
		names := slices.Sorted(func(yield func(string) bool) {
			for name := range files {
				if !yield(name) {
					return
				}
			}
		})
		var archive bytes.Buffer
		name := prefix + ".tar.gz"
		if goos == "windows" {
			name = prefix + ".zip"
			writer := zip.NewWriter(&archive)
			for _, file := range names {
				entry, err := writer.Create(prefix + "/" + file)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = entry.Write([]byte(files[file]))
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
		} else {
			compressed := gzip.NewWriter(&archive)
			writer := tar.NewWriter(compressed)
			for _, file := range names {
				if err := writer.WriteHeader(&tar.Header{Name: prefix + "/" + file, Mode: 0o755, Size: int64(len(files[file])), Typeflag: tar.TypeReg}); err != nil {
					t.Fatal(err)
				}
				_, _ = writer.Write([]byte(files[file]))
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			if err := compressed.Close(); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, name), archive.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(archive.Bytes())
		sums.WriteString(hex.EncodeToString(sum[:]) + "  " + name + "\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sums.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

type npmManifest struct {
	Name                 string            `json:"name"`
	Bin                  map[string]string `json:"bin"`
	Main                 *string           `json:"main"`
	Exports              json.RawMessage   `json:"exports"`
	Scripts              json.RawMessage   `json:"scripts"`
	Files                []string          `json:"files"`
	OS                   []string          `json:"os"`
	CPU                  []string          `json:"cpu"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
}

// packNpm generates the npm packages with the release generator and returns each manifest by directory.
func packNpm(t *testing.T, root string, targets [][2]string) (string, map[string]npmManifest) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatalf("the npm package generator needs python3: %v", err)
	}
	const version = "0.0.1"
	archives := t.TempDir()
	out := t.TempDir()
	writeReleaseArchives(t, archives, version, targets)
	cmd := exec.Command(python, filepath.Join(root, "automation", "release", "npm", "pack_npm.py"), "--archives", archives, "--version", version, "--out", out, "--no-pack", "--pi-base", "1.0.0")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pack_npm.py: %v\n%s", err, output)
	}
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	manifests := map[string]npmManifest{}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(out, entry.Name(), "package.json"))
		if err != nil {
			t.Fatal(err)
		}
		var manifest npmManifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			t.Fatal(err)
		}
		manifests[entry.Name()] = manifest
	}
	return out, manifests
}

// packageFiles lists a package directory's files relative to it, except package.json, which npm always publishes.
func packageFiles(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err == nil && rel != "package.json" {
			files = append(files, filepath.ToSlash(rel))
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	return files
}

func TestPackageDistributionUpstream(t *testing.T) {
	root := repoRoot(t)
	cases := upstreamTestCases(t, root, packageDistributionTest)
	if len(cases) != 2 {
		t.Fatalf("upstream denominator has %d cases, want the 2 this port maps", len(cases))
	}
	targets := releaseTargets(t, root)
	out, manifests := packNpm(t, root, targets)
	launcherSource, err := os.ReadFile(filepath.Join(root, "automation", "release", "npm", "launcher", "bin", "pig.js"))
	if err != nil {
		t.Fatal(err)
	}
	byLine := map[int]func(t *testing.T){
		// uses the bundle for executables and modular output for libraries: bin is the executable entrypoint and
		// nothing else is. PiG's launcher maps every release target to a platform package that carries that
		// target's native binary, and each package publishes exactly its files list.
		20: func(t *testing.T) {
			launcher, ok := manifests["pig"]
			if !ok || launcher.Name != "@pi-in-go/pig" {
				t.Fatalf("launcher package = %+v", launcher)
			}
			if !sameStringMap(launcher.Bin, map[string]string{"pig": "bin/pig.js"}) {
				t.Errorf("launcher bin = %v, want pig -> bin/pig.js", launcher.Bin)
			}
			if launcher.Main != nil || launcher.Exports != nil || launcher.Scripts != nil {
				t.Errorf("launcher main/exports/scripts = %v/%s/%s; the Go module, not npm, is PiG's library and nothing runs at install", launcher.Main, launcher.Exports, launcher.Scripts)
			}
			if got, want := packageFiles(t, filepath.Join(out, "pig")), slices.Sorted(slices.Values(launcher.Files)); !slices.Equal(got, want) {
				t.Errorf("launcher publishes %v, files lists %v", got, want)
			}
			var wantPackages []string
			for _, target := range targets {
				goos, goarch := target[0], target[1]
				platform, cpu := npmOS[goos], npmCPU[goarch]
				if platform == "" || cpu == "" {
					t.Fatalf("release target %s/%s has no npm platform", goos, goarch)
				}
				name := "@pi-in-go/pig-" + platform + "-" + cpu
				wantPackages = append(wantPackages, name)
				binary := "pig"
				if goos == "windows" {
					binary = "pig.exe"
				}
				dir := "pig-" + platform + "-" + cpu
				manifest, ok := manifests[dir]
				if !ok || manifest.Name != name || !slices.Equal(manifest.OS, []string{platform}) || !slices.Equal(manifest.CPU, []string{cpu}) {
					t.Errorf("platform package for %s/%s = %+v", goos, goarch, manifest)
					continue
				}
				if manifest.Bin != nil || manifest.Main != nil || manifest.Exports != nil || manifest.Scripts != nil {
					t.Errorf("%s declares bin/main/exports/scripts; only the launcher is an entrypoint", name)
				}
				if got, want := packageFiles(t, filepath.Join(out, dir)), slices.Sorted(slices.Values(manifest.Files)); !slices.Equal(got, want) {
					t.Errorf("%s publishes %v, files lists %v", name, got, want)
				}
				data, err := os.ReadFile(filepath.Join(out, dir, binary))
				if err != nil || string(data) != "binary "+goos+"/"+goarch {
					t.Errorf("%s carries %q (%v), want the %s/%s release binary", name, data, err, goos, goarch)
				}
				if !strings.Contains(string(launcherSource), `"`+platform+` `+cpu+`": `+"`${SCOPE}/pig-"+platform+"-"+cpu+"`") {
					t.Errorf("launcher bin/pig.js does not map %s %s to %s", platform, cpu, name)
				}
			}
			sort.Strings(wantPackages)
			got := slices.Sorted(func(yield func(string) bool) {
				for name := range launcher.OptionalDependencies {
					if !yield(name) {
						return
					}
				}
			})
			if !slices.Equal(got, wantPackages) {
				t.Errorf("launcher optionalDependencies = %v, want every release target %v", got, wantPackages)
			}
			if mapped := regexp.MustCompile(`"(\w+ \w+)": `+"`"+`\$\{SCOPE\}/pig-`).FindAllStringSubmatch(string(launcherSource), -1); len(mapped) != len(targets) {
				t.Errorf("launcher maps %d platforms, release builds %d", len(mapped), len(targets))
			}
			if len(manifests) != len(targets)+1 {
				t.Errorf("generator emitted %d packages, want %d platform packages and the launcher", len(manifests), len(targets))
			}
		},
		// keeps experimental exports source-only (#9132): no published package exposes an experimental entrypoint.
		// npm packages export nothing; in the Go module every experimental package is internal (not importable by
		// other modules) or a main package (not importable at all).
		28: func(t *testing.T) {
			for dir, manifest := range manifests {
				if manifest.Exports != nil {
					t.Errorf("%s exports %s", dir, manifest.Exports)
				}
			}
			cmd := exec.Command("go", "list", "-f", "{{.ImportPath}} {{.Name}}", "./...")
			cmd.Dir = root
			output, err := cmd.Output()
			if err != nil {
				t.Fatalf("go list: %v", err)
			}
			experimental := 0
			for line := range strings.SplitSeq(strings.TrimSpace(string(output)), "\n") {
				importPath, name, _ := strings.Cut(line, " ")
				if !strings.Contains(strings.ToLower(importPath), "experimental") {
					continue
				}
				experimental++
				if name != "main" && !strings.HasPrefix(importPath, rootModule+"/internal/") {
					t.Errorf("experimental package %s is importable by other modules", importPath)
				}
			}
			if experimental == 0 {
				t.Error("go list found no experimental package; the check would pass vacuously")
			}
		},
	}
	for _, tc := range cases {
		run, ok := byLine[tc.Line]
		if !ok {
			t.Fatalf("unmapped upstream case %s at line %d", tc.ID, tc.Line)
		}
		t.Run(tc.ID, func(t *testing.T) {
			t.Logf(".upstream/current/%s:%d", packageDistributionTest, tc.Line)
			run(t)
		})
	}
}

func sameStringMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if other, ok := b[key]; !ok || other != value {
			return false
		}
	}
	return true
}

type upstreamTestCase struct {
	ID   string `json:"id"`
	Line int    `json:"line"`
}

// upstreamTestCases reads the compiler-derived case inventory of one pinned upstream test file.
func upstreamTestCases(t *testing.T, root, path string) []upstreamTestCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "test", "parity", "interfaces", "upstream-tests-v"+pigversion.UpstreamVersion+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var inventory struct {
		Files []struct {
			Path  string             `json:"path"`
			Cases []upstreamTestCase `json:"cases"`
		} `json:"files"`
	}
	if err := json.Unmarshal(raw, &inventory); err != nil {
		t.Fatal(err)
	}
	for _, file := range inventory.Files {
		if file.Path == path {
			return file.Cases
		}
	}
	t.Fatalf("missing upstream case inventory for %s", path)
	return nil
}
