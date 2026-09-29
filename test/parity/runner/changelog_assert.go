//go:build parity

package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	pig "github.com/MichaelKinsy/PiG"
)

func readChangelogSource(bin BinaryRef, env []string) (string, error) {
	if bin.Label == "pig" {
		return pig.Changelog, nil
	}
	var root string
	for _, kv := range env {
		if key, value, ok := strings.Cut(kv, "="); ok && key == "PI_PACKAGE_DIR" {
			root = value
		}
	}
	if root == "" {
		var err error
		root, err = piPackageRoot(bin.Path)
		if err != nil {
			return "", err
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "CHANGELOG.md"))
	return string(data), err
}

var changelogSourceHeader = regexp.MustCompile(`^##[ \t]+\[?([0-9]+\.[0-9]+\.[0-9]+)\]?`)
var changelogRenderedHeader = regexp.MustCompile(`(?m)^\s*\[([0-9]+\.[0-9]+\.[0-9]+)\]`)

// expectedChangelogHeaders derives the denominator from the document input, independently of Pig's parser and renderer. Pi utils/changelog.ts accepts release headings and interactive-mode.ts renders all entries in reverse source order.
func expectedChangelogHeaders(source string) []string {
	var headers []string
	for line := range strings.SplitSeq(source, "\n") {
		if !strings.HasPrefix(line, "## ") {
			continue
		}
		if match := changelogSourceHeader.FindStringSubmatch(line); match != nil {
			headers = append(headers, match[1])
		}
	}
	slices.Reverse(headers)
	return headers
}

func evaluateChangelogHeaders(o *ScenarioOutcome) {
	for _, system := range []struct {
		name string
		runs []Result
	}{{"pig", o.Pig.Runs}, {"pi", o.Pi.Runs}} {
		for index, result := range system.runs {
			expected := expectedChangelogHeaders(result.ChangelogSource)
			if len(expected) == 0 {
				o.Failures = append(o.Failures, fmt.Sprintf("%s run %d: changelog assertion has no released document headers", system.name, index+1))
				continue
			}
			var actual []string
			for _, match := range changelogRenderedHeader.FindAllStringSubmatch(result.Output, -1) {
				actual = append(actual, match[1])
			}
			if !slices.Equal(actual, expected) {
				o.Failures = append(o.Failures, fmt.Sprintf("%s run %d: incomplete or reordered changelog headers: got %v want %v", system.name, index+1, actual, expected))
			}
		}
	}
}
