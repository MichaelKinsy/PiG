// SPDX-License-Identifier: MIT

// Command bugledger generates the public bug fix ledger from the project's ground truth: the commit history since the previous release, the "Fixed" sections of CHANGELOG.md and changelog.d, and the divergence records that mark a Pi defect fixed ahead of Pi.
//
// `make bug-ledger` writes docs/bug-ledger.json and docs/site/docs/bug-ledger.md. `make bug-ledger-check` (-check) fails when they are out of date, when a recorded test no longer exists, or when a fix-titled commit that changes shipped code has no entry. The ledger needs the full history; a shallow clone fails with a message that says so.
//
// -notes <version> prints the draft release notes for a version from the same sources.
//
// A fix-titled commit that is not a product bug is listed with a category and a reason in automation/ci/bugledger/exempt.txt, or carries a "Bug-Ledger: exempt <category>: <reason>" trailer. A follow-up commit of an existing fix carries "Bug-Ledger: see <commit>".
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

const (
	jsonPath = "docs/bug-ledger.json"
	pagePath = "docs/site/docs/bug-ledger.md"
)

func main() {
	root := flag.String("root", ".", "repository root")
	since := flag.String("since", "0.4.1", "release the ledger starts after")
	check := flag.Bool("check", false, "fail when the committed ledger is out of date instead of writing it")
	refresh := flag.Bool("refresh", false, "recompute every entry from the history instead of keeping the committed entries whose fix is recorded (a release does this)")
	summary := flag.Bool("summary", false, "print the entry counts of the committed ledger")
	strict := flag.Bool("strict", false, "with -check, compare commit hashes and tests exactly")
	notes := flag.String("notes", "", "print the draft release notes for this version to standard output instead of writing the ledger")
	flag.Parse()
	abs, err := filepath.Abs(*root)
	if err != nil {
		fail(err)
	}
	if *summary {
		if err := printSummary(abs); err != nil {
			fail(err)
		}
		return
	}
	if *notes != "" {
		text, err := releaseNotes(abs, *since, *notes)
		if err != nil {
			fail(err)
		}
		fmt.Print(text)
		return
	}
	if err := runWith(abs, *since, *check, *strict, *refresh); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "bug-ledger:", err)
	os.Exit(1)
}

func run(root, since string, check, strict bool) error {
	return runWith(root, since, check, strict, false)
}

// runWith writes or checks the ledger. Writing keeps the committed entries whose fix is recorded unless refresh is set (see keepPrior); the strict check compares with the ledger the history implies, so it refreshes.
func runWith(root, since string, check, strict, refresh bool) error {
	l, err := build(root, since)
	if err != nil {
		return err
	}
	if !check && !refresh {
		if prior, _, err := readCommitted(root); err == nil {
			keepPrior(l, prior.Entries, root)
		}
	}
	canonical(l)
	data, err := marshalJSON(l)
	if err != nil {
		return err
	}
	page := renderMarkdown(l)
	if !check {
		for _, p := range []string{jsonPath, pagePath} {
			if err := os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755); err != nil {
				return err
			}
		}
		if err := os.WriteFile(filepath.Join(root, jsonPath), data, 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(root, pagePath), []byte(page), 0o644); err != nil {
			return err
		}
		fmt.Printf("bug-ledger: %d entries written to %s and %s\n", len(l.Entries), jsonPath, pagePath)
		return nil
	}
	return verify(root, l, data, page, strict)
}

// printSummary prints the counts the page does not carry: a committed count would change in every pull request that adds an entry.
func printSummary(root string) error {
	l, _, err := readCommitted(root)
	if err != nil {
		return err
	}
	counts := map[string]int{}
	untested := 0
	for _, e := range l.Entries {
		counts[e.Class]++
		if len(e.Tests) == 0 {
			untested++
		}
	}
	fmt.Printf("%s. %s, %s, %s. %s have no recorded test.\n", plural(len(l.Entries), "bug", "bugs"), plural(counts[ClassOwn], "own defect", "own defects"), plural(counts[ClassAheadOfPi], "fixed ahead of Pi", "fixed ahead of Pi"), plural(counts[ClassPiPort], "Pi fix ported", "Pi fixes ported"), plural(untested, "bug", "bugs"))
	return nil
}
