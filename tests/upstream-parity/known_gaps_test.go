// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package parity

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/parity/knowngaps"
)

// knownGapScope selects this suite's rows in parity/known-gaps.toml.
const knownGapScope = "upstream-parity"

var (
	knownGapsOnce sync.Once
	knownGaps     knowngaps.Ledger
	knownGapsErr  error
	observedGaps  sync.Map // key -> struct{}
)

func repoRoot() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..")
}

func loadKnownGaps() (map[string]knowngaps.Entry, error) {
	knownGapsOnce.Do(func() { knownGaps, knownGapsErr = knowngaps.Load(repoRoot()) })
	return knownGaps.Scope(knownGapScope), knownGapsErr
}

// gap reports a parity failure identified by key. A key listed in
// parity/known-gaps.toml is logged instead of failing; every other key fails.
func gap(t *testing.T, key, format string, args ...any) {
	t.Helper()
	observedGaps.Store(key, struct{}{})
	gaps, err := loadKnownGaps()
	if err != nil {
		t.Fatalf("load known gaps: %v", err)
	}
	msg := fmt.Sprintf(format, args...)
	if entry, ok := gaps[key]; ok {
		t.Logf("known gap [%s] (%s): %s", key, entry.Tracking, msg)
		return
	}
	t.Errorf("[%s] %s", key, msg)
}

// TestMain fails the run when a listed gap was not observed, so a fixed gap
// cannot linger in the ledger. The check only runs for a full, unfiltered
// run with the upstream mirror present, because a -run filter or a missing
// mirror legitimately leaves keys unobserved.
func TestMain(m *testing.M) {
	code := m.Run()
	if code != 0 || runFiltered() {
		os.Exit(code)
	}
	if _, err := LoadUpstream(); err != nil {
		os.Exit(code)
	}
	if _, err := loadKnownGaps(); err != nil {
		fmt.Fprintf(os.Stderr, "load known gaps: %v\n", err)
		os.Exit(1)
	}
	observed := make(map[string]struct{})
	observedGaps.Range(func(key, _ any) bool {
		observed[key.(string)] = struct{}{}
		return true
	})
	if stale := knownGaps.Stale(knownGapScope, observed); len(stale) > 0 {
		fmt.Fprintf(os.Stderr, "FAIL: parity/known-gaps.toml lists gaps that are now closed; remove them: %v\n", stale)
		os.Exit(1)
	}
	os.Exit(code)
}

func runFiltered() bool {
	for _, arg := range os.Args[1:] {
		for _, p := range []string{"-test.run", "-test.skip", "-test.list"} {
			if len(arg) >= len(p) && arg[:len(p)] == p {
				return true
			}
		}
	}
	return false
}
