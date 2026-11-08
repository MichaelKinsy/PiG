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
		path:   "apps/p2/plugins/terminals/ghostty/testdata/golden.json",
		code:   codeOpaque,
		reason: "Pi's own answers (testdata/gen.mjs runs Pi's ghostty.ts over cases.json): the 6,684-character run is the base64 of a synthetic bracketed paste of x characters from the terminal-clipboard case, not a provider capture.",
	},
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
}
