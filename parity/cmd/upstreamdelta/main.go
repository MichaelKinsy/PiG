// Command upstreamdelta proves that every changed upstream source file in a
// version leap received an explicit disposition and durable evidence.
package main

import (
	"bytes"
	"cmp"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/MichaelKinsy/PiG/coding"
)

var trackedRoots = []string{
	"packages/agent/src",
	"packages/ai/src",
	"packages/coding-agent/src",
	"packages/tui/src",
}

type syncManifest struct {
	From  string      `toml:"from"`
	To    string      `toml:"to"`
	Files []fileAudit `toml:"files"`
}

type fileAudit struct {
	Path        string   `toml:"path"`
	Change      string   `toml:"change"`
	Disposition string   `toml:"disposition"`
	Evidence    []string `toml:"evidence"`
	Rationale   string   `toml:"rationale"`
}

type sourceDelta struct {
	Path   string
	Change string
}

func main() {
	manifestPath := flag.String("manifest", "", "sync manifest (default: parity/upstream-sync/v<current pin>.toml)")
	upstreamRoot := flag.String("upstream-root", ".upstream", "directory containing version mirrors")
	repoRoot := flag.String("repo-root", ".", "repository root used to validate evidence paths")
	generate := flag.Bool("generate", false, "print a pending manifest for the requested version range")
	from := flag.String("from", "", "source version for -generate")
	to := flag.String("to", "", "target version for -generate")
	flag.Parse()

	if *generate {
		if *from == "" || *to == "" {
			fail("-generate requires -from and -to")
		}
		deltas, err := computeDelta(filepath.Join(*upstreamRoot, "v"+*from), filepath.Join(*upstreamRoot, "v"+*to))
		if err != nil {
			fail("compute delta: %v", err)
		}
		writePendingManifest(*from, *to, deltas)
		return
	}

	if *manifestPath == "" {
		*manifestPath = filepath.Join("parity", "upstream-sync", "v"+coding.UpstreamVersion+".toml")
	}
	var manifest syncManifest
	if _, err := toml.DecodeFile(*manifestPath, &manifest); err != nil {
		fail("load %s: %v", *manifestPath, err)
	}
	if manifest.To != coding.UpstreamVersion {
		fail("manifest target %q does not match coding.UpstreamVersion %q", manifest.To, coding.UpstreamVersion)
	}
	deltas, err := computeDelta(filepath.Join(*upstreamRoot, "v"+manifest.From), filepath.Join(*upstreamRoot, "v"+manifest.To))
	if err != nil {
		fail("compute delta: %v", err)
	}
	problems := validateManifest(manifest, deltas, *repoRoot)
	if len(problems) > 0 {
		for _, problem := range problems {
			fmt.Fprintln(os.Stderr, "upstream-delta:", problem)
		}
		os.Exit(1)
	}
	fmt.Printf("upstream-delta: clean (%d changed source files audited for %s -> %s)\n", len(deltas), manifest.From, manifest.To)
}

func computeDelta(fromRoot, toRoot string) ([]sourceDelta, error) {
	fromFiles, err := discoverSources(fromRoot)
	if err != nil {
		return nil, err
	}
	toFiles, err := discoverSources(toRoot)
	if err != nil {
		return nil, err
	}
	paths := make(map[string]struct{}, len(fromFiles)+len(toFiles))
	for path := range fromFiles {
		paths[path] = struct{}{}
	}
	for path := range toFiles {
		paths[path] = struct{}{}
	}
	var deltas []sourceDelta
	for path := range paths {
		fromBody, inFrom := fromFiles[path]
		toBody, inTo := toFiles[path]
		change := ""
		switch {
		case !inFrom:
			change = "added"
		case !inTo:
			change = "removed"
		case !bytes.Equal(fromBody, toBody):
			change = "modified"
		}
		if change != "" {
			deltas = append(deltas, sourceDelta{Path: path, Change: change})
		}
	}
	slices.SortFunc(deltas, func(a, b sourceDelta) int { return cmp.Compare(a.Path, b.Path) })
	return deltas, nil
}

func discoverSources(root string) (map[string][]byte, error) {
	files := make(map[string][]byte)
	for _, trackedRoot := range trackedRoots {
		base := filepath.Join(root, filepath.FromSlash(trackedRoot))
		if _, err := os.Stat(base); err != nil {
			return nil, fmt.Errorf("tracked root %s: %w", base, err)
		}
		err := filepath.WalkDir(base, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !isSourceFile(entry.Name()) || isTestPath(path) {
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			files[filepath.ToSlash(rel)] = body
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return files, nil
}

func isSourceFile(name string) bool {
	return strings.HasSuffix(name, ".ts") || strings.HasSuffix(name, ".tsx")
}

func isTestPath(path string) bool {
	slashPath := filepath.ToSlash(path)
	name := filepath.Base(path)
	if strings.HasSuffix(name, ".test.ts") || strings.HasSuffix(name, ".spec.ts") || strings.HasSuffix(name, ".test.tsx") || strings.HasSuffix(name, ".spec.tsx") {
		return true
	}
	return strings.Contains(slashPath, "/test/") || strings.Contains(slashPath, "/tests/") || strings.Contains(slashPath, "/__tests__/")
}

func validateManifest(manifest syncManifest, deltas []sourceDelta, repoRoot string) []string {
	if manifest.From == "" || manifest.To == "" || manifest.From == manifest.To {
		return []string{"manifest requires distinct non-empty from and to versions"}
	}
	expected := make(map[string]string, len(deltas))
	for _, delta := range deltas {
		expected[delta.Path] = delta.Change
	}
	seen := make(map[string]struct{}, len(manifest.Files))
	var problems []string
	for _, audit := range manifest.Files {
		if _, duplicate := seen[audit.Path]; duplicate {
			problems = append(problems, fmt.Sprintf("duplicate file audit %s", audit.Path))
			continue
		}
		seen[audit.Path] = struct{}{}
		change, exists := expected[audit.Path]
		if !exists {
			problems = append(problems, fmt.Sprintf("stale file audit %s", audit.Path))
			continue
		}
		if audit.Change != change {
			problems = append(problems, fmt.Sprintf("%s change is %q, want %q", audit.Path, audit.Change, change))
		}
		problems = append(problems, validateAudit(audit, repoRoot)...)
	}
	for _, delta := range deltas {
		if _, exists := seen[delta.Path]; !exists {
			problems = append(problems, fmt.Sprintf("missing file audit %s [%s]", delta.Path, delta.Change))
		}
	}
	slices.Sort(problems)
	return problems
}

func validateAudit(audit fileAudit, repoRoot string) []string {
	var problems []string
	switch audit.Disposition {
	case "ported":
		if len(audit.Evidence) == 0 {
			problems = append(problems, fmt.Sprintf("%s disposition %q requires evidence", audit.Path, audit.Disposition))
		}
		if strings.TrimSpace(audit.Rationale) == "" {
			problems = append(problems, fmt.Sprintf("%s disposition %q requires a delta-specific evidence rationale", audit.Path, audit.Disposition))
		}
	case "unchanged-observable":
		if len(audit.Evidence) == 0 {
			problems = append(problems, fmt.Sprintf("%s disposition %q requires evidence", audit.Path, audit.Disposition))
		}
	case "designed-out", "deferred":
		if strings.TrimSpace(audit.Rationale) == "" {
			problems = append(problems, fmt.Sprintf("%s disposition %q requires a rationale", audit.Path, audit.Disposition))
		}
	case "pending":
		problems = append(problems, fmt.Sprintf("%s remains pending", audit.Path))
	default:
		problems = append(problems, fmt.Sprintf("%s has unknown disposition %q", audit.Path, audit.Disposition))
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

func writePendingManifest(from, to string, deltas []sourceDelta) {
	fmt.Printf("from = %q\nto = %q\n", from, to)
	for _, delta := range deltas {
		fmt.Printf("\n[[files]]\npath = %q\nchange = %q\ndisposition = %q\nevidence = []\nrationale = %q\n", delta.Path, delta.Change, "pending", "")
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "upstream-delta: "+format+"\n", args...)
	os.Exit(1)
}
