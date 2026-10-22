// SPDX-License-Identifier: MIT

package main

import (
	"slices"
	"strconv"
	"strings"
)

// The committed ledger is edited by every pull request that adds a fix, so its layout decides whether two pull requests conflict. Git merges two
// changes cleanly only when they touch different places, and GitHub's server-side merge honors no custom merge driver. Three rules keep each
// pull request to its own lines:
//
//   - canonical order: entries are sorted by ID, and an ID is a commit hash or a hash of the entry's text, so a new entry lands at a place
//     no other new entry is likely to share. The development order used to put every new entry at the end, where every pull request collided.
//   - no aggregate: no committed line is a function of all entries (the page used to open with the entry counts).
//   - incremental regeneration: `make bug-ledger` keeps every committed entry whose fix is already recorded and changes only the entries the
//     fragments and commits of the branch add or remove. `make bug-ledger-refresh` recomputes all of them from the history, as a release does.

// canonical returns the entries in the order the committed files list them.
func canonical(l *Ledger) {
	slices.SortStableFunc(l.Entries, func(a, b Entry) int { return strings.Compare(a.ID, b.ID) })
}

// keepPrior replaces each generated entry by the committed entry of the same bug when that entry records its fix commits. Regenerating then
// leaves the entries of other branches as they are, including their commit lists, which differ between clones of different ages, except that a recorded test the tree no longer has is dropped. An entry
// with no commit yet is generated again, so it picks up its commit once it exists.
func keepPrior(l *Ledger, prior []Entry, root string) {
	have := map[string][]Entry{}
	for _, p := range prior {
		if len(p.Commits) > 0 {
			// A test a port retired or renamed since the entry was committed no longer names anything.
			p.Tests = slices.DeleteFunc(slices.Clone(p.Tests), func(t string) bool { return !testExists(root, t) })
			if p.Tests == nil {
				p.Tests = []string{}
			}
			k := identity(p)
			have[k] = append(have[k], p)
		}
	}
	kept := make([]bool, len(l.Entries))
	used := map[string]bool{}
	for i, e := range l.Entries {
		k := identity(e)
		if list := have[k]; len(list) > 0 {
			l.Entries[i], have[k], kept[i] = list[0], list[1:], true
			used[list[0].ID] = true
		}
	}
	// A generated ID never takes the ID of a kept entry.
	for i := range l.Entries {
		if kept[i] {
			continue
		}
		id := l.Entries[i].ID
		for n := 2; used[l.Entries[i].ID]; n++ {
			l.Entries[i].ID = id + "-" + strconv.Itoa(n)
		}
		used[l.Entries[i].ID] = true
	}
	entryOf := map[string]string{}
	for _, e := range l.Entries {
		if e.Divergence != "" {
			entryOf[e.Divergence] = e.ID
		}
	}
	for i := range l.Divergences {
		l.Divergences[i].Entry = entryOf[l.Divergences[i].ID]
	}
}
