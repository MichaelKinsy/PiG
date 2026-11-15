// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// A commit message is immutable. When the root cause an entry derives from one is false (it says what the code did before a later fix of the same pull request changed it), the entry cannot be fixed at its source, and the sources that can change (the changelog bullet) are not read for the root cause. corrections.json is the reviewed, hand-written way to state the root cause of such an entry. It is not the generated ledger: the ledger is rewritten from it.
//
// Only rootCause can be corrected, and each correction says who decided and why. The file cleans itself: the check fails on a correction for an entry the ledger does not have, on one the committed ledger does not carry, and on one that repeats what the generator derives without it, so a correction cannot outlive its use.

const (
	correctionsPath = "automation/ci/bugledger/corrections.json"
	// correctionSource is the rootCauseSource of an entry whose root cause is a correction.
	correctionSource = "correction"
)

// correction is one entry of corrections.json.
type correction struct {
	ID        string `json:"id"`
	RootCause string `json:"rootCause"`
	// DecidedBy names the role that decided that the derived text is wrong, and where (a role, never a person), and Reason says why the commit message cannot be the source: what it says and what is true.
	DecidedBy string `json:"decidedBy"`
	Reason    string `json:"reason"`
}

// readCorrections reads corrections.json; a missing file is no corrections.
func readCorrections(root string) ([]correction, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(correctionsPath)))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	fail := func(format string, a ...any) error {
		return fmt.Errorf("%s: %s", correctionsPath, fmt.Sprintf(format, a...))
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var out []correction
	if err := dec.Decode(&out); err != nil {
		return nil, fail("must be a list of {id, rootCause, decidedBy, reason}: %v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fail("text after the list")
	}
	seen := map[string]bool{}
	for i, c := range out {
		switch {
		case !strings.HasPrefix(c.ID, "BF-"):
			return nil, fail("entry %d: id %q is not a ledger entry id (BF-...)", i+1, c.ID)
		case seen[c.ID]:
			return nil, fail("%s is corrected twice", c.ID)
		case strings.TrimSpace(c.RootCause) == "" || strings.TrimSpace(c.DecidedBy) == "" || strings.TrimSpace(c.Reason) == "":
			return nil, fail("%s needs a rootCause, decidedBy and reason", c.ID)
		case publicText(c.RootCause) != c.RootCause:
			return nil, fail("%s: rootCause must be public prose on one line: no private wording, no commit hash, no line break", c.ID)
		}
		seen[c.ID] = true
	}
	return out, nil
}

func entryByID(l *Ledger, id string) *Entry {
	for i := range l.Entries {
		if l.Entries[i].ID == id {
			return &l.Entries[i]
		}
	}
	return nil
}

// applyCorrections sets the root cause of each corrected entry of l and returns the ids that name no entry.
func applyCorrections(l *Ledger, corrections []correction) (stale []string) {
	for _, c := range corrections {
		e := entryByID(l, c.ID)
		if e == nil {
			stale = append(stale, c.ID)
			continue
		}
		e.RootCause, e.CauseFrom = c.RootCause, correctionSource
	}
	return stale
}

func staleCorrections(ids []string) error {
	return fmt.Errorf("%s: %s is stale: no entry of the ledger has that id; delete the correction", correctionsPath, strings.Join(ids, ", "))
}

// checkCorrections reports the corrections the committed ledger does not honor or that no longer correct anything. generated is the ledger the history implies, before any correction.
func checkCorrections(corrections []correction, committed, generated *Ledger) []string {
	var problems []string
	for _, c := range corrections {
		e := entryByID(committed, c.ID)
		if e == nil {
			problems = append(problems, staleCorrections([]string{c.ID}).Error())
			continue
		}
		if e.RootCause != c.RootCause {
			problems = append(problems, fmt.Sprintf("entry %s: its root cause differs from corrections.json", c.ID))
		}
		// The generated entry of the same bug is the one with its identity that shares a fix commit (see keepPrior).
		i := slices.IndexFunc(generated.Entries, func(g Entry) bool { return identity(g) == identity(*e) && sharesCommit(g, *e) })
		if i >= 0 && generated.Entries[i].RootCause == c.RootCause {
			problems = append(problems, fmt.Sprintf("%s: the correction of %s is redundant, the generator derives the same root cause without it; delete it", correctionsPath, c.ID))
		}
	}
	return problems
}
