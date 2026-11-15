// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"maps"
	"reflect"
	"strings"
	"testing"
)

const correctionsFile = "automation/ci/bugledger/corrections.json"

func writeCorrections(t *testing.T, r testRepo, entries ...map[string]string) {
	t.Helper()
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	r.write(correctionsFile, string(data)+"\n")
}

func correctionOf(id, text string) map[string]string {
	return map[string]string{"id": id, "rootCause": text, "decidedBy": "the maintainer, in a review", "reason": "the fix commit message says it differently and cannot change"}
}

// twoFixes commits two fixes with their ledger and returns the ID of the first.
func twoFixes(t *testing.T) (testRepo, string) {
	t.Helper()
	r := newFixture(t)
	r.write("changelog.d/alpha.md", "### Fixed\n\n- The alpha loop no longer hangs.\n")
	r.commit("alpha: fix it\n\nRoot cause: the alpha loop never checked the stop flag.", "changelog.d/alpha.md")
	addFix(t, r, "beta", "The beta cache no longer grows.")
	l, _, err := readCommitted(r.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range l.Entries {
		if strings.Contains(e.Title, "alpha loop") {
			return r, e.ID
		}
	}
	t.Fatalf("no alpha entry in %+v", l.Entries)
	return r, ""
}

func TestAReviewedCorrectionReplacesTheRootCauseOfItsEntryAndNoOther(t *testing.T) {
	r, id := twoFixes(t)
	if err := run(r.root, "0.4.1", false, false); err != nil {
		t.Fatal(err)
	}
	before, beforeJSON, err := readCommitted(r.root)
	if err != nil {
		t.Fatal(err)
	}
	// No file: the ledger is what it was, byte for byte.
	if err := run(r.root, "0.4.1", false, false); err != nil {
		t.Fatal(err)
	}
	if _, again, _ := readCommitted(r.root); !bytes.Equal(again, beforeJSON) {
		t.Fatal("regenerating without a corrections file changed the ledger")
	}

	const text = "the alpha loop waited on a flag that nothing set."
	writeCorrections(t, r, correctionOf(id, text))
	err = run(r.root, "0.4.1", true, false)
	if err == nil || !strings.Contains(err.Error(), id) || !strings.Contains(err.Error(), "differs from corrections.json") {
		t.Fatalf("a correction the committed ledger does not carry must fail the check, got %v", err)
	}
	if err := run(r.root, "0.4.1", false, false); err != nil {
		t.Fatal(err)
	}
	after, _, err := readCommitted(r.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Entries) != len(before.Entries) {
		t.Fatalf("entries %d -> %d", len(before.Entries), len(after.Entries))
	}
	for _, old := range before.Entries {
		got := entryByID(after, old.ID)
		if got == nil {
			t.Fatalf("entry %s disappeared", old.ID)
		}
		want := old
		if old.ID == id {
			want.RootCause, want.CauseFrom = text, "correction"
		}
		if !reflect.DeepEqual(*got, want) {
			t.Errorf("entry %s = %+v, want %+v", old.ID, *got, want)
		}
	}
	for _, strict := range []bool{false, true} {
		if err := run(r.root, "0.4.1", true, strict); err != nil {
			t.Errorf("check (strict %v) after regeneration: %v", strict, err)
		}
	}
	// A refresh recomputes every entry and still applies the correction.
	if err := runWith(r.root, "0.4.1", false, false, true); err != nil {
		t.Fatal(err)
	}
	if refreshed, _, _ := readCommitted(r.root); entryByID(refreshed, id) == nil || entryByID(refreshed, id).RootCause != text {
		t.Errorf("refresh dropped the correction: %+v", entryByID(refreshed, id))
	}
}

func TestACorrectionThatIsMalformedFailsTheGenerator(t *testing.T) {
	r, id := twoFixes(t)
	good := correctionOf(id, "the alpha loop waited on a flag that nothing set.")
	without := func(key string) map[string]string {
		c := map[string]string{}
		for k, v := range good {
			if k != key {
				c[k] = v
			}
		}
		return c
	}
	with := func(key, value string) map[string]string {
		c := map[string]string{}
		maps.Copy(c, good)
		c[key] = value
		return c
	}
	for name, content := range map[string]string{
		"not json":               "BF-1 the cause\n",
		"an object":              `{"id": "` + id + `"}`,
		"another field":          mustJSON(t, with("releaseNote", "A different note.")),
		"the title":              mustJSON(t, with("title", "A different title")),
		"no decidedBy":           mustJSON(t, without("decidedBy")),
		"no reason":              mustJSON(t, without("reason")),
		"no rootCause":           mustJSON(t, without("rootCause")),
		"a blank reason":         mustJSON(t, with("reason", "  ")),
		"private wording":        mustJSON(t, with("rootCause", "Reviewers asked for it. The alpha loop waited on a flag.")),
		"the same id twice":      mustJSON(t, good, good),
		"trailing text":          mustJSON(t, good) + "x",
		"an id without BF-":      mustJSON(t, with("id", "4ec88b4")),
		"a cause with a newline": mustJSON(t, with("rootCause", "line one\nline two")),
	} {
		r.write(correctionsFile, content)
		for _, check := range []bool{false, true} {
			err := run(r.root, "0.4.1", check, false)
			if err == nil || !strings.Contains(err.Error(), "corrections.json") {
				t.Errorf("%s (check %v): got %v, want an error naming corrections.json", name, check, err)
			}
		}
	}
}

func mustJSON(t *testing.T, entries ...map[string]string) string {
	t.Helper()
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestACorrectionForAnEntryTheLedgerDoesNotHaveFailsTheCheckAndTheGenerator(t *testing.T) {
	r, _ := twoFixes(t)
	writeCorrections(t, r, correctionOf("BF-0000000", "a cause for a bug that is gone."))
	err := run(r.root, "0.4.1", true, false)
	if err == nil || !strings.Contains(err.Error(), "BF-0000000") || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("check: got %v, want the stale correction named", err)
	}
	err = run(r.root, "0.4.1", false, false)
	if err == nil || !strings.Contains(err.Error(), "BF-0000000") || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("generator: got %v, want the stale correction named", err)
	}
}

func TestACorrectionThatRepeatsWhatTheGeneratorDerivesFailsTheCheck(t *testing.T) {
	r, id := twoFixes(t)
	// The alpha fix commit states "the alpha loop never checked the stop flag.": a correction with that text corrects nothing.
	writeCorrections(t, r, correctionOf(id, "the alpha loop never checked the stop flag."))
	if err := run(r.root, "0.4.1", false, false); err != nil {
		t.Fatal(err)
	}
	err := run(r.root, "0.4.1", true, false)
	if err == nil || !strings.Contains(err.Error(), id) || !strings.Contains(err.Error(), "redundant") {
		t.Fatalf("a redundant correction must fail the check, got %v", err)
	}
}
