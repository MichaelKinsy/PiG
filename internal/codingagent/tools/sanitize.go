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
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// ansiRegex mirrors upstream utils/ansi.ts ansiRegex (from chalk's
// ansi-regex): OSC sequences up to the first string terminator (BEL, ESC \
// or 0x9C), then CSI and related sequences introduced by ESC or the 8-bit
// CSI 0x9B.
var ansiRegex = lazyregexp.New(
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

// maxPendingAnsiLength is utils/ansi.ts MAX_PENDING_ANSI_LENGTH: the longest unfinished sequence held back while streaming, in
// UTF-16 code units. A longer one is processed as it is.
const maxPendingAnsiLength = 256

// unfinishedCSIAtEnd is the CSI half of utils/ansi.ts unfinishedAnsiAtEndRegex: an introducer, its optional intermediates and
// parameters, and no final byte before the end of the text.
var unfinishedCSIAtEnd = lazyregexp.New(`^[\x1b\x{9b}][\[\]()#;?]*(?:\d{1,4}(?:[;:]\d{0,4})*)?$`)

// unfinishedOSCAtEnd reports whether s, which starts with ESC ], is an OSC sequence without its string terminator: the
// OSC half of unfinishedAnsiAtEndRegex, `ESC](?:[^BEL U+009C ESC]|ESC(?!\\))*$`. A trailing ESC may start ESC \.
func unfinishedOSCAtEnd(s string) bool {
	if !strings.HasPrefix(s, "\x1b]") {
		return false
	}
	rest := s[2:]
	for rest != "" {
		r, size := utf8.DecodeRuneInString(rest)
		switch r {
		case '\a', '\u009c':
			return false
		case '\x1b':
			if strings.HasPrefix(rest[size:], "\\") {
				return false
			}
		}
		rest = rest[size:]
	}
	return true
}

// SplitIncompleteAnsiSuffix mirrors utils/ansi.ts splitIncompleteAnsiSuffix: it splits streamed text into a part that is safe to
// strip now and a trailing unfinished escape sequence that belongs in front of the next chunk. Only the last
// maxPendingAnsiLength UTF-16 code units are searched.
func SplitIncompleteAnsiSuffix(value string) (complete, pending string) {
	if !strings.Contains(value, "\x1b") && !strings.Contains(value, "\u009b") {
		return value, ""
	}
	windowStart, units := len(value), 0
	for windowStart > 0 {
		r, size := utf8.DecodeLastRuneInString(value[:windowStart])
		if units+utf16.RuneLen(r) > maxPendingAnsiLength {
			break
		}
		units += utf16.RuneLen(r)
		windowStart -= size
	}
	for i, r := range value[windowStart:] {
		if r != '\x1b' && r != '\u009b' {
			continue
		}
		tail := value[windowStart+i:]
		if unfinishedOSCAtEnd(tail) || unfinishedCSIAtEnd.MatchString(tail) {
			return value[:windowStart+i], tail
		}
	}
	return value, ""
}
