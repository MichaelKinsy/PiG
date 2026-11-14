// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"
	"time"
)

// The cause rule starts at one instant, causeRequiredFrom (2026-11-14T05:00:00Z), whatever time zone the author's clock keeps: the same commit instant, written at -0500, +0000 and +1300, gets one answer, and the second before the instant and the instant itself get the two answers. The message names the instant of the commit in UTC.
func TestTheCauseRuleStartsAtOneInstantInEveryTimeZone(t *testing.T) {
	for _, c := range []struct {
		instant string
		asks    bool
	}{
		{"2026-11-14T04:59:59Z", false},
		{"2026-11-14T05:00:00Z", true},
		{"2026-11-13T23:30:00Z", false},
		{"2026-11-14T06:00:00Z", true},
	} {
		at, err := time.Parse(time.RFC3339, c.instant)
		if err != nil {
			t.Fatal(err)
		}
		for _, zone := range []struct {
			name   string
			offset int
		}{{"-0500", -5 * 3600}, {"+0000", 0}, {"+1300", 13 * 3600}} {
			t.Run(c.instant+" at "+zone.name, func(t *testing.T) {
				r := newFixture(t)
				r.write("b.go", "package x\n")
				commitAt(t, r, at.In(time.FixedZone(zone.name, zone.offset)).Format("2006-01-02T15:04:05-0700"), "Fix a crash in b", "b.go")
				if err := run(r.root, "0.4.1", false, false); err != nil {
					t.Fatal(err)
				}
				err := run(r.root, "0.4.1", true, false)
				switch {
				case c.asks && (err == nil || !strings.Contains(err.Error(), "has no root cause") || !strings.Contains(err.Error(), c.instant)):
					t.Errorf("a fix made at %s (%s) must be asked for its cause, naming its UTC instant, got %v", c.instant, zone.name, err)
				case !c.asks && err != nil:
					t.Errorf("a fix made at %s (%s) is before the rule and must pass, got %v", c.instant, zone.name, err)
				}
			})
		}
	}
}

// An entry the committed ledger already holds is judged by the instant of its commit, not by the date the ledger wrote for it in the author's zone: a ledger written before the rule named an instant has such dates.
func TestACommittedEntryIsJudgedByItsCommitInstant(t *testing.T) {
	r := newFixture(t)
	r.write("b.go", "package x\n")
	commitAt(t, r, "2026-11-14T06:00:00Z", "Fix a crash in b", "b.go")
	if err := run(r.root, "0.4.1", false, false); err != nil {
		t.Fatal(err)
	}
	l, _, err := readCommitted(r.root)
	if err != nil {
		t.Fatal(err)
	}
	l.Entries[0].Commits[0].Date = "2026-11-13"
	writeLedger(t, r, l)
	if err := run(r.root, "0.4.1", true, false); err == nil || !strings.Contains(err.Error(), "has no root cause") {
		t.Fatalf("a committed entry whose commit is after the rule's instant must be asked for its cause, got %v", err)
	}
}

// A committed entry that records no cause, whose commit message now gives one, is reported as stale when its commit is after the instant of the rule, whatever author-zone date the committed ledger holds for the commit.
func TestAStaleCommittedEntryWithoutACauseIsReportedByItsCommitInstant(t *testing.T) {
	r := newFixture(t)
	r.write("a.go", "package x\n")
	commitAt(t, r, "2026-11-14T06:00:00Z", "Fix a crash in a\n\nCause: the length was read before it was checked.", "a.go")
	if err := run(r.root, "0.4.1", false, false); err != nil {
		t.Fatal(err)
	}
	l, _, err := readCommitted(r.root)
	if err != nil {
		t.Fatal(err)
	}
	l.Entries[0].RootCause, l.Entries[0].CauseFrom = "", ""
	l.Entries[0].Commits[0].Date = "2026-11-13"
	writeLedger(t, r, l)
	if err := run(r.root, "0.4.1", true, false); err == nil || !strings.Contains(err.Error(), "its sources now give one") {
		t.Fatalf("the stale committed entry must be reported, got %v", err)
	}
}
