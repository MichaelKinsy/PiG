//go:build !pig_strip_bedrock_converse_stream && !pig_strip_changelog && !pig_strip_codemode && !pig_strip_docs && !pig_strip_export_html && !pig_strip_extension_sdk_go && !pig_strip_extension_sdk_lisp && !pig_strip_extension_sdk_python && !pig_strip_extension_sdk_rust && !pig_strip_google_vertex && !pig_strip_llama_cpp && !pig_strip_mcp && !pig_strip_mermaid && !pig_strip_mistral_conversations && !pig_strip_node_extensions && !pig_strip_pig_login && !pig_strip_piglet_builder && !pig_strip_self_update && !pig_strip_syntax_highlight && !pig_strip_tool_search && !pig_strip_word_dictionaries

// Pi has no strip: the help of a build with strip tags drops the entries of what they strip (TestStripLeakGate covers that), so the
// comparison with Pi's help runs in a build without tags.

package cli

import (
	"bytes"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// cli/args.ts printHelp against pinned Pi: every line of Pi's help appears in Pig's, in order, after the program name (`pi` is `pig`), the
// configuration directory and the environment variable prefix are renamed. PiG's own sections are skipped.
func TestHelpMatchesPi(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "node", "testdata/help.mjs")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	word := regexp.MustCompile(`\bpi\b`)
	// D2 ("Product name in messages"): the help lists PiG's names of PI_OFFLINE, PI_SELF_UPDATE_COMMAND and PI_TELEMETRY; Pi's names are still read.
	rename := strings.NewReplacer("PI_CODING_AGENT", "PIG_CODING_AGENT", "PI_PACKAGE_DIR", "PIG_PACKAGE_DIR", "PI_OFFLINE", "PIG_OFFLINE", "PI_SELF_UPDATE_COMMAND", "PIG_SELF_UPDATE_COMMAND", "PI_TELEMETRY", "PIG_TELEMETRY", "~/.pi/", "~/.pig/", "https://pi.dev/", "https://pig.dev/")
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
		"  PIG_SHARE_VIEWER_URL              - Base URL for /share command (default: https://pig.dev/session/)": "PiG's /share has no viewer URL variable",
		"  PI_SHARE_VIEWER_URL              - Base URL for /share command (default: https://pig.dev/session/)":  "PiG's /share has no viewer URL variable",
	}
	var expected []string
	for i := 0; i < len(want); i++ {
		if _, known := knownGaps[want[i]]; known {
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

// Pi 2a46c420 (#11532, docs(coding-agent): document which working directory resumed sessions use): the help says which working directory each session flag uses. TestHelpMatchesPi compares it with Pi's own help.
func TestHelpCarriesPi2a46c420SessionFlagWording(t *testing.T) {
	var help bytes.Buffer
	printHelp(&help, false)
	for _, line := range []string{
		"  --continue, -c                 Continue the most recent session for the current directory",
		"  --resume, -r                   Pick a session to resume",
		"  --session <path|id>            Open a session by file path or full/partial ID",
		"  --session-id <id>              Use the current directory's existing session with the ID, creating it otherwise",
		"  --fork <path|id>               Fork a session by file path or full/partial ID into the current directory",
	} {
		if !strings.Contains(help.String(), line+"\n") {
			t.Errorf("help lacks %q", line)
		}
	}
}
