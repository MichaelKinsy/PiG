package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"go/token"
	"go/types"
	"os"
	"runtime"
	"slices"
	"strings"

	"golang.org/x/tools/go/packages"
)

type inventory struct {
	GoVersion  string            `json:"goVersion"`
	Packages   []string          `json:"packages"`
	Interfaces []interfaceRecord `json:"interfaces"`
}

type interfaceRecord struct {
	ID        string          `json:"id"`
	Package   string          `json:"package"`
	Name      string          `json:"name"`
	Kind      string          `json:"kind"`
	Exported  bool            `json:"exported"`
	Shape     json.RawMessage `json:"shape"`
	ShapeHash string          `json:"shapeHash"`
}

type shape struct {
	Type string `json:"type"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "go-interface-extractor:", err)
		os.Exit(1)
	}
}

func run() error {
	out := flag.String("out", "-", "output JSON path or - for stdout")
	flag.Parse()
	patterns := flag.Args()
	if len(patterns) == 0 {
		patterns = []string{
			"./agent/...", "./ai/...", "./coding/...", "./extensions/sdk/...",
			"./internal/codingagent/...", "./tui/...", "./cmd/pig",
		}
	}
	result, err := extract(patterns)
	if err != nil {
		return err
	}
	writer := os.Stdout
	if *out != "-" {
		writer, err = os.Create(*out)
		if err != nil {
			return err
		}
		defer func() { _ = writer.Close() }()
	}
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return err
	}
	return nil
}

func extract(patterns []string) (inventory, error) {
	cfg := &packages.Config{Mode: packages.NeedName | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedDeps}
	loaded, err := packages.Load(cfg, patterns...)
	if err != nil {
		return inventory{}, err
	}
	if packages.PrintErrors(loaded) > 0 {
		return inventory{}, fmt.Errorf("load exported Go packages")
	}
	result := inventory{GoVersion: languageVersion(runtimeVersion())}
	seen := map[string]struct{}{}
	for _, pkg := range loaded {
		if !candidatePackage(pkg.PkgPath) {
			continue
		}
		result.Packages = append(result.Packages, pkg.PkgPath)
		records := extractPackage(pkg.Types)
		for _, record := range records {
			if _, duplicate := seen[record.ID]; duplicate {
				return inventory{}, fmt.Errorf("duplicate Go interface ID %s", record.ID)
			}
			seen[record.ID] = struct{}{}
			result.Interfaces = append(result.Interfaces, record)
		}
	}
	slices.Sort(result.Packages)
	slices.SortFunc(result.Interfaces, func(a, b interfaceRecord) int { return strings.Compare(a.ID, b.ID) })
	return result, nil
}

func candidatePackage(pkgPath string) bool {
	for _, segment := range []string{"/extensiontest", "/testdata", "/examples", "/parity", "/termsim"} {
		if strings.Contains(pkgPath, segment) {
			return false
		}
	}
	return true
}

var runtimeVersion = runtime.Version

// languageVersion reduces a toolchain version to its language version, so the
// inventory records "1.26" rather than "1.26.1".
//
// The inventory is an account of Pig's exported Go surface, which a compiler
// patch release does not change. Recording the patch made the drift gate depend
// on which toolchain happened to generate the file: CI builds in a pinned image
// (1.26.6) while developers run whatever their version manager installed, so
// the committed file could satisfy one and never the other. That is why
// interface-go-drift failed in CI on a file that was clean locally.
func languageVersion(v string) string {
	v = strings.TrimPrefix(v, "go")
	major, rest, ok := strings.Cut(v, ".")
	if !ok {
		return v
	}
	minor, _, _ := strings.Cut(rest, ".")
	return major + "." + minor
}

func extractPackage(pkg *types.Package) []interfaceRecord {
	scope := pkg.Scope()
	qualifier := func(other *types.Package) string {
		if other == pkg {
			return ""
		}
		return other.Path()
	}
	var records []interfaceRecord
	for _, name := range scope.Names() {
		object := scope.Lookup(name)
		records = append(records, recordForObject(pkg.Path(), name, object, qualifier))
		named, ok := object.Type().(*types.Named)
		if !ok {
			continue
		}
		methodSet := types.NewMethodSet(types.NewPointer(named))
		for selection := range methodSet.Methods() {
			method := selection.Obj()
			records = append(records, recordForObject(pkg.Path(), name+"."+method.Name(), method, qualifier))
		}
	}
	return records
}

func recordForObject(pkgPath, name string, object types.Object, qualifier types.Qualifier) interfaceRecord {
	kind := "symbol"
	switch object.(type) {
	case *types.Func:
		kind = "function"
	case *types.TypeName:
		kind = "type"
	case *types.Const:
		kind = "constant"
	case *types.Var:
		kind = "variable"
	}
	canonical := shape{Type: types.TypeString(object.Type(), qualifier)}
	encoded, _ := json.Marshal(canonical)
	sum := sha256.Sum256(encoded)
	return interfaceRecord{
		ID: "go:" + pkgPath + "#" + name, Package: pkgPath, Name: name, Kind: kind, Exported: token.IsExported(object.Name()),
		Shape: encoded, ShapeHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}
