//go:build ignore

// Command gen writes tables.go and tables.bin of package collate: the root-locale part of the tables of golang.org/x/text/collate at the version
// go.mod requires, renumbered densely (see tablegen), and the same tables compressed. Run by `go generate`; TestTablesMatchXText fails when
// they and x/text disagree.
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
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}

func run() error {
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}} {{.Version}}", "golang.org/x/text").Output()
	if err != nil {
		return err
	}
	dir, version, _ := strings.Cut(strings.TrimSpace(string(out)), " ")
	tables, err := os.ReadFile(filepath.Join(dir, "collate", "tables.go"))
	if err != nil {
		return err
	}
	generated, err := tablegen.Generate(tables, "golang.org/x/text@"+version+"/collate/tables.go")
	if err != nil {
		return err
	}
	compressed, err := tablegen.Compress(generated.Raw)
	if err != nil {
		return err
	}
	if err := os.WriteFile("tables.bin", compressed, 0o644); err != nil {
		return err
	}
	return os.WriteFile("tables.go", generated.Source, 0o644)
}
