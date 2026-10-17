// Command testprune inventories and classifies PiG's tests, measures the
// coverage a deletion batch would lose, and enforces the provenance ratchet
// for golden files and for expectations edited together with the code they test.
//
//	testprune inventory [-out dir]          classify every Go test function
//	testprune history   [-out dir]          find expectations edited in the same commit as code
//	testprune lint -base REV [-head REV]    fail on undeclared goldens and unjustified co-edited expectations
//	testprune coverdiff -pkg P -skip RE     prove a deletion batch loses no coverage
//	testprune redundant -pkg P              list tests whose coverage other tests repeat
//	testprune mutate -pkg P -out FILE       run sampled mutants against the suite
//	testprune mutcompare BEFORE AFTER       fail when pruning left a killed mutant alive
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	root, err := repoRoot()
	if err != nil {
		fatal(err)
	}
	cmd, args := os.Args[1], os.Args[2:]
	switch cmd {
	case "inventory":
		err = runInventory(root, args)
	case "history":
		err = runHistory(root, args)
	case "lint":
		err = runLint(root, args)
	case "coverdiff":
		err = runCoverDiff(root, args)
	case "redundant":
		err = runRedundant(root, args)
	case "journeycover":
		err = runJourneyCover(root, args)
	case "mutate":
		err = runMutate(root, args)
	case "mutcompare":
		err = runMutantCompare(args)
	default:
		usage()
	}
	if err != nil {
		fatal(err)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: testprune inventory|history|lint|coverdiff|redundant|mutate|mutcompare [flags]")
	os.Exit(2)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "testprune:", err)
	os.Exit(1)
}

func repoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// loadUnitEvidence returns "<pkgdir>|<TestName>" for every test named by a
// reviewed, mutation-proven entry in test/parity/unit-evidence.
func loadUnitEvidence(root string) (map[string]bool, error) {
	paths, err := filepath.Glob(filepath.Join(root, "test/parity/unit-evidence/*.json"))
	if err != nil {
		return nil, err
	}
	keys := map[string]bool{}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var entries []struct {
			Package string   `json:"package"`
			Tests   []string `json:"tests"`
		}
		if json.Unmarshal(data, &entries) != nil {
			continue
		}
		for _, e := range entries {
			for _, t := range e.Tests {
				keys[strings.TrimPrefix(e.Package, "./")+"|"+t] = true
			}
		}
	}
	return keys, nil
}

func runInventory(root string, args []string) error {
	fs := flag.NewFlagSet("inventory", flag.ExitOnError)
	out := fs.String("out", "tmp/test-prune", "output directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	files, err := listTestFiles(root)
	if err != nil {
		return err
	}
	evidence, err := loadUnitEvidence(root)
	if err != nil {
		return err
	}
	var prov provenance
	if _, err := toml.DecodeFile(filepath.Join(root, provenanceFile), &prov); err == nil {
		for _, g := range prov.Golden {
			for _, p := range g.Paths {
				if g.Owner == "pi" {
					piGoldenBases = append(piGoldenBases, filepath.Base(p))
				}
			}
		}
	}
	funcs, err := inventoryFiles(root, files, evidence)
	if err != nil {
		return err
	}
	dir := filepath.Join(root, *out)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("path\tname\tkind\tline\tlines\ttags\tclass\tasserts\tliterals\tsignals\n")
	for _, f := range funcs {
		fmt.Fprintf(&b, "%s\t%s\t%s\t%d\t%d\t%s\t%s\t%d\t%d\t%s\n", f.Path, f.Name, f.Kind, f.Line, f.Lines, f.Tags, f.Class, f.Asserts, f.Literals, strings.Join(f.Signals, ","))
	}
	if err := os.WriteFile(filepath.Join(dir, "inventory.tsv"), []byte(b.String()), 0o644); err != nil {
		return err
	}
	others, err := inventoryOther(root)
	if err != nil {
		return err
	}
	var ob strings.Builder
	ob.WriteString("path\tlanguage\tcases\tclass\tsignals\n")
	for _, o := range others {
		fmt.Fprintf(&ob, "%s\t%s\t%d\t%s\t%s\n", o.Path, o.Language, o.Cases, o.Class, strings.Join(o.Signals, ","))
	}
	if err := os.WriteFile(filepath.Join(dir, "inventory-other.tsv"), []byte(ob.String()), 0o644); err != nil {
		return err
	}
	fmt.Print(summarize(funcs))
	fmt.Print(summarizeOther(others))
	return nil
}

func summarize(funcs []testFunc) string {
	type counts map[Class]int
	byPkg := map[string]counts{}
	total := counts{}
	for _, f := range funcs {
		if byPkg[f.Pkg] == nil {
			byPkg[f.Pkg] = counts{}
		}
		byPkg[f.Pkg][f.Class]++
		total[f.Class]++
	}
	classes := []Class{ClassOracle, ClassPiCited, ClassInvariant, ClassSelfRef, ClassShape, ClassUnclassified}
	var b strings.Builder
	fmt.Fprintf(&b, "tests: %d\n", len(funcs))
	for _, c := range classes {
		fmt.Fprintf(&b, "  %-14s %d\n", c, total[c])
	}
	pkgs := make([]string, 0, len(byPkg))
	for p := range byPkg {
		pkgs = append(pkgs, p)
	}
	sort.Slice(pkgs, func(i, j int) bool {
		return byPkg[pkgs[i]][ClassSelfRef]+byPkg[pkgs[i]][ClassShape] > byPkg[pkgs[j]][ClassSelfRef]+byPkg[pkgs[j]][ClassShape]
	})
	b.WriteString("\npackage\tA-oracle\tA-cited\tB-invariant\tC-selfref\tD-shape\tU-example\n")
	for _, p := range pkgs {
		c := byPkg[p]
		fmt.Fprintf(&b, "%s", p)
		for _, k := range classes {
			fmt.Fprintf(&b, "\t%d", c[k])
		}
		b.WriteByte('\n')
	}
	return b.String()
}
