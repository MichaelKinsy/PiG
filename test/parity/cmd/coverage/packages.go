package main

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/test/parity/upstreampackages"
)

// corePackages are the four packages that PORT_MAP covered before every package of the monorepo was mapped. Their subtotal keeps the earlier headline comparable.
var corePackages = []string{"agent", "ai", "coding-agent", "tui"}

// shippedEntryPackage is the package whose npm manifest names what Pi ships.
const shippedEntryPackage = "coding-agent"

// packageRow is the status count of one upstream package.
type packageRow struct {
	Key   string
	Stats statusStats
}

// packageAccounting is the per-package table and the subtotals derived from it.
type packageAccounting struct {
	Rows []packageRow
	// Core, Shipped and Whole are the subtotals over the core packages, the shipped surface and every package.
	Core, Shipped, Whole statusStats
	// ShippedPackages are the packages Pi ships: the dependency closure of the coding-agent package. ExcludedPrefixes are the shipped-package source paths that its files list excludes.
	ShippedPackages  []string
	ExcludedPrefixes []string
	// Closure counts the semantic interface mappings by disposition; nil when the tree has no mapping.
	Closure *interfaceClosure
}

// entryPackage is the package key of an upstream path such as packages/ai/src/models.ts.
func entryPackage(upstream string) (string, bool) {
	parts := strings.SplitN(upstream, "/", 3)
	if len(parts) < 3 || parts[0] != "packages" {
		return "", false
	}
	return parts[1], true
}

type npmManifest struct {
	Name         string            `json:"name"`
	Dependencies map[string]string `json:"dependencies"`
	Files        []string          `json:"files"`
}

func readManifest(upstreamRoot, key string) (npmManifest, error) {
	var manifest npmManifest
	body, err := os.ReadFile(filepath.Join(upstreamRoot, "packages", key, "package.json"))
	if err != nil {
		return manifest, err
	}
	return manifest, json.Unmarshal(body, &manifest)
}

// shippedSurface derives from the mirror what Pi ships: the workspace packages that coding-agent depends on, directly or through another package's dependencies, and the coding-agent source paths its npm files list excludes (`!dist/x` removes src/x).
func shippedSurface(upstreamRoot string) (packages, excluded []string, err error) {
	manifests := map[string]npmManifest{}
	byName := map[string]string{}
	for _, p := range upstreampackages.All() {
		manifest, err := readManifest(upstreamRoot, p.Key)
		if err != nil {
			return nil, nil, err
		}
		manifests[p.Key] = manifest
		byName[manifest.Name] = p.Key
	}
	seen := map[string]bool{shippedEntryPackage: true}
	queue := []string{shippedEntryPackage}
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		for dependency := range manifests[key].Dependencies {
			if next, ok := byName[dependency]; ok && !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	for key := range seen {
		packages = append(packages, key)
	}
	slices.Sort(packages)
	for _, pattern := range manifests[shippedEntryPackage].Files {
		if rest, ok := strings.CutPrefix(pattern, "!dist/"); ok {
			excluded = append(excluded, path.Join("packages", shippedEntryPackage, "src", rest)+"/")
		}
	}
	slices.Sort(excluded)
	return packages, excluded, nil
}

// accountPackages groups the PORT_MAP entries by package and derives the subtotals.
func accountPackages(entries []portMapEntry, coverage, behavioralCoverage map[string][]string, upstreamRoot string) (packageAccounting, error) {
	var accounting packageAccounting
	shipped, excluded, err := shippedSurface(upstreamRoot)
	if err != nil {
		return accounting, fmt.Errorf("shipped surface: %w", err)
	}
	accounting.ShippedPackages, accounting.ExcludedPrefixes = shipped, excluded
	listed := map[string]bool{}
	for _, p := range upstreampackages.All() {
		listed[p.Key] = true
	}
	byPackage := map[string][]portMapEntry{}
	var shippedEntries, coreEntries []portMapEntry
	for _, e := range entries {
		key, ok := entryPackage(e.UpstreamPath)
		if !ok {
			continue
		}
		if !listed[key] {
			return accounting, fmt.Errorf("PORT_MAP maps %s, but package %s is not in test/parity/upstreampackages/packages.json", e.UpstreamPath, key)
		}
		byPackage[key] = append(byPackage[key], e)
		if slices.Contains(corePackages, key) {
			coreEntries = append(coreEntries, e)
		}
		if slices.Contains(shipped, key) && !hasAnyPrefix(e.UpstreamPath, excluded) {
			shippedEntries = append(shippedEntries, e)
		}
	}
	var all []portMapEntry
	for _, p := range upstreampackages.All() {
		accounting.Rows = append(accounting.Rows, packageRow{Key: p.Key, Stats: computeStatusStats(byPackage[p.Key], coverage, behavioralCoverage)})
		all = append(all, byPackage[p.Key]...)
	}
	accounting.Core = computeStatusStats(coreEntries, coverage, behavioralCoverage)
	accounting.Shipped = computeStatusStats(shippedEntries, coverage, behavioralCoverage)
	accounting.Whole = computeStatusStats(all, coverage, behavioralCoverage)
	return accounting, nil
}

func hasAnyPrefix(value string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

// subtotalLine renders one subtotal of the accounting.
func subtotalLine(label string, s statusStats) string {
	return fmt.Sprintf("- **%s:** %d / %d intended-portable files ✅ (%.1f%%) from %d rows; %d behavioral (%.1f%%), %d weak-only, %d untested.",
		label, s.Ported, s.intendedPortable(), s.portingPercentage(), s.Total, s.Behavioral, s.behavioralPercentage(), s.NonBehavioralOnly, s.Untested)
}

// writePackageAccounting renders the per-package table and the subtotals.
func writePackageAccounting(w io.Writer, accounting packageAccounting) {
	_, _ = fmt.Fprintln(w, "| package | src files | n/a | intended | ✅ | 🟡 | ⬜ | ported | behavioral |")
	_, _ = fmt.Fprintln(w, "|---|---:|---:|---:|---:|---:|---:|---:|---:|")
	for _, row := range accounting.Rows {
		s := row.Stats
		_, _ = fmt.Fprintf(w, "| `%s` | %d | %d | %d | %d | %d | %d | %.1f%% | %.1f%% |\n",
			row.Key, s.Total, s.NA, s.intendedPortable(), s.Ported, s.Partial, s.NotStarted, s.portingPercentage(), s.behavioralPercentage())
	}
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, subtotalLine("Four core packages ("+strings.Join(corePackages, ", ")+")", accounting.Core))
	_, _ = fmt.Fprintln(w, subtotalLine("Shipped surface ("+strings.Join(accounting.ShippedPackages, ", ")+"; coding-agent without "+strings.Join(accounting.ExcludedPrefixes, ", ")+")", accounting.Shipped))
	_, _ = fmt.Fprintln(w, subtotalLine(fmt.Sprintf("Whole monorepo (%d packages)", len(accounting.Rows)), accounting.Whole))
	if accounting.Closure != nil {
		_, _ = fmt.Fprintln(w, accounting.Closure.line())
	}
}

// interfaceClosure counts the semantic interface mappings by disposition.
type interfaceClosure struct {
	Total        int
	Dispositions map[string]int
}

// loadInterfaceClosure reads test/parity/interfaces/mapping-v<version>.json under repoRoot. A missing file yields nil, so a fixture tree without a mapping reports no closure.
func loadInterfaceClosure(repoRoot, version string) (*interfaceClosure, error) {
	body, err := os.ReadFile(filepath.Join(repoRoot, "test", "parity", "interfaces", "mapping-v"+version+".json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var mapping struct {
		Mappings []struct {
			Disposition string `json:"disposition"`
		} `json:"mappings"`
	}
	if err := json.Unmarshal(body, &mapping); err != nil {
		return nil, err
	}
	closure := &interfaceClosure{Total: len(mapping.Mappings), Dispositions: map[string]int{}}
	for _, m := range mapping.Mappings {
		closure.Dispositions[m.Disposition]++
	}
	return closure, nil
}

// line renders the closure for the reports. A file-level ✅ is not interface closure, so only mappings with a closing disposition count as closed.
func (c *interfaceClosure) line() string {
	var parts []string
	for _, name := range slices.Sorted(maps.Keys(c.Dispositions)) {
		parts = append(parts, fmt.Sprintf("%d %s", c.Dispositions[name], name))
	}
	closed := c.Dispositions["ported"] + c.Dispositions["designed-out"] + c.Dispositions["divergence"]
	return fmt.Sprintf("- **Interface closure:** %d of %d semantic interface IDs closed (%s). A file-level ✅ does not close an interface ID.", closed, c.Total, strings.Join(parts, ", "))
}
