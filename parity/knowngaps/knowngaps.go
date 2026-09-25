// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

// Package knowngaps loads parity/known-gaps.toml, the one ledger of parity
// gaps that gates tolerate against the pinned Pi version.
//
// A gate reports each failing parity assertion by a stable key. A key listed
// for the gate's scope is tolerated and logged; any other key fails. A listed
// key that the gate no longer observes also fails, so the ledger only shrinks.
package knowngaps

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"

	"github.com/BurntSushi/toml"
)

// Path is the ledger location relative to the repository root.
const Path = "parity/known-gaps.toml"

// Scopes names every gate that consumes the ledger.
var Scopes = []string{"upstream-parity", "correspondence", "tui-keybindings", "app-keybindings"}

var upstreamVersionPattern = regexp.MustCompile(`(?m)^const UpstreamVersion = "([^"]+)"`)

// PinnedVersion reads coding.UpstreamVersion from
// coding/pigversion/pigversion.go under repoRoot (the literal coding.go
// re-exports, since coding/upstream.go itself only aliases it). It parses
// the file instead of importing the package so tests in packages that
// coding depends on (tui, internal/codingagent) can use it.
func PinnedVersion(repoRoot string) (string, error) {
	data, err := os.ReadFile(filepath.Join(repoRoot, "coding", "pigversion", "pigversion.go"))
	if err != nil {
		return "", err
	}
	match := upstreamVersionPattern.FindSubmatch(data)
	if match == nil {
		return "", fmt.Errorf("coding/pigversion/pigversion.go: UpstreamVersion not found")
	}
	return string(match[1]), nil
}

// Entry is one tolerated gap.
type Entry struct {
	Key      string `toml:"key"`
	Scope    string `toml:"scope"`
	Tracking string `toml:"tracking"`
	Why      string `toml:"why"`
}

// Ledger is the decoded known-gaps file.
type Ledger struct {
	PiVersion string  `toml:"pi_version"`
	Gap       []Entry `toml:"gap"`
}

// Load decodes and validates the ledger under repoRoot. The ledger must name
// the current pin, and every entry needs a key, a known scope, tracking, and a
// reason. Keys are unique within a scope.
func Load(repoRoot string) (Ledger, error) {
	var ledger Ledger
	path := filepath.Join(repoRoot, filepath.FromSlash(Path))
	meta, err := toml.DecodeFile(path, &ledger)
	if err != nil {
		return Ledger{}, fmt.Errorf("%s: %w", Path, err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		return Ledger{}, fmt.Errorf("%s: unknown fields %v", Path, undecoded)
	}
	pinned, err := PinnedVersion(repoRoot)
	if err != nil {
		return Ledger{}, err
	}
	if ledger.PiVersion != pinned {
		return Ledger{}, fmt.Errorf("%s: pi_version = %q, want the pinned %q; re-derive every gap against the new pin", Path, ledger.PiVersion, pinned)
	}
	seen := make(map[[2]string]struct{}, len(ledger.Gap))
	for _, entry := range ledger.Gap {
		if entry.Key == "" || entry.Tracking == "" || entry.Why == "" {
			return Ledger{}, fmt.Errorf("%s: entry %q needs key, scope, tracking, and why", Path, entry.Key)
		}
		if !slices.Contains(Scopes, entry.Scope) {
			return Ledger{}, fmt.Errorf("%s: entry %q has scope %q, want one of %v", Path, entry.Key, entry.Scope, Scopes)
		}
		id := [2]string{entry.Scope, entry.Key}
		if _, dup := seen[id]; dup {
			return Ledger{}, fmt.Errorf("%s: duplicate key %q in scope %q", Path, entry.Key, entry.Scope)
		}
		seen[id] = struct{}{}
	}
	return ledger, nil
}

// Scope returns the entries for one gate, keyed by gap key.
func (l Ledger) Scope(scope string) map[string]Entry {
	out := make(map[string]Entry)
	for _, entry := range l.Gap {
		if entry.Scope == scope {
			out[entry.Key] = entry
		}
	}
	return out
}

// Stale returns the sorted keys listed for scope that observed lacks. A
// caller must only ask after a complete, unfiltered run of its gate.
func (l Ledger) Stale(scope string, observed map[string]struct{}) []string {
	var stale []string
	for key := range l.Scope(scope) {
		if _, ok := observed[key]; !ok {
			stale = append(stale, key)
		}
	}
	slices.Sort(stale)
	return stale
}

// Blocked reports whether the gaps listed for scope explain err from a builder
// that refuses any finding. While the scope lists gaps, err must be exactly the
// refusal for that many findings; once the scope is empty, err must be nil.
// A non-nil problem means neither holds.
func Blocked(repoRoot, scope string, err error, refusal func(findings int) string) (blocked bool, problem error) {
	ledger, loadErr := Load(repoRoot)
	if loadErr != nil {
		return false, loadErr
	}
	listed := len(ledger.Scope(scope))
	if listed == 0 {
		if err != nil {
			return false, fmt.Errorf("no %s gaps are listed, but the build failed: %w", scope, err)
		}
		return false, nil
	}
	want := refusal(listed)
	if err == nil {
		return false, fmt.Errorf("with %d %s gaps listed in %s, the build succeeded; want the refusal %q or remove the closed gaps", listed, scope, Path, want)
	}
	if err.Error() != want {
		return false, fmt.Errorf("with %d %s gaps listed in %s, want the refusal %q, got: %w", listed, scope, Path, want, err)
	}
	return true, nil
}

// Tracker decides the gaps one complete test run observes against the
// ledger rows for its scope.
type Tracker struct {
	ledger   Ledger
	scope    string
	known    map[string]Entry
	observed map[string]struct{}
}

// NewTracker loads the ledger for scope.
func NewTracker(repoRoot, scope string) (*Tracker, error) {
	ledger, err := Load(repoRoot)
	if err != nil {
		return nil, err
	}
	return &Tracker{ledger: ledger, scope: scope, known: ledger.Scope(scope), observed: make(map[string]struct{})}, nil
}

// Gap records an observed gap and returns its ledger entry when listed.
func (t *Tracker) Gap(key string) (Entry, bool) {
	t.observed[key] = struct{}{}
	entry, ok := t.known[key]
	return entry, ok
}

// Stale returns listed keys that the run did not observe.
func (t *Tracker) Stale() []string { return t.ledger.Stale(t.scope, t.observed) }
