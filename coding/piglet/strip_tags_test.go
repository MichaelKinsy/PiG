package piglet

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var stripTagPattern = regexp.MustCompile(`(!?)(pig_strip_[A-Za-z0-9_]+)`)

// TestStripBuildTagsHaveRegistrationShims pins the tag table against the
// source tree: every tag a Piglet Binary can be built with has a shim pair
// (a production file built without the tag and one built with it), and every
// pig_strip_ tag a production file names is in the table, so a renamed ID or
// a forgotten shim fails here instead of silently linking the feature.
func TestStripBuildTagsHaveRegistrationShims(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	on, off := map[string][]string{}, map[string][]string{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".upstream", "node_modules", "testdata", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		line, err := buildConstraint(path)
		if err != nil || line == "" {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		for _, match := range stripTagPattern.FindAllStringSubmatch(line, -1) {
			if match[1] == "!" {
				on[match[2]] = append(on[match[2]], rel)
			} else {
				off[match[2]] = append(off[match[2]], rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	tags := StripBuildTags()
	for _, tag := range tags {
		if len(on[tag]) == 0 || len(off[tag]) == 0 {
			t.Errorf("%s: shims built without the tag %v, with it %v; want at least one of each", tag, on[tag], off[tag])
		}
	}
	for tag, files := range on {
		if !slices.Contains(tags, tag) {
			t.Errorf("%s (%v) is not the tag of any strip ID", tag, files)
		}
	}
	for tag, files := range off {
		if !slices.Contains(tags, tag) {
			t.Errorf("%s (%v) is not the tag of any strip ID", tag, files)
		}
	}
}

func buildConstraint(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "//go:build ") {
			return line, nil
		}
		if strings.HasPrefix(line, "package ") {
			return "", nil
		}
	}
	return "", scanner.Err()
}
