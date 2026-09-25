// Command familygaps reports, for one behavior family, which
// upstream PORT_MAP entries fall within its scope and which lack
// behavioral verification. Weak scenarios (boot-only, smoke-only,
// deferred, and provider registration-only outside registry paths) do
// not mark an entry as covered.
//
// Usage:
//
//	go run ./parity/cmd/familygaps                       # all families
//	go run ./parity/cmd/familygaps -family slash-commands # one family
//	go run ./parity/cmd/familygaps -strict               # exit 1 if any family has untested entries
//
// Output for a single family:
//
//	Family: slash-commands
//	Scope: 2 PORT_MAP entries
//	Description: Slash command registry, dispatch, and individual command handlers.
//
//	In-family scenarios: 4
//
//	✅ Behaviorally covered by in-family scenario (1):
//	   packages/coding-agent/src/core/slash-commands.ts → 4 scenario(s)
//
//	🟡 Covered only by other-family scenario (0):
//
//	⬜ Untested (1):
//	   packages/coding-agent/src/utils/changelog.ts
//
// Run at the start of a family loop to see the scope; run at the
// end to verify no behaviorally-required entry in scope was silently skipped. Every entry
// in the ⬜ section must either be covered before the loop ends OR
// justified in the commit message with one of:
//
//   - covered elsewhere (cite the scenario)
//   - numbered divergence (cite the DN)
//   - explicit user deferral
//   - undefined upstream behavior (cite probe evidence)
//
// "Silent skip" is no longer allowed.
package main

import (
	"cmp"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

type familiesConfig struct {
	Families map[string]familyDef `toml:"families"`
}

type familyDef struct {
	Description     string   `toml:"description"`
	Includes        []string `toml:"includes"`
	RequireInFamily bool     `toml:"require_in_family"`
}

type scenarioFile struct {
	Name        string   `toml:"name"`
	Description string   `toml:"description"`
	Covers      []string `toml:"covers"`
	Tags        []string `toml:"tags"`
	Family      string   `toml:"-"`
	Source      string   `toml:"-"`
}

func main() {
	familyName := flag.String("family", "", "report on a single family (default: all)")
	familiesPath := flag.String("families", "parity/families.toml", "families config")
	portMap := flag.String("port-map", "PORT_MAP.md", "PORT_MAP.md path")
	scenariosDir := flag.String("scenarios", "parity/scenarios", "scenarios dir")
	strict := flag.Bool("strict", false, "exit 1 if any family has untested entries or any ported row has no family")
	flag.Parse()

	fams, err := loadFamilies(*familiesPath)
	if err != nil {
		fail("load families: %v", err)
	}
	entries, err := parsePortMap(*portMap)
	if err != nil {
		fail("parse port map: %v", err)
	}
	scenarios, err := loadScenarios(*scenariosDir)
	if err != nil {
		fail("load scenarios: %v", err)
	}

	names := slices.Sorted(mapKeys(fams.Families))
	if *familyName != "" {
		if _, ok := fams.Families[*familyName]; !ok {
			fail("unknown family %q (declared in %s: %v)", *familyName, *familiesPath, names)
		}
		names = []string{*familyName}
	}

	unassigned := unassignedEntries(entries, fams.Families)
	if *familyName == "" && len(unassigned) > 0 {
		fmt.Printf("Unassigned ported PORT_MAP entries (%d):\n", len(unassigned))
		for _, entry := range unassigned {
			fmt.Printf("   %s\n", entry)
		}
		fmt.Println()
		fmt.Println(strings.Repeat("─", 60))
		fmt.Println()
	}

	anyUntested := len(unassigned) > 0 && *familyName == ""
	for i, name := range names {
		if i > 0 {
			fmt.Println()
			fmt.Println(strings.Repeat("─", 60))
			fmt.Println()
		}
		untested := reportFamily(name, fams.Families[name], entries, scenarios)
		if untested > 0 {
			anyUntested = true
		}
	}

	if *strict && anyUntested {
		fmt.Fprintln(os.Stderr, "\nfamilygaps: untested family entries or ported rows without a family remain (strict mode)")
		os.Exit(1)
	}

	// Final reminder. The reminder is louder than usual here because
	// family-gaps reports a file-level metric: it can show zero ⬜
	// while behavioral surfaces inside "covered" files remain
	// smoke-tested. Reading the reminder is mandatory before assuming
	// a family is done.
	fmt.Println()
	if !anyUntested {
		fmt.Println("family-gaps: zero ⬜ untested files across all reported families.")
		fmt.Println("WARNING: zero ⬜ ≠ zero behavior gaps. A file is reported as ✅")
		fmt.Println("covered if ANY scenario asserts ANY of its behavior. Behavioral")
		fmt.Println("surfaces inside covered files (branches, error paths, edge cases)")
		fmt.Println("are NOT tracked here. If your loop produced one scenario per file")
		fmt.Println("and stopped, the file-level metric is misleading you.")
	}
}

func unassignedEntries(entries []portMapEntry, families map[string]familyDef) []string {
	var unassigned []string
	for _, entry := range entries {
		if entry.Status == "n/a" || entry.Status == "⏸" {
			continue
		}
		assigned := false
		for _, family := range families {
			if matchesAny(entry.Path, family.Includes) {
				assigned = true
				break
			}
		}
		if !assigned {
			unassigned = append(unassigned, entry.Path)
		}
	}
	slices.Sort(unassigned)
	return unassigned
}

func reportFamily(name string, def familyDef, entries []portMapEntry, scenarios []scenarioFile) int {
	// 1. Resolve scope: intended-portable PORT_MAP entries matching any include pattern.
	var scope []portMapEntry
	for _, entry := range entries {
		if entry.Status != "n/a" && entry.Status != "⏸" && matchesAny(entry.Path, def.Includes) {
			scope = append(scope, entry)
		}
	}
	slices.SortFunc(scope, func(a, b portMapEntry) int { return cmp.Compare(a.Path, b.Path) })

	// 2. Index coverage per upstream file.
	type cov struct {
		inFamily   []string // scenario names from THIS family
		otherFamil []string // scenario names from OTHER families
	}
	idx := make(map[string]*cov)
	for _, entry := range scope {
		idx[entry.Path] = &cov{}
	}
	for _, sc := range scenarios {
		for _, c := range sc.Covers {
			if !isBehavioralCoverage(sc, c) {
				continue
			}
			rec, ok := idx[c]
			if !ok {
				continue
			}
			if sc.Family == name {
				rec.inFamily = append(rec.inFamily, sc.Name)
			} else {
				rec.otherFamil = append(rec.otherFamil, sc.Name)
			}
		}
	}

	// 3. Count in-family scenarios and behavioral scenarios separately.
	inFamilyCount, inFamilyBehavioral := 0, 0
	for _, sc := range scenarios {
		if sc.Family == name {
			inFamilyCount++
			if isBehavioralScenario(sc) {
				inFamilyBehavioral++
			}
		}
	}

	// 4. Bucket.
	var covIn, covOther, untested []string
	for _, entry := range scope {
		rec := idx[entry.Path]
		switch {
		case entry.Status != "✅":
			untested = append(untested, fmt.Sprintf("%s [%s]", entry.Path, entry.Status))
		case len(rec.inFamily) > 0:
			covIn = append(covIn, entry.Path)
		case len(rec.otherFamil) > 0 && !def.RequireInFamily:
			covOther = append(covOther, entry.Path)
		default:
			untested = append(untested, entry.Path)
		}
	}

	// 5. Render.
	fmt.Printf("Family: %s\n", name)
	fmt.Printf("Scope: %d PORT_MAP entries\n", len(scope))
	if def.Description != "" {
		fmt.Printf("Description: %s\n", def.Description)
	}
	fmt.Println()
	fmt.Printf("In-family scenarios: %d (%d behavioral)\n", inFamilyCount, inFamilyBehavioral)
	fmt.Println()

	fmt.Printf("✅ Behaviorally covered by in-family scenario (%d):\n", len(covIn))
	for _, e := range covIn {
		rec := idx[e]
		slices.Sort(rec.inFamily)
		rec.inFamily = slices.Compact(rec.inFamily)
		fmt.Printf("   %s → %d scenario(s)\n", e, len(rec.inFamily))
		for _, s := range rec.inFamily {
			fmt.Printf("     - %s\n", s)
		}
	}
	fmt.Println()

	fmt.Printf("🟡 Behaviorally covered only by other-family scenario (%d):\n", len(covOther))
	for _, e := range covOther {
		rec := idx[e]
		slices.Sort(rec.otherFamil)
		rec.otherFamil = slices.Compact(rec.otherFamil)
		fmt.Printf("   %s → %v\n", e, rec.otherFamil)
	}
	fmt.Println()

	fmt.Printf("⬜ Untested (%d):\n", len(untested))
	for _, e := range untested {
		fmt.Printf("   %s\n", e)
	}

	return len(untested)
}

// matchesAny returns true if path matches any include pattern.
// A pattern ending in "/" matches as a prefix (directory). All other
// patterns must match exactly.
func isBehavioralScenario(sc scenarioFile) bool {
	return scenarioQuality(sc) == "behavioral"
}

func isBehavioralCoverage(sc scenarioFile, cover string) bool {
	switch scenarioQuality(sc) {
	case "behavioral":
		return true
	case "registration-only":
		return isRegistrationCoveragePath(cover)
	default:
		return false
	}
}

func scenarioQuality(sc scenarioFile) string {
	switch {
	case slices.Contains(sc.Tags, "deferred"):
		return "deferred"
	case strings.HasPrefix(sc.Description, "boot-only:") || slices.Contains(sc.Tags, "boot-only"):
		return "boot-only"
	case slices.Contains(sc.Tags, "registration-only"):
		return "registration-only"
	case slices.Contains(sc.Tags, "smoke-only"):
		return "smoke-only"
	default:
		return "behavioral"
	}
}

func isRegistrationCoveragePath(path string) bool {
	switch path {
	case "packages/ai/src/api-registry.ts",
		"packages/ai/src/env-api-keys.ts",
		"packages/ai/src/providers/register-builtins.ts",
		"packages/coding-agent/src/core/auth-storage.ts",
		"packages/coding-agent/src/core/resolve-config-value.ts":
		return true
	default:
		return false
	}
}

func matchesAny(path string, patterns []string) bool {
	for _, p := range patterns {
		if strings.HasSuffix(p, "/") {
			if strings.HasPrefix(path, p) {
				return true
			}
			continue
		}
		if path == p {
			return true
		}
	}
	return false
}

func loadFamilies(path string) (familiesConfig, error) {
	var cfg familiesConfig
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

var portMapRowRE = regexp.MustCompile("^\\|\\s+`([^`]+)`\\s+\\|\\s+`([^`]*)`\\s+\\|\\s+(✅|🟡|⬜|⏸|🔴|n/a)\\s+\\|\\s*$")

type portMapEntry struct {
	Path   string
	Status string
}

func parsePortMap(path string) ([]portMapEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []portMapEntry
	for line := range strings.SplitSeq(string(data), "\n") {
		m := portMapRowRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		out = append(out, portMapEntry{Path: m[1], Status: m[3]})
	}
	return out, nil
}

func loadScenarios(dir string) ([]scenarioFile, error) {
	var out []scenarioFile
	absRoot, _ := filepath.Abs(dir)
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".toml") {
			return nil
		}
		var sc scenarioFile
		if _, decErr := toml.DecodeFile(path, &sc); decErr != nil {
			return fmt.Errorf("%s: %w", path, decErr)
		}
		sc.Source = path
		abs, _ := filepath.Abs(path)
		rel, _ := filepath.Rel(absRoot, abs)
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) >= 2 {
			sc.Family = parts[0]
		} else {
			sc.Family = "_top"
		}
		out = append(out, sc)
		return nil
	})
	slices.SortFunc(out, func(a, b scenarioFile) int { return cmp.Compare(a.Source, b.Source) })
	return out, err
}

func mapKeys[K comparable, V any](m map[K]V) func(yield func(K) bool) {
	return func(yield func(K) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "familygaps: "+format+"\n", args...)
	os.Exit(1)
}
