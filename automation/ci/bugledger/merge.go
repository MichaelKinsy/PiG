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
//
// The same bug is the committed entry with the generated entry's identity that shares a fix commit with it. Many commit entries share one
// identity (every commit whose subject says the same thing, or whose wording is private, has the same title), so the identity alone does not
// say which committed entry belongs to which generated one: pairing them by position let a count that differed by one (a duplicate left by an
// earlier regeneration, a commit a merge brought back into the history, a fix older than the committed ones) replace a new entry by an old one,
// drop an entry whose fix is still in the history, and change the ledger on every later run. A committed entry that no generated entry claims is
// a bug the history no longer has and is dropped; a generated entry that no committed entry claims is new and keeps its generated form.
// Only an entry whose identity is the bug itself (a release note or a divergence, whose commit list legitimately differs between clones) also
// takes the committed entry of its identity that no other generated entry claimed when they share no commit.
func keepPrior(l *Ledger, prior []Entry, root string) {
	type committed struct {
		entry Entry
		taken bool
	}
	have := map[string][]*committed{}
	for _, p := range prior {
		if len(p.Commits) > 0 {
			// A test a port retired or renamed since the entry was committed no longer names anything.
			p.Tests = slices.DeleteFunc(slices.Clone(p.Tests), func(t string) bool { return !testExists(root, t) })
			if p.Tests == nil {
				p.Tests = []string{}
			}
			k := identity(p)
			have[k] = append(have[k], &committed{entry: p})
		}
	}
	// The file order of the committed entries must not decide the pairing.
	for _, list := range have {
		slices.SortStableFunc(list, func(a, b *committed) int { return strings.Compare(a.entry.ID, b.entry.ID) })
	}
	kept := make([]bool, len(l.Entries))
	used := map[string]bool{}
	keep := func(i int, c *committed) {
		l.Entries[i], c.taken, kept[i] = c.entry, true, true
		used[c.entry.ID] = true
	}
	for i, e := range l.Entries {
		var first, same *committed
		for _, c := range have[identity(e)] {
			if c.taken || !sharesCommit(c.entry, e) {
				continue
			}
			if first == nil {
				first = c
			}
			if same == nil && c.entry.ID == e.ID {
				same = c
			}
		}
		switch {
		case same != nil:
			keep(i, same)
		case first != nil:
			keep(i, first)
		}
	}
	for i, e := range l.Entries {
		if kept[i] || e.Origin == "commit" {
			continue
		}
		for _, c := range have[identity(e)] {
			if !c.taken {
				keep(i, c)
				break
			}
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

// sharesCommit reports whether two entries list a fix commit in common.
func sharesCommit(a, b Entry) bool {
	return slices.ContainsFunc(a.Commits, func(x Commit) bool {
		return slices.ContainsFunc(b.Commits, func(y Commit) bool { return x.Hash == y.Hash })
	})
}
