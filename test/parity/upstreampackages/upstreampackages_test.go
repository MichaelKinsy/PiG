// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package upstreampackages

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestListIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range All() {
		if p.Key == "" || p.Name == "" || p.Root != "packages/"+p.Key {
			t.Errorf("malformed package %+v", p)
		}
		if seen[p.Key] {
			t.Errorf("duplicate package %s", p.Key)
		}
		seen[p.Key] = true
	}
}

// TestListEqualsMirrorPackages fails when the upstream mirror gains or loses a package directory that the list does not. It skips when the mirror is absent, as the other parity tools do.
func TestListEqualsMirrorPackages(t *testing.T) {
	mirror := filepath.Join("..", "..", "..", ".upstream", "current", "packages")
	entries, err := os.ReadDir(mirror)
	if err != nil {
		t.Skipf("upstream mirror not present: %v", err)
	}
	var dirs []string
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, entry.Name())
		}
	}
	var listed []string
	for _, p := range All() {
		listed = append(listed, p.Key)
	}
	slices.Sort(dirs)
	slices.Sort(listed)
	if !slices.Equal(dirs, listed) {
		t.Errorf("packages.json lists %v, the mirror has %v", listed, dirs)
	}
}
