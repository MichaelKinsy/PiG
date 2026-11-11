// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"regexp"
	"strings"
)

// A fragment can name an earlier Pi release as a past fact ("as Pi 1.4.0 does"), and the public-claims check excuses it
// through an entry for the fragment directory in automation/ci/version-records.toml. The fold deletes the fragments and
// carries their bullets into a released CHANGELOG.md section, which that check does not read for such statements, so the
// entry then excuses nothing and the check fails it as stale ("names a missing file"). The fold therefore removes the
// entry it has made obsolete, in the same change that deletes the fragments.
var (
	recordHeader  = regexp.MustCompile(`(?m)^\[\[record\]\]`)
	recordPaths   = regexp.MustCompile(`(?s)(paths = \[)(.*?)(\])`)
	recordPathStr = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`)
)

// dropRecordEntry removes entry from the "paths" of every [[record]] of text, and a record left with no path. It leaves
// the rest of the file as it was, and returns text unchanged when no record names entry.
func dropRecordEntry(text, entry string) string {
	starts := recordHeader.FindAllStringIndex(text, -1)
	if len(starts) == 0 {
		return text
	}
	var out strings.Builder
	out.WriteString(text[:starts[0][0]])
	for i, start := range starts {
		end := len(text)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		out.WriteString(dropFromRecord(text[start[0]:end], entry))
	}
	return out.String()
}

// dropFromRecord returns block without entry in its paths, or "" when no path remains.
func dropFromRecord(block, entry string) string {
	loc := recordPaths.FindStringSubmatchIndex(block)
	if loc == nil {
		return block
	}
	body := block[loc[4]:loc[5]]
	var keep []string
	found := false
	for _, m := range recordPathStr.FindAllStringSubmatch(body, -1) {
		if m[1] == entry {
			found = true
			continue
		}
		keep = append(keep, m[0])
	}
	if !found {
		return block
	}
	if len(keep) == 0 {
		return ""
	}
	var rendered string
	if strings.Contains(body, "\n") {
		rendered = "\n  " + strings.Join(keep, ",\n  ") + ",\n"
	} else {
		rendered = strings.Join(keep, ", ")
	}
	return block[:loc[4]] + rendered + block[loc[5]:]
}

// dropRecordEntryFile applies dropRecordEntry to the file at path. A missing file has no entry to drop.
func dropRecordEntryFile(path, entry string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	out := dropRecordEntry(string(data), entry)
	if out == string(data) {
		return nil
	}
	return os.WriteFile(path, []byte(out), 0o644)
}
