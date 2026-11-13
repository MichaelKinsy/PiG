// SPDX-License-Identifier: MIT

// The ceilings of TestReleaseBinarySizeGate say that the binary grew; this gate says which package made it grow. The linker removes code that nothing
// calls, so a package that is in the dependency graph costs nothing until a call reaches it, and then all of it comes into the binary. This test reads the
// symbol table of each release build and fails with the name of a package that is linked and should not be.

//go:build linux

package perfgate

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/MichaelKinsy/PiG/internal/sizereport"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// linkedPackagesPath is the policy file, relative to the module root.
const linkedPackagesPath = "automation/perf/linked-packages.toml"

type linkedPackages struct {
	// HeavyBytes is the symbol bytes from which a package that is not in the Go distribution is heavyweight.
	HeavyBytes int64 `toml:"heavy_bytes"`
	// Targets are the platforms whose binaries are read.
	Targets []string `toml:"targets"`
	// Binary maps a binary's name to its policy.
	Binary map[string]linkedBinary `toml:"binary"`
}

type linkedBinary struct {
	// Build is the package to build.
	Build string `toml:"build"`
	// Allow lists the heavyweight packages the binary may link. A package outside the list that reaches HeavyBytes fails the gate, and so does a package on the list that no target links any more, so the list only shrinks.
	Allow []string `toml:"allow"`
	// Forbid maps a package (and the packages below it) that the binary must not link at all, whatever its size, to the reason.
	Forbid map[string]string `toml:"forbid"`
}

// loadLinkedPackages reads the policy file at path. A file that lists no target or no binary is an error: the gate would build nothing and pass.
func loadLinkedPackages(path string) (linkedPackages, error) {
	var policy linkedPackages
	if _, err := toml.DecodeFile(path, &policy); err != nil {
		return linkedPackages{}, fmt.Errorf("read %s: %w", path, err)
	}
	if len(policy.Targets) == 0 || len(policy.Binary) == 0 {
		return linkedPackages{}, fmt.Errorf("%s lists no targets or no binary (%d targets, %d binaries)", path, len(policy.Targets), len(policy.Binary))
	}
	return policy, nil
}

// A policy file that does not parse is an error that names the file and the line, and one that lists no target or no binary is an error too: the gate would build nothing and pass.
func TestLinkedPackagesPolicyMustParseAndListBinaries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "linked-packages.toml")
	for _, c := range []struct{ name, content, want string }{
		{"malformed", "heavy_bytes = 1\ntargets = [\"linux_amd64\"]\"\n", "line 2"},
		{"empty", "", "lists no"},
		{"no binary", "heavy_bytes = 1\ntargets = [\"linux_amd64\"]\n", "lists no"},
		{"no target", "heavy_bytes = 1\n\n[binary.pig]\nbuild = \"./cmd/pig\"\n", "lists no"},
	} {
		if err := os.WriteFile(path, []byte(c.content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadLinkedPackages(path); err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to name %s and %q", c.name, err, path, c.want)
		}
	}
	if err := os.WriteFile(path, []byte("heavy_bytes = 1\ntargets = [\"linux_amd64\"]\n\n[binary.pig]\nbuild = \"./cmd/pig\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if policy, err := loadLinkedPackages(path); err != nil || len(policy.Binary) != 1 || len(policy.Targets) != 1 {
		t.Fatalf("policy = %+v, err = %v; want one binary and one target", policy, err)
	}
}

// The stock binaries of the release, built unstripped (the code is the same as the stripped release build), may link only the heavyweight packages the policy file lists.
func TestReleaseBinaryLinksOnlyAllowedPackages(t *testing.T) {
	root := testenv.ModuleRoot(t)
	policy, err := loadLinkedPackages(filepath.Join(root, linkedPackagesPath))
	if err != nil {
		t.Fatal(err)
	}
	for name, binary := range policy.Binary {
		t.Run(name, func(t *testing.T) {
			linkedHeavy := map[string]bool{}
			for _, target := range policy.Targets {
				goos, goarch, _ := strings.Cut(target, "_")
				t.Run(target, func(t *testing.T) {
					build := sizereport.Build{Root: root, Package: binary.Build, GOOS: goos, GOARCH: goarch}
					symbols, err := build.Symbols(t.Context(), t.TempDir())
					if err != nil {
						t.Fatal(err)
					}
					totals := sizereport.Sum(symbols).File
					for _, row := range sizereport.Sorted(totals) {
						if !sizereport.IsImportPath(row.Package) {
							continue
						}
						if reason, forbidden := forbiddenPackage(binary.Forbid, row.Package); forbidden {
							t.Errorf("%s links %s (%d bytes), which it must not: %s. %s", name, row.Package, row.Bytes, reason, whyHint(binary.Build, goos, goarch, row.Package))
							continue
						}
						if row.Bytes < policy.HeavyBytes || sizereport.IsStandard(row.Package) {
							continue
						}
						linkedHeavy[row.Package] = true
						if !slices.Contains(binary.Allow, row.Package) {
							t.Errorf("%s now links %s (%d bytes, over the %d bytes that make a package heavyweight) and %s does not list it. Something that was dead code is reachable now. %s Remove the call that reaches it. A third-party package joins the list only with the owner's approval; a package of PiG itself joins it in the commit that makes it that large, with the size report in the commit message", name, row.Package, row.Bytes, policy.HeavyBytes, linkedPackagesPath, whyHint(binary.Build, goos, goarch, row.Package))
						}
					}
				})
			}
			var stale []string
			for _, pkg := range binary.Allow {
				if !linkedHeavy[pkg] {
					stale = append(stale, pkg)
				}
			}
			sort.Strings(stale)
			if len(stale) > 0 && !t.Failed() {
				t.Errorf("%s: %s lists packages that no target links as heavyweight any more; delete them so the list only shrinks:\n%s", name, linkedPackagesPath, strings.Join(stale, "\n"))
			}
		})
	}
}

// forbiddenPackage is the reason pkg must not be linked: its own entry or that of a package it is below.
func forbiddenPackage(forbid map[string]string, pkg string) (string, bool) {
	for path, reason := range forbid {
		if pkg == path || strings.HasPrefix(pkg, path+"/") {
			return reason, true
		}
	}
	return "", false
}

func whyHint(build, goos, goarch, pkg string) string {
	return fmt.Sprintf("Find the call that keeps it: go run ./automation/ci/sizereport -pkg %s -goos %s -goarch %s -why %s", build, goos, goarch, pkg)
}

// The dependency gate reads its target list from the policy file, and every target must be a release target of the size gate.
func TestLinkedPackagesTargetsAreReleaseTargets(t *testing.T) {
	root := testenv.ModuleRoot(t)
	policy, err := loadLinkedPackages(filepath.Join(root, linkedPackagesPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range policy.Targets {
		goos, goarch, _ := strings.Cut(target, "_")
		if !slices.ContainsFunc(releasePlatforms, func(p struct{ goos, goarch string }) bool { return p.goos == goos && p.goarch == goarch }) {
			t.Errorf("target %s is not a release platform of TestReleaseBinarySizeGate", target)
		}
	}
	if policy.HeavyBytes <= 0 || len(policy.Binary) == 0 {
		t.Errorf("%s has no heavy_bytes or no binary", linkedPackagesPath)
	}
}
