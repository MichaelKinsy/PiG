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
func verify(root string, generated *Ledger, genJSON []byte, genPage string, strict bool) error {
	committed, committedJSON, err := readCommitted(root)
	if err != nil {
		return err
	}
	var problems []string
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

func firstCommit(e Entry) string {
	if len(e.Commits) == 0 {
		return "not yet committed"
	}
	return "commit " + e.Commits[0].Hash
}
