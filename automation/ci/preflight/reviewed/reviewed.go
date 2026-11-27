// SPDX-License-Identifier: MIT

// Package reviewed holds the reviewed ledger of expected-value changes, test/parity/ledgers/testprune-reviewed.toml, and the one
// definition of when one of its entries is valid. The line lint (test/parity/cmd/testprune) and the pre-flight's
// test-expectation rule both admit a test's expected-value change by an entry of this ledger, so both call this package.
package reviewed

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	// File is the ledger's path in the repository.
	File = "test/parity/ledgers/testprune-reviewed.toml"
	// DivergenceDir is where the records of the divergences a DIVERGENCE entry cites live.
	DivergenceDir = "docs/parity/divergences"
	// MinCommitLen is the shortest commit prefix an entry may name; a shorter one matches nothing.
	MinCommitLen = 10
	// PortMap is the first of the inventories a CITE entry's Pi file must be named by.
	PortMap = "docs/parity/PORT_MAP.md"
)

// Entry is one reviewed finding: it satisfies the expected-value finding of exactly its commit and path.
type Entry struct {
	Path   string `toml:"path"`
	Commit string `toml:"commit"`
	Class  string `toml:"class"`
	Cite   string `toml:"cite"`
	Reason string `toml:"reason"`
}

// Parse reads the ledger text. An empty text holds no entries.
func Parse(text string) ([]Entry, error) {
	var doc struct {
		Entry []Entry `toml:"entry"`
	}
	if _, err := toml.Decode(text, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", File, err)
	}
	return doc.Entry, nil
}

// DivergenceFile is the path of the record of divergence id.
func DivergenceFile(id string) string { return DivergenceDir + "/" + id + ".md" }

// Pinned reports whether the entry names a commit prefix long enough to match one.
func (e Entry) Pinned() bool { return len(e.Commit) >= MinCommitLen }

// NamesCommit reports whether sha, a full commit hash, starts with the entry's commit.
func (e Entry) NamesCommit(sha string) bool {
	return e.Pinned() && strings.HasPrefix(sha, e.Commit)
}

// Matches reports whether the entry is the review of commit's change to the test file path.
func (e Entry) Matches(commit, filePath string) bool {
	return e.NamesCommit(commit) && e.Path == filePath
}

var (
	citeRE       = regexp.MustCompile(`(?m)^Pi-Behavior-Change:\s*(\S*\.ts):\d+`)
	divergenceRE = regexp.MustCompile(`^D\d+$`)
)

// Validate reports why the entry cannot stand for a review, or nil. piNames holds the file names of the Pi sources and
// tests the inventories of main name; divergenceRecorded tells whether the record of a divergence id exists.
func (e Entry) Validate(piNames map[string]bool, divergenceRecorded func(id string) bool) error {
	if strings.TrimSpace(e.Reason) == "" {
		return fmt.Errorf("has no reason")
	}
	switch e.Class {
	case "CITE":
		m := citeRE.FindStringSubmatch("Pi-Behavior-Change: " + e.Cite)
		if m == nil || !piNames[path.Base(m[1])] {
			return fmt.Errorf("cite %q does not name a Pi file:line of %s", e.Cite, PortMap)
		}
	case "DIVERGENCE":
		if !divergenceRE.MatchString(e.Cite) {
			return fmt.Errorf("cite %q is not a divergence id", e.Cite)
		}
		if !divergenceRecorded(e.Cite) {
			return fmt.Errorf("divergence %s has no %s", e.Cite, DivergenceFile(e.Cite))
		}
	case "NOBEHAVIOR", "message-only":
	default:
		return fmt.Errorf("unknown class %q", e.Class)
	}
	return nil
}
