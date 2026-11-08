//go:build ignore

// Command gen writes tables.go of package collate: the root-locale part of the tables of golang.org/x/text/collate at the version go.mod
// requires, renumbered densely (see tablegen). Run by `go generate`; TestTablesMatchXText fails when tables.go and x/text disagree.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/third_party/collate/tablegen"
)

func main() {
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}} {{.Version}}", "golang.org/x/text").Output()
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
	dir, version, _ := strings.Cut(strings.TrimSpace(string(out)), " ")
	tables, err := os.ReadFile(filepath.Join(dir, "collate", "tables.go"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
	generated, err := tablegen.Generate(tables, "golang.org/x/text@"+version+"/collate/tables.go")
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
	if err := os.WriteFile("tables.go", generated, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}
