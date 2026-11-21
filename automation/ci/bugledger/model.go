// SPDX-License-Identifier: MIT

package main

import "time"

// Source classes. Every entry has exactly one.
const (
	// ClassOwn is a defect PiG introduced: it differed from Pi, or failed where Pi does not.
	ClassOwn = "own"
	// ClassAheadOfPi is a defect Pi has and PiG fixed first. It carries a divergence number and a remove-when condition.
	ClassAheadOfPi = "ahead-of-pi"
	// ClassPiPort is a fix Pi shipped that PiG ported.
	ClassPiPort = "pi-port"
)

// Commit is one commit that fixed, or is part of the fix of, an entry.
type Commit struct {
	Hash string `json:"hash"`
	Date string `json:"date"`
	// PR is the pull request number named in the subject, when there is one.
	PR string `json:"pr,omitempty"`
	// at is the instant of the commit's author date. It is not part of the document: the committed ledger records only Date, which is the author's own calendar date, and fillInstants reads the instant from the history.
	at time.Time
}

// Entry is one fixed bug.
type Entry struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Symptom    string   `json:"symptom"`
	RootCause  string   `json:"rootCause"`
	CauseFrom  string   `json:"rootCauseSource"`
	Impact     string   `json:"userImpact"`
	Platform   string   `json:"platform"`
	Class      string   `json:"class"`
	Divergence string   `json:"divergence,omitempty"`
	PiIssue    string   `json:"piIssue,omitempty"`
	RemoveWhen string   `json:"removeWhen,omitempty"`
	FixedIn    string   `json:"fixedIn"`
	Note       string   `json:"releaseNote,omitempty"`
	Origin     string   `json:"origin"`
	Commits    []Commit `json:"commits"`
	// CommitsFrom is "release note" when the listed commit wrote the release note and no commit of its work was identified as the fix.
	CommitsFrom string   `json:"commitsFrom,omitempty"`
	Tests       []string `json:"tests"`
	Evidence    string   `json:"evidence,omitempty"`
	// stated is what the changelog bullet of the entry states outright. It is read from the tree on every run and is not part of the document.
	stated stated
}

// stated records the file that holds the changelog bullet of an entry and which of the entry's fields the bullet states with a marker. A stated field is the entry's as written: no committed entry and no commit message overrides it.
type stated struct {
	file  string
	cause bool
	tests    bool
	evidence bool
}

// DivergenceRef accounts for one divergence record added since the baseline release: the ledger entry of a Pi defect it fixes ahead of Pi, or the statement that it is a deliberate difference and no bug fix.
type DivergenceRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Entry is the ID of the ledger entry for the Pi defect the divergence fixes; empty for a deliberate difference.
	Entry string `json:"entry,omitempty"`
}

// Ledger is the generated document.
type Ledger struct {
	// Since is the release the ledger starts after.
	Since   string  `json:"since"`
	Entries []Entry `json:"entries"`
	// Divergences lists every divergence record added since the baseline, so none is missing from the ledger unaccounted.
	Divergences []DivergenceRef `json:"divergences"`
	// Exempt counts fix-titled commits that are not product bugs, by reason category.
	Exempt map[string]int `json:"exemptCommits"`
	// reach is the history the ledger was built from; it is not part of the document.
	reach history
}
