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
	"github.com/MichaelKinsy/PiG/internal/text"
)

// StripANSI mirrors upstream stripAnsi (see text.StripANSI). Anything its pattern does not cover (a lone ESC, ESC + letter outside the
// final-byte set) is left for SanitizeBinaryOutput.
// Ports packages/coding-agent/src/utils/shell.ts
func StripANSI(b []byte) []byte { return text.StripANSI(b) }

// SanitizeBinaryOutput mirrors upstream shell.ts sanitizeBinaryOutput (see text.SanitizeBinaryOutput).
func SanitizeBinaryOutput(s string) string { return text.SanitizeBinaryOutput(s) }

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
	rest := s[2:] //portlint:allow utf16units offsets come from utf8 decoding of this Go string, never from a JS index
	for rest != "" {
		r, size := utf8.DecodeRuneInString(rest)
		switch r {
		case '\a', '\u009c':
			return false
		case '\x1b':
			if strings.HasPrefix(rest[size:], "\\") { //portlint:allow utf16units offsets come from utf8 decoding of this Go string, never from a JS index
				return false
			}
		}
		rest = rest[size:] //portlint:allow utf16units offsets come from utf8 decoding of this Go string, never from a JS index
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
		r, size := utf8.DecodeLastRuneInString(value[:windowStart]) //portlint:allow utf16units offsets come from utf8 decoding of this Go string, never from a JS index
		if units+utf16.RuneLen(r) > maxPendingAnsiLength {
			break
		}
		units += utf16.RuneLen(r)
		windowStart -= size
	}
	for i, r := range value[windowStart:] { //portlint:allow utf16units offsets come from utf8 decoding of this Go string, never from a JS index
		if r != '\x1b' && r != '\u009b' {
			continue
		}
		tail := value[windowStart+i:] //portlint:allow utf16units offsets come from utf8 decoding of this Go string, never from a JS index
		if unfinishedOSCAtEnd(tail) || unfinishedCSIAtEnd.MatchString(tail) {
			return value[:windowStart+i], tail //portlint:allow utf16units offsets come from utf8 decoding of this Go string, never from a JS index
		}
	}
	return value, ""
}
