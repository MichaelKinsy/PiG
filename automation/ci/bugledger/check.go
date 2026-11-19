// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// identity is what makes two entries the same bug. Commit hashes, tests and root-cause text are left out: they change when the fix lands, and the commit that writes a release note cannot name its own hash.
func identity(e Entry) string {
	return strings.Join([]string{e.Origin, e.Class, e.Divergence, e.FixedIn, e.Title, e.Note}, "\x1f")
}

func readCommitted(root string) (*Ledger, []byte, error) {
	data, err := os.ReadFile(filepath.Join(root, jsonPath))
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w; run `make bug-ledger`", jsonPath, err)
	}
	var l Ledger
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", jsonPath, err)
	}
	return &l, data, nil
}

// verify compares the committed ledger with the one the history implies. Without strict it tolerates a stale commit list, so the commit that adds a fix and the commit that regenerates the ledger need not be the same.
func verify(root string, generated *Ledger, genJSON []byte, genPage string, strict bool, extra []string) error {
	committed, committedJSON, err := readCommitted(root)
	if err != nil {
		return err
	}
	problems := slices.Clone(extra)
	have := map[string]int{}
	for _, e := range committed.Entries {
		have[identity(e)]++
	}
	for _, e := range generated.Entries {
		k := identity(e)
		if have[k] > 0 {
			have[k]--
			continue
		}
		problems = append(problems, fmt.Sprintf("no ledger entry for %q (%s)", e.Title, firstCommit(e)))
	}
	for _, e := range committed.Entries {
		if k := identity(e); have[k] > 0 {
			have[k]--
			problems = append(problems, fmt.Sprintf("entry %s %q matches no bug in the history", e.ID, e.Title))
		}
	}
	haveDiv := map[string]DivergenceRef{}
	for _, d := range committed.Divergences {
		haveDiv[d.ID] = d
	}
	for _, d := range generated.Divergences {
		if got, ok := haveDiv[d.ID]; !ok {
			problems = append(problems, fmt.Sprintf("divergence %s %q is not accounted for in the ledger", d.ID, d.Title))
		} else if got.Entry == "" && d.Entry != "" {
			problems = append(problems, fmt.Sprintf("divergence %s fixes a Pi defect but the ledger lists it as a deliberate difference", d.ID))
		}
	}
	for _, e := range committed.Entries {
		for _, t := range e.Tests {
			if !testExists(root, t) {
				problems = append(problems, fmt.Sprintf("entry %s names the test %s, which does not exist", e.ID, t))
			}
		}
		if len(e.Commits) > 0 {
			continue
		}
		if e.Origin == "divergence" {
			problems = append(problems, fmt.Sprintf("entry %s has no fix commit", e.ID))
		}
	}
	problems = append(problems, checkMovedTests(root)...)
	settled, err := unsettled(root, committed, generated)
	if err != nil {
		return err
	}
	problems = append(problems, settled...)
	page, err := os.ReadFile(filepath.Join(root, pagePath))
	if err != nil {
		problems = append(problems, fmt.Sprintf("%s: %v", pagePath, err))
	} else if string(page) != renderMarkdown(committed) {
		problems = append(problems, pagePath+" does not match "+jsonPath)
	}
	if strict {
		if !bytes.Equal(committedJSON, genJSON) {
			problems = append(problems, jsonPath+" differs from the ledger the history implies")
		}
		if err == nil && string(page) != genPage {
			problems = append(problems, pagePath+" differs from the ledger the history implies")
		}
	}
	if len(problems) == 0 {
		return nil
	}
	slices.Sort(problems)
	return errors.New("the bug ledger is out of date; run `make bug-ledger` and commit the result:\n  " + strings.Join(problems, "\n  "))
}

// causeRequiredFrom is the first instant whose fixes must have a root cause: an entry whose newest fix commit has an author instant at or after it, and for which no source gives a cause, fails the check. It is one moment in UTC, 2026-11-14T05:00:00Z, compared with the commit's author instant, so the answer does not depend on the time zone the author's clock keeps. It is midnight of 2026-11-14 at -0500, the zone of the hosts the rule has been judged on, so no commit made there changed its verdict when the rule was stated as an instant. The fixes before it were written when the ledger took whatever paragraph held a cause word, and their commit messages cannot change; they stay "Not recorded" until their changelog bullet gets a cause marker.
var causeRequiredFrom = time.Date(2026, 11, 14, 5, 0, 0, 0, time.UTC)

// newestFix returns the latest author instant among the commits of e, and whether e has a commit. A commit whose instant is unknown, because the history no longer holds it, counts from the start of the day its record names, in UTC.
func newestFix(e Entry) (time.Time, bool) {
	var newest time.Time
	for _, c := range e.Commits {
		at := c.at
		if at.IsZero() {
			day, err := time.Parse("2006-01-02", c.Date)
			if err != nil {
				continue
			}
			at = day
		}
		if at.After(newest) {
			newest = at
		}
	}
	return newest, !newest.IsZero()
}

// fixedSinceTheRule reports whether the newest fix commit of e is at or after the instant the cause rule starts.
func fixedSinceTheRule(e Entry) bool {
	newest, ok := newestFix(e)
	return ok && !newest.Before(causeRequiredFrom)
}

// needsCause reports whether an entry is a fix that must state its cause and has none.
func needsCause(e Entry) bool {
	return e.RootCause == "" && fixedSinceTheRule(e)
}

// fillInstants reads the author instant of every commit of entries that has none. The committed ledger records only the author's calendar date; the history says the moment.
func fillInstants(root string, entries []Entry) error {
	var hashes []string
	for _, e := range entries {
		for _, c := range e.Commits {
			if c.at.IsZero() {
				hashes = append(hashes, c.Hash)
			}
		}
	}
	found, err := repo{root}.authorInstants(hashes)
	if err != nil {
		return err
	}
	for i := range entries {
		for j := range entries[i].Commits {
			c := &entries[i].Commits[j]
			if !c.at.IsZero() {
				continue
			}
			for hash, at := range found {
				if strings.HasPrefix(hash, c.Hash) {
					c.at = at
					break
				}
			}
		}
	}
	return nil
}

// causeDated says which instant made the rule apply to e, so that a reader in any zone sees the moment compared.
func causeDated(e Entry) string {
	newest, _ := newestFix(e)
	return fmt.Sprintf("its newest fix commit is dated %s, on or after %s", newest.UTC().Format(time.RFC3339), causeRequiredFrom.Format(time.RFC3339))
}

// askForCause says where the cause of an entry that has none is written.
func askForCause(e Entry) string {
	switch {
	case e.Origin == "commit":
		return fmt.Sprintf("entry %s has no root cause (%s): the message of commit %s gives none and cannot change. Write the fix as a \"### Fixed\" bullet of a changelog.d fragment that ends with <!-- fix: %s --> <!-- cause: <why it happened> -->, or, if it fixes no product bug, exempt the commit in automation/ci/bugledger/exempt.txt", e.ID, causeDated(e), e.Commits[0].Hash, e.Commits[0].Hash)
	case e.stated.file != "":
		return fmt.Sprintf("entry %s has no root cause (%s): neither its bullet in %s nor its fix commits say why the bug happened. Add <!-- cause: <why it happened> --> to the bullet %q and run `make bug-ledger`", e.ID, causeDated(e), e.stated.file, clip(e.Title, 60))
	}
	return fmt.Sprintf("entry %s %q has no root cause (%s): state it where the entry is written and run `make bug-ledger`", e.ID, clip(e.Title, 60), causeDated(e))
}

// unsettled reports what `make bug-ledger` would settle differently from the committed ledger for the bugs both have, because the tree or the rules now say otherwise: a class the rules no longer give the entry's text, a cause or tests the entry's bullet states and the entry lacks, a cause that is a paragraph about how the fix was checked, a commit HEAD does not reach where the fix marker names the commit that replaced it. It also reports the fixes made since causeRequiredFrom for which no cause can be derived. A commit list or a test list that only differs in what the history attributes is not reported: the check tolerates those, so that the commit that adds a fix and the commit that regenerates the ledger need not be the same.
func unsettled(root string, committed, generated *Ledger) ([]string, error) {
	moved, err := loadMovedTests(root)
	if err != nil {
		return nil, err
	}
	written := *generated
	written.Entries = slices.Clone(generated.Entries)
	written.Divergences = slices.Clone(generated.Divergences)
	keepPrior(&written, committed.Entries, root, moved)
	if err := fillInstants(root, written.Entries); err != nil {
		return nil, err
	}
	byID := map[string]Entry{}
	file := map[string]string{}
	names := map[string]bool{}
	for _, e := range written.Entries {
		byID[e.ID] = e
		if e.stated.file != "" {
			file[identity(e)] = e.stated.file
		}
		for _, c := range e.Commits {
			names[c.Hash] = true
		}
	}
	var problems []string
	for _, c := range committed.Entries {
		for _, x := range c.Commits {
			if !generated.reach.has(x.Hash) && !names[x.Hash] {
				where := ""
				if f := file[identity(c)]; f != "" {
					where = " in " + f
				}
				problems = append(problems, fmt.Sprintf("entry %s names the commit %s, which HEAD does not reach, and the fix marker of its bullet%s names the commit that replaced it", c.ID, x.Hash, where))
			}
		}
		e, ok := byID[c.ID]
		if !ok || identity(e) != identity(c) {
			continue
		}
		if e.Impact != c.Impact {
			problems = append(problems, fmt.Sprintf("entry %s: the user impact is %q and its text now reads as %q", c.ID, c.Impact, e.Impact))
		}
		if e.Platform != c.Platform {
			problems = append(problems, fmt.Sprintf("entry %s: the platform is %q and its text now reads as %q", c.ID, c.Platform, e.Platform))
		}
		if e.stated.tests && !slices.Equal(e.Tests, c.Tests) {
			problems = append(problems, fmt.Sprintf("entry %s does not list the tests the tests marker of its bullet in %s names", c.ID, e.stated.file))
		}
		switch {
		case needsCause(e):
			problems = append(problems, askForCause(e))
		case e.RootCause == c.RootCause && e.CauseFrom == c.CauseFrom:
		case e.stated.cause:
			problems = append(problems, fmt.Sprintf("entry %s does not have the root cause the cause marker of its bullet in %s states", c.ID, e.stated.file))
		case c.CauseFrom == "commit" && evidenceHead.MatchString(c.RootCause):
			problems = append(problems, fmt.Sprintf("entry %s: the root cause is a paragraph about how the fix was checked (%q), not why the bug happened", c.ID, clip(c.RootCause, 60)))
		case c.RootCause == "" && fixedSinceTheRule(e):
			problems = append(problems, fmt.Sprintf("entry %s has no root cause and its sources now give one", c.ID))
		}
	}
	return problems, nil
}

func firstCommit(e Entry) string {
	if len(e.Commits) == 0 {
		return "not yet committed"
	}
	return "commit " + e.Commits[0].Hash
}
