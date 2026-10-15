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
	strict := flag.Bool("strict", false, "with -check, compare commit hashes and tests exactly")
	notes := flag.String("notes", "", "print the draft release notes for this version to standard output instead of writing the ledger")
	flag.Parse()
	abs, err := filepath.Abs(*root)
	if err != nil {
		fail(err)
	}
	if *notes != "" {
		text, err := releaseNotes(abs, *since, *notes)
		if err != nil {
			fail(err)
		}
		fmt.Print(text)
		return
	}
	if err := run(abs, *since, *check, *strict); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "bug-ledger:", err)
	os.Exit(1)
}

func run(root, since string, check, strict bool) error {
	l, err := build(root, since)
	if err != nil {
		return err
	}
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
