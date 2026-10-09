package cli

import (
	"bytes"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// cli/args.ts printHelp against pinned Pi: every line of Pi's help appears in Pig's, in order, after the program name (`pi` is `pig`), the
// configuration directory and the environment variable prefix are renamed. PiG's own sections are skipped, and the lines Pi 1.1.0 adds for
// `--tools +name/-name` belong to the tool-selection port (K1) and are listed as known gaps rather than hidden.
func TestHelpMatchesPi(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "node", "testdata/help.mjs")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	word := regexp.MustCompile(`\bpi\b`)
	rename := strings.NewReplacer("PI_CODING_AGENT", "PIG_CODING_AGENT", "PI_PACKAGE_DIR", "PIG_PACKAGE_DIR", "~/.pi/", "~/.pig/", "https://pi.dev/", "https://pig.dev/")
	spaces := regexp.MustCompile(` {2,}`)
	// Renaming an environment variable shifts its description column; Pig re-pads the line, so those lines compare by their words.
	squash := func(line string) string {
		if strings.HasPrefix(line, "  PIG_") {
			return spaces.ReplaceAllString(line, " ")
		}
		return line
	}
	var want []string
	for line := range strings.SplitSeq(strings.TrimRight(string(output), "\n"), "\n") {
		want = append(want, squash(rename.Replace(word.ReplaceAllString(line, "pig"))))
	}
	var help bytes.Buffer
	printHelp(&help, false)
	got := strings.Split(strings.TrimRight(help.String(), "\n"), "\n")
	for i := range got {
		got[i] = squash(got[i])
	}

	knownGaps := map[string]string{
		"                                 Only +name/-name entries add to or remove from the defaults":          "K1: --tools +name/-name",
		"  # Add codemode to the default tools":                                                                 "K1: --tools +name/-name",
		"  pig --tools +codemode":                                                                               "K1: --tools +name/-name",
		"  PIG_SHARE_VIEWER_URL              - Base URL for /share command (default: https://pig.dev/session/)": "PiG's /share has no viewer URL variable",
		"  PI_SHARE_VIEWER_URL              - Base URL for /share command (default: https://pig.dev/session/)":  "PiG's /share has no viewer URL variable",
	}
	// A known-gap example block ends with the blank line that separates examples; it is skipped with the block.
	var expected []string
	for i := 0; i < len(want); i++ {
		if _, known := knownGaps[want[i]]; known {
			if want[i] == "  pig --tools +codemode" && i+1 < len(want) && want[i+1] == "" {
				i++
			}
			continue
		}
		expected = append(expected, want[i])
	}
	next := 0
	for _, line := range expected {
		found := false
		for j := next; j < len(got); j++ {
			if got[j] == line {
				found, next = true, j+1
				break
			}
		}
		if !found {
			t.Errorf("Pi's help line is missing from Pig's help (or out of order): %q", line)
		}
	}
}
