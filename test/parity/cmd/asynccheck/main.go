// Command asynccheck proves that every upstream source file with Promise/async
// behavior has an explicit cross-language disposition and durable evidence.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/test/parity/upstreampackages"
)

var (
	trackedPackages = upstreampackages.All()
	asyncPattern    = regexp.MustCompile(`\basync\b|\bPromise\s*[<.(]|\.then\s*\(|\.catch\s*\(|\.finally\s*\(`)
)

type asyncManifest struct {
	Version string       `toml:"version"`
	Files   []asyncAudit `toml:"files"`
}

type asyncAudit struct {
	Path        string   `toml:"path"`
	Disposition string   `toml:"disposition"`
	Contracts   []string `toml:"contracts"`
	Evidence    []string `toml:"evidence"`
	Rationale   string   `toml:"rationale"`
}

var allowedContracts = map[string]struct{}{
	"awaited":            {},
	"concurrent-joined":  {},
	"detached-owned":     {},
	"cancellable":        {},
	"ui-handoff":         {},
	"microtask-ordering": {},
}

func main() {
	manifestPath := flag.String("manifest", "test/parity/async-contracts.toml", "async contract manifest")
	upstreamRoot := flag.String("upstream", ".upstream/current", "current upstream mirror")
	repoRoot := flag.String("repo-root", ".", "repository root used to validate evidence")
	generate := flag.Bool("generate", false, "print a pending manifest for the current mirror")
	previousManifest := flag.String("previous", "", "with -generate, keep each row of this earlier manifest whose source file is byte-identical in -previous-upstream")
	previousUpstream := flag.String("previous-upstream", "", "with -previous, the earlier version's upstream mirror")
	flag.Parse()

	paths, err := discoverAsyncSources(*upstreamRoot)
	if err != nil {
		fail("discover: %v", err)
	}
	if *generate {
		var carry func(string) (asyncAudit, bool)
		if *previousManifest != "" {
			carry = carryUnchanged(*previousManifest, *previousUpstream, *upstreamRoot)
		}
		fmt.Print(pendingManifest(coding.UpstreamVersion, paths, carry))
		return
	}

	var manifest asyncManifest
	if _, err := toml.DecodeFile(*manifestPath, &manifest); err != nil {
		fail("load %s: %v", *manifestPath, err)
	}
	problems := validateManifest(manifest, paths, *repoRoot)
	if len(problems) > 0 {
		for _, problem := range problems {
			fmt.Fprintln(os.Stderr, "async-contracts:", problem)
		}
		os.Exit(1)
	}
	fmt.Printf("async-contracts: clean (%d async/Promise source files audited at %s)\n", len(paths), manifest.Version)
}

func discoverAsyncSources(root string) ([]string, error) {
	var paths []string
	for _, pkg := range trackedPackages {
		manifestPath := filepath.Join(root, filepath.FromSlash(pkg.Root), "package.json")
		body, err := os.ReadFile(manifestPath)
		if err != nil {
			return nil, err
		}
		var manifest struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		}
		if err := json.Unmarshal(body, &manifest); err != nil {
			return nil, fmt.Errorf("%s: %w", manifestPath, err)
		}
		if manifest.Name != pkg.Name || manifest.Version != coding.UpstreamPackageVersion {
			return nil, fmt.Errorf("%s: got %q version %q, want %q version %q", manifestPath, manifest.Name, manifest.Version, pkg.Name, coding.UpstreamPackageVersion)
		}
		base := filepath.Join(root, filepath.FromSlash(pkg.Root), "src")
		if _, err := os.Stat(base); err != nil {
			return nil, fmt.Errorf("tracked root %s: %w", base, err)
		}
		err = filepath.WalkDir(base, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || (!strings.HasSuffix(entry.Name(), ".ts") && !strings.HasSuffix(entry.Name(), ".tsx")) || isTestPath(path) {
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if !asyncPattern.Match(body) {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			paths = append(paths, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	slices.Sort(paths)
	return paths, nil
}

func isTestPath(path string) bool {
	slashPath := filepath.ToSlash(path)
	name := filepath.Base(path)
	if strings.HasSuffix(name, ".test.ts") || strings.HasSuffix(name, ".spec.ts") || strings.HasSuffix(name, ".test.tsx") || strings.HasSuffix(name, ".spec.tsx") {
		return true
	}
	return strings.Contains(slashPath, "/test/") || strings.Contains(slashPath, "/tests/") || strings.Contains(slashPath, "/__tests__/")
}

func validateManifest(manifest asyncManifest, paths []string, repoRoot string) []string {
	if manifest.Version != coding.UpstreamVersion {
		return []string{fmt.Sprintf("manifest version %q does not match coding.UpstreamVersion %q", manifest.Version, coding.UpstreamVersion)}
	}
	expected := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		expected[path] = struct{}{}
	}
	seen := make(map[string]struct{}, len(manifest.Files))
	var problems []string
	for _, audit := range manifest.Files {
		if _, duplicate := seen[audit.Path]; duplicate {
			problems = append(problems, fmt.Sprintf("duplicate file audit %s", audit.Path))
			continue
		}
		seen[audit.Path] = struct{}{}
		if _, exists := expected[audit.Path]; !exists {
			problems = append(problems, fmt.Sprintf("stale file audit %s", audit.Path))
			continue
		}
		problems = append(problems, validateAudit(audit, repoRoot)...)
	}
	for _, path := range paths {
		if _, exists := seen[path]; !exists {
			problems = append(problems, fmt.Sprintf("missing file audit %s", path))
		}
	}
	slices.Sort(problems)
	return problems
}

func validateAudit(audit asyncAudit, repoRoot string) []string {
	var problems []string
	switch audit.Disposition {
	case "translated":
		if len(audit.Contracts) == 0 {
			problems = append(problems, fmt.Sprintf("%s translated without an async contract", audit.Path))
		}
		if len(audit.Evidence) == 0 {
			problems = append(problems, fmt.Sprintf("%s translated without evidence", audit.Path))
		}
	case "designed-out", "deferred", "no-runtime-async":
		if strings.TrimSpace(audit.Rationale) == "" {
			problems = append(problems, fmt.Sprintf("%s disposition %q requires a rationale", audit.Path, audit.Disposition))
		}
	case "pending":
		problems = append(problems, fmt.Sprintf("%s remains pending", audit.Path))
	default:
		problems = append(problems, fmt.Sprintf("%s has unknown disposition %q", audit.Path, audit.Disposition))
	}
	for _, contract := range audit.Contracts {
		if _, ok := allowedContracts[contract]; !ok {
			problems = append(problems, fmt.Sprintf("%s has unknown async contract %q", audit.Path, contract))
		}
	}
	for _, evidence := range audit.Evidence {
		kind, path, ok := strings.Cut(evidence, ":")
		if !ok || (kind != "scenario" && kind != "test" && kind != "probe") {
			problems = append(problems, fmt.Sprintf("%s has invalid evidence %q", audit.Path, evidence))
			continue
		}
		path, _, _ = strings.Cut(path, "#")
		fullPath := filepath.Join(repoRoot, filepath.FromSlash(path))
		if _, err := os.Stat(fullPath); err != nil {
			problems = append(problems, fmt.Sprintf("%s evidence %q: %v", audit.Path, evidence, err))
			continue
		}
		if kind == "scenario" {
			var scenario struct {
				Covers []string `toml:"covers"`
			}
			if _, err := toml.DecodeFile(fullPath, &scenario); err != nil {
				problems = append(problems, fmt.Sprintf("%s scenario evidence %q: %v", audit.Path, evidence, err))
			} else if !slices.Contains(scenario.Covers, audit.Path) {
				problems = append(problems, fmt.Sprintf("%s scenario evidence %q does not cover the audited path", audit.Path, evidence))
			}
		}
	}
	return problems
}

// pendingManifest renders a manifest with one row per path: carry's row when it has one, otherwise a pending row.
func pendingManifest(version string, paths []string, carry func(string) (asyncAudit, bool)) string {
	var out strings.Builder
	fmt.Fprintf(&out, "version = %q\n", version)
	list := func(values []string) string {
		quoted := make([]string, len(values))
		for i, v := range values {
			quoted[i] = fmt.Sprintf("%q", v)
		}
		return "[" + strings.Join(quoted, ", ") + "]"
	}
	for _, path := range paths {
		row := asyncAudit{Path: path, Disposition: "pending", Contracts: []string{}, Evidence: []string{}}
		if carry != nil {
			if kept, ok := carry(path); ok {
				row = kept
			}
		}
		fmt.Fprintf(&out, "\n[[files]]\npath = %q\ndisposition = %q\ncontracts = %s\nevidence = %s\nrationale = %q\n", row.Path, row.Disposition, list(row.Contracts), list(row.Evidence), row.Rationale)
	}
	return out.String()
}

// carryUnchanged keeps the reviewed rows of an earlier manifest for the source files that are byte-identical in the two mirrors: a
// changed file's async contract may differ, so it is pending again.
func carryUnchanged(manifestPath, previousRoot, root string) func(string) (asyncAudit, bool) {
	var previous asyncManifest
	if _, err := toml.DecodeFile(manifestPath, &previous); err != nil {
		fail("load %s: %v", manifestPath, err)
	}
	rows := map[string]asyncAudit{}
	for _, row := range previous.Files {
		rows[row.Path] = row
	}
	return func(path string) (asyncAudit, bool) {
		row, ok := rows[path]
		if !ok || row.Disposition == "pending" {
			return asyncAudit{}, false
		}
		before, errBefore := os.ReadFile(filepath.Join(previousRoot, filepath.FromSlash(path)))
		now, errNow := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if errBefore != nil || errNow != nil || !bytes.Equal(before, now) {
			return asyncAudit{}, false
		}
		return row, true
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "async-contracts: "+format+"\n", args...)
	os.Exit(1)
}
