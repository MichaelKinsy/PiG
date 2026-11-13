// SPDX-License-Identifier: MIT

package main

// allowed records a reviewed false positive: path is the exact repository-relative file and code the rule it trips. An entry that no longer matches a hit fails the gate, so the list only shrinks. Private-term findings (FG006) are never allowlisted.
type allowed struct {
	path   string
	code   string
	reason string
}

var allowlist = []allowed{
	{
		path:   "test/parity/scenarios/extensions-runtime/testdata/ext/overlay-qa-tests.ts",
		code:   codeHomePath,
		reason: "Pi 1.2.0's packages/coding-agent/examples/extensions/overlay-qa-tests.ts byte for byte (see 84-overlay-focus.toml); the /Users/<name>/ strings are upstream's published example source, not a capture.",
	},
	{
		path:   "tui/internal/hljs/testdata/corpus.json",
		code:   codeHomePath,
		reason: "highlight.js language sample corpus; the /home/kew string is a RenderMan RIB search-path sample in a public highlighter test input, not an account path.",
	},
	{
		path:   "internal/nodepath/testdata/resolve_cases.json",
		code:   codeHomePath,
		reason: "Oracle corpus for Node's path.win32.resolve; C:\\Users\\session. is a trailing-dot normalization case, not an account path.",
	},
	{
		path:   "automation/ci/preflight/content_test.go",
		code:   codeBilling,
		reason: "The pre-flight's own tests: synthetic minimal shapes made to prove the FG002 rules of automation/ci/preflight/forbidden.yaml fire, not captured provider data.",
	},
	{
		path:   "automation/ci/preflight/forbidden.yaml",
		code:   codeBilling,
		reason: "The pre-flight's FG002 rule text: the pattern that detects a billing or quota row, not a row.",
	},
}
