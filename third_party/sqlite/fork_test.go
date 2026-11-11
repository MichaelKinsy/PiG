// Copyright 2026 The Sqlite Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package sqlite

import (
	"os"
	"regexp"
	"testing"
)

// This directory is a copy of the driver layer of one modernc.org/sqlite release, and the transpiled library it drives
// (modernc.org/sqlite/lib) comes from the module go.mod requires. The two must be the same release: a bump of the
// requirement without a new copy runs the old driver layer against a new library.
func TestForkIsTheReleaseGoModRequires(t *testing.T) {
	version := regexp.MustCompile(`modernc\.org/sqlite (v\d+\.\d+\.\d+)`)
	find := func(file string) string {
		t.Helper()
		text, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		match := version.FindSubmatch(text)
		if match == nil {
			t.Fatalf("%s names no modernc.org/sqlite version", file)
		}
		return string(match[1])
	}
	required, forked := find("../../go.mod"), find("FORK.md")
	if required != forked {
		t.Fatalf("go.mod requires modernc.org/sqlite %s but FORK.md says this directory is %s: copy the %s driver layer over it (see FORK.md, Updating)", required, forked, required)
	}
}
