package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/MichaelKinsy/PiG/coding"
)

const vectorsDir = "test/parity/sdk-vectors"

// vectorModuleDir is the directory name of a ledger module: "/" becomes "-".
func vectorModuleDir(module string) string { return strings.ReplaceAll(module, "/", "-") }

type vectorCase struct {
	Name   string           `json:"name"`
	Args   *json.RawMessage `json:"args"`
	Result *json.RawMessage `json:"result"`
	Throws *string          `json:"throws"`
}

type vectorFile struct {
	Pin     string       `json:"pin"`
	Package string       `json:"package"`
	Module  string       `json:"module"`
	Export  string       `json:"export"`
	Cases   []vectorCase `json:"cases"`
}

// checkVectors validates the Pi vectors under root/test/parity/sdk-vectors:
// each file is well formed, was generated at the current pin from a checked-in
// case source, and names an export of the ledger's denominator. Fewer
// vectors than exports is fine (they arrive with the helper lanes); a vector
// that cannot be trusted is not.
func checkVectors(root string, rows []ledgerRow, pin string) []string {
	var problems []string
	add := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }
	keys := map[string]ledgerRow{}
	dirs := map[string]string{} // module dir -> module
	for _, r := range rows {
		keys[r.Key] = r
		dirs[vectorModuleDir(r.Module)] = r.Module
	}
	base := filepath.Join(root, vectorsDir)
	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(base, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() || rel == "go.mod" || strings.HasSuffix(rel, ".md") || strings.HasPrefix(rel, "cases/") && strings.HasSuffix(rel, ".mjs") {
			if !d.IsDir() && strings.HasPrefix(rel, "cases/") {
				parts := strings.Split(rel, "/")
				if len(parts) != 3 {
					add("%s/%s: a case source is cases/<module-dir>/<export>.mjs", vectorsDir, rel)
				} else if _, ok := dirs[parts[1]]; !ok {
					add("%s/%s: %q is not a served module directory", vectorsDir, rel, parts[1])
				} else if _, ok := keys[dirs[parts[1]]+": "+strings.TrimSuffix(parts[2], ".mjs")]; !ok {
					add("%s/%s: names no export of %s in the ledger", vectorsDir, rel, dirs[parts[1]])
				} else if _, err := os.Stat(filepath.Join(base, parts[1], strings.TrimSuffix(parts[2], ".mjs")+".json")); err != nil {
					add("%s/%s: its vector file is not generated: run node automation/gen/generate-sdk-vectors.mjs", vectorsDir, rel)
				}
			}
			return nil
		}
		parts := strings.Split(rel, "/")
		if len(parts) != 2 || !strings.HasSuffix(parts[1], ".json") {
			add("%s/%s: a vector file is <module-dir>/<export>.json", vectorsDir, rel)
			return nil
		}
		module, ok := dirs[parts[0]]
		export := strings.TrimSuffix(parts[1], ".json")
		if !ok {
			add("%s/%s: %q is not a served module directory", vectorsDir, rel, parts[0])
			return nil
		}
		if _, ok := keys[module+": "+export]; !ok {
			add("%s/%s: names no export of %s in the ledger", vectorsDir, rel, module)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var f vectorFile
		var strict map[string]json.RawMessage
		if json.Unmarshal(raw, &f) != nil || json.Unmarshal(raw, &strict) != nil {
			add("%s/%s: not a vector file (invalid JSON)", vectorsDir, rel)
			return nil
		}
		if f.Pin != pin {
			add("%s/%s: generated at pin %q, the current pin is %q: re-run node automation/gen/generate-sdk-vectors.mjs", vectorsDir, rel, f.Pin, pin)
		}
		if f.Module != parts[0] || f.Export != export || f.Package == "" {
			add("%s/%s: module, export and package must match the path (module %q, export %q, package %q)", vectorsDir, rel, f.Module, f.Export, f.Package)
		}
		if _, err := os.Stat(filepath.Join(base, "cases", parts[0], export+".mjs")); err != nil {
			add("%s/%s: no case source cases/%s/%s.mjs: a vector is generated from Pi, never hand-written", vectorsDir, rel, parts[0], export)
		}
		if len(f.Cases) == 0 {
			add("%s/%s: no cases", vectorsDir, rel)
		}
		seen := map[string]bool{}
		for i, c := range f.Cases {
			switch {
			case c.Name == "" || seen[c.Name]:
				add("%s/%s: case %d needs a unique name", vectorsDir, rel, i)
			case c.Args == nil || !strings.HasPrefix(strings.TrimSpace(string(*c.Args)), "["):
				add("%s/%s: case %q needs an args array", vectorsDir, rel, c.Name)
			case (c.Result == nil) == (c.Throws == nil):
				add("%s/%s: case %q needs exactly one of result and throws", vectorsDir, rel, c.Name)
			}
			seen[c.Name] = true
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		add("%s: %v", vectorsDir, err)
	}
	sort.Strings(problems)
	return problems
}

func currentPin() string { return coding.UpstreamVersion }
