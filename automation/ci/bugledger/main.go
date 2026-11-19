// SPDX-License-Identifier: MIT

// Command bugledger generates the public bug fix ledger from the project's ground truth: the commit history since the previous release, the "Fixed" sections of CHANGELOG.md and changelog.d, and the divergence records that mark a Pi defect fixed ahead of Pi.
//
// `make bug-ledger` writes docs/bug-ledger.json and docs/site/docs/bug-ledger.md. `make bug-ledger-check` (-check) fails when they are out of date, when a recorded test no longer exists, when a fix-titled commit that changes shipped code has no entry, or when a new fix has no root cause. The ledger needs the full history; a shallow clone fails with a message that says so.
//
// The ledger is a function of the tree and of the history of HEAD alone. It never reads a commit HEAD does not reach, a file git ignores, or the clock, so two clones of one commit write the same files whatever else they hold.
//
// -notes <version> prints the draft release notes for a version from the same sources.
//
// A fix-titled commit that is not a product bug is listed with a category and a reason in automation/ci/bugledger/exempt.txt, or carries a "Bug-Ledger: exempt <category>: <reason>" trailer. A follow-up commit of an existing fix carries "Bug-Ledger: see <commit>".
//
// A commit message is immutable, so the root cause an entry derives from it can be wrong for good. automation/ci/bugledger/corrections.json is the reviewed, hand-written list of the entries whose root cause is replaced: one object per entry with its id, the new rootCause (the only field that can be corrected), who decided and why. A correction names an entry of the ledger, and it is removed once the sources give the same text: `make bug-ledger-check` fails on a correction for an entry that does not exist, on one the ledger does not carry, and on one whose text the generator derives without it.
//
// A fix states its root cause and its regression tests outright in its changelog bullet ("<!-- cause: ... -->", "<!-- tests: ... -->") or in its commit message ("Root-Cause:" and "Regression-Test:" trailers); see message.go. Without them the cause is read from the prose of the fix commits and of the bullet, never from a paragraph that reports how the fix was checked, and the tests are the Go tests the fix commit's diff adds or edits and the tests its message names in a file it touches, resolved against the tree. A test a later commit moved keeps its fix when it kept its name (the one file that declares it); a renamed test needs a line in automation/ci/bugledger/moved-tests.txt.
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
	since := flag.String("since", "0.4.1", "release the ledger starts after; with -notes, the release the notes start after (default: the release before the notes' version)")
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
		notesSince := ""
		flag.Visit(func(f *flag.Flag) {
			if f.Name == "since" {
				notesSince = *since
			}
		})
		text, err := releaseNotes(abs, notesSince, *notes)
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
	corrections, err := readCorrections(root)
	if err != nil {
		return err
	}
	l, err := build(root, since)
	if err != nil {
		return err
	}
	if !check && !refresh {
		if prior, _, err := readCommitted(root); err == nil {
			moved, err := loadMovedTests(root)
			if err != nil {
				return err
			}
			keepPrior(l, prior.Entries, root, moved)
		}
	}
	if err := fillInstants(root, l.Entries); err != nil {
		return err
	}
	// The corrections are judged against what the history derives, so they are applied after that is known: the check names a correction that repeats it.
	var correctionProblems []string
	if check {
		if committed, _, err := readCommitted(root); err == nil {
			correctionProblems = checkCorrections(corrections, committed, l)
		}
	}
	if stale := applyCorrections(l, corrections); !check && len(stale) > 0 {
		return staleCorrections(stale)
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
		for _, e := range l.Entries {
			if needsCause(e) {
				fmt.Fprintln(os.Stderr, "bug-ledger: `make bug-ledger-check` will fail:", askForCause(e))
			}
		}
		return nil
	}
	return verify(root, l, data, page, strict, correctionProblems)
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
