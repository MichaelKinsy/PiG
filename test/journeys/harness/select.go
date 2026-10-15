package harness

import (
	"fmt"
	"os/exec"
	"slices"
	"strings"
)

// AlwaysRun lists path prefixes whose change selects every journey: the program entry point, the journey infrastructure and the lock-order checker they depend on, the module definition, and the packages every journey's comparison passes through. Every journey compares provider requests (ai/, agent/), session files (internal/sessionentry/ and the encoders under internal/) and frames, so a change there selects all of them even when no journey names the package.
var AlwaysRun = []string{"cmd/pig/", "test/journeys/", "internal/", "ai/", "agent/", "go.mod", "go.sum"}

// ignorable reports files no journey can observe: documentation and changelog fragments.
func ignorable(path string) bool {
	return strings.HasSuffix(path, ".md") || strings.HasPrefix(path, "docs/") || strings.HasPrefix(path, "changelog.d/")
}

// Select returns the names of the journeys a change set touches, in registry order. A file selects a journey when it lies under one of the journey's Touches prefixes; a file under AlwaysRun selects all. Test files and documentation select nothing by themselves, except that a test file next to a prefix still selects through that prefix, because the package it tests is what the journeys exercise.
func Select(journeys []*Journey, changed []string) []string {
	all := false
	var files []string
	for _, f := range changed {
		f = strings.TrimSpace(f)
		if f == "" || ignorable(f) {
			continue
		}
		for _, p := range AlwaysRun {
			if strings.HasPrefix(f, p) {
				all = true
			}
		}
		files = append(files, f)
	}
	var out []string
	for _, j := range journeys {
		if all || slices.ContainsFunc(files, func(f string) bool {
			return slices.ContainsFunc(j.Touches, func(p string) bool { return strings.HasPrefix(f, p) })
		}) {
			out = append(out, j.Name)
		}
	}
	return out
}

// ChangedFiles lists the files changed since the merge base with base, plus uncommitted and untracked files, relative to the repository root.
func ChangedFiles(dir, base string) ([]string, error) {
	run := func(args ...string) ([]string, error) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return strings.Fields(string(out)), nil
	}
	committed, err := run("diff", "--name-only", base+"...HEAD")
	if err != nil {
		return nil, err
	}
	dirty, err := run("diff", "--name-only", "HEAD")
	if err != nil {
		return nil, err
	}
	untracked, err := run("ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	return slices.Concat(committed, dirty, untracked), nil
}
