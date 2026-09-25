// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-FileCopyrightText: Copyright (c) Sindre Sorhus <sindresorhus@gmail.com> (https://sindresorhus.com)
// SPDX-License-Identifier: MIT

// Pure-Go ANSI strip + binary sanitizer used by bash output processing.
// Mirrors upstream's `strip-ansi` package and
// `sanitizeBinaryOutput` from packages/coding-agent/src/utils/shell.ts.
//
// We keep these in their own file (not pulling in a third-party dep)
// because the rules are tiny and we want predictable behavior under
// the parity audit.

package tools

import (
	"regexp"
	"strings"
)

// ansiRegex mirrors upstream utils/ansi.ts ansiRegex (from chalk's
// ansi-regex): OSC sequences up to the first string terminator (BEL, ESC \
// or 0x9C), then CSI and related sequences introduced by ESC or the 8-bit
// CSI 0x9B.
var ansiRegex = regexp.MustCompile(
	`(?:\x1b\][\s\S]*?(?:\x07|\x1b\\|\x{9c}))` +
		`|[\x1b\x{9b}][\[\]()#;?]*(?:\d{1,4}(?:[;:]\d{0,4})*)?[\dA-PR-TZcf-nq-uy=><~]`)

// StripANSI mirrors upstream stripAnsi: remove every ansiRegex match.
// Anything the pattern does not cover (a lone ESC, ESC + letter outside the
// final-byte set) is left for SanitizeBinaryOutput.
func StripANSI(b []byte) []byte {
	if len(b) == 0 {
		return b
	}
	// Fast path: ANSI codes require ESC (7-bit) or CSI (8-bit) introducer.
	if !strings.Contains(string(b), "\x1b") && !strings.Contains(string(b), "\u009b") {
		return b
	}
	return ansiRegex.ReplaceAll(b, nil)
}

// SanitizeBinaryOutput mirrors upstream shell.ts sanitizeBinaryOutput: drop
// control characters other than tab, newline and carriage return, and the
// Unicode format characters U+FFF9..U+FFFB (they crash string-width).
// Invalid UTF-8 bytes become U+FFFD, as upstream's decoder would have
// produced before sanitizing.
func SanitizeBinaryOutput(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			b.WriteRune(r)
		case r <= 0x1f:
		case r >= 0xfff9 && r <= 0xfffb:
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
