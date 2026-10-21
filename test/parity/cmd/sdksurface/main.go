// Command sdksurface generates docs/extension-sdk-surface.md: every
// extension-facing surface of Pi's extension API, read from the pinned
// upstream types.ts and the declarations it re-exports or exposes, with one
// column per PiG extension runtime (the Node runtime and the Go, Rust and
// Python SDKs).
//
// A cell's status comes from symbol detection in that runtime's source: the
// Go SDK's exported identifiers (go/ast), the Rust SDK's pub items, the
// Python SDK's classes and members, and the Node runtime's classes, `pi`
// object keys and property reads. test/parity/sdk-surface.toml names which symbol
// realizes a surface where the language's naming differs from the default
// rule, and why a realization is a stand-in; it never decides a status on its
// own. Fields that cross the subprocess wire are also checked against the
// host's Go decoding types, so a field the SDK sends but the host drops is
// missing.
//
//	go run ./test/parity/cmd/sdksurface          # rewrite the matrix
//	go run ./test/parity/cmd/sdksurface -check   # fail if it is stale
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	root := flag.String("root", ".", "repository root")
	check := flag.Bool("check", false, "fail if the checked-in matrix is stale or has unexcepted missing cells")
	release := flag.Bool("release", false, "with -check, also apply the release gate: a gap on a high-use export fails")
	flag.Parse()
	if *release && !*check {
		fmt.Fprintln(os.Stderr, "sdksurface: -release needs -check")
		os.Exit(2)
	}
	if err := run(*root, *check, *release); err != nil {
		fmt.Fprintln(os.Stderr, "sdksurface:", err)
		os.Exit(1)
	}
}

func run(root string, check, release bool) error {
	m, err := buildMatrix(root)
	if err != nil {
		return err
	}
	out := m.render()
	path := filepath.Join(root, matrixPath)
	corpora, err := loadCorpora(root)
	if err != nil {
		return err
	}
	gaps, err := loadGaps(root)
	if err != nil {
		return err
	}
	rows := m.consistencyRows(corpora)
	consistency := renderConsistency(rows, gaps)
	consistencyFile := filepath.Join(root, consistencyPath)
	in, err := loadLedgerInput(root, m, corpora, !check)
	if err != nil {
		return err
	}
	in.Release = release
	typeOnly, err := typeOnlyCounts(root, in.Rows)
	if err != nil {
		return err
	}
	ledgerFileText := regenerateLedger(in)
	exportsDoc := renderExportsDoc(in, typeOnly)
	if check {
		for _, f := range []struct {
			path string
			want []byte
		}{{path, out}, {consistencyFile, consistency}, {filepath.Join(root, ledgerPath), ledgerFileText}, {filepath.Join(root, exportsDocPath), exportsDoc}} {
			current, err := os.ReadFile(f.path)
			if err != nil {
				return err
			}
			if !bytes.Equal(current, f.want) {
				return fmt.Errorf("%s is stale: run `go run ./test/parity/cmd/sdksurface`", f.path)
			}
		}
		if err := m.validate(); err != nil {
			return err
		}
		if err := validateGaps(rows, gaps); err != nil {
			return err
		}
		return ledgerError(append(checkLedger(in), checkVectors(root, in.Rows, currentPin())...))
	}
	for _, f := range []struct {
		path string
		data []byte
	}{{path, out}, {consistencyFile, consistency}, {filepath.Join(root, ledgerPath), ledgerFileText}, {filepath.Join(root, exportsDocPath), exportsDoc}} {
		if err := os.WriteFile(f.path, f.data, 0o644); err != nil {
			return err
		}
	}
	if err := m.validate(); err != nil {
		return err
	}
	if err := validateGaps(rows, gaps); err != nil {
		return err
	}
	return ledgerError(append(checkLedger(in), checkVectors(root, in.Rows, currentPin())...))
}

func ledgerError(problems []string) error {
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%d export ledger problem(s):\n  %s", len(problems), strings.Join(problems, "\n  "))
}
