// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-FileCopyrightText: Copyright (c) Sindre Sorhus <sindresorhus@gmail.com> (https://sindresorhus.com)
// SPDX-License-Identifier: MIT

package text

import (
	"bytes"
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// ansiRegex mirrors upstream utils/ansi.ts ansiRegex (from chalk's
// ansi-regex): OSC sequences up to the first string terminator (BEL, ESC \
// or 0x9C), then CSI and related sequences introduced by ESC or the 8-bit
// CSI 0x9B. A digit is a valid final byte, so an unfinished "ESC[38;5;" loses
// only "ESC[38;5".
var ansiRegex = lazyregexp.New(
	`(?:\x1b\][\s\S]*?(?:\x07|\x1b\\|\x{9c}))` +
		`|[\x1b\x{9b}][\[\]()#;?]*(?:\d{1,4}(?:[;:]\d{0,4})*)?[\dA-PR-TZcf-nq-uy=><~]`)

// csi8 is the UTF-8 encoding of the 8-bit CSI introducer U+009B.
const csi8 = "\u009b"

var csi8Bytes = []byte(csi8)

// StripANSI mirrors upstream utils/ansi.ts stripAnsi: it removes every ansiRegex match. Text without an introducer is returned as it is.
func StripANSI(b []byte) []byte {
	// Fast path: ANSI codes require ESC (7-bit) or CSI (8-bit) introducer.
	if bytes.IndexByte(b, '\x1b') < 0 && !bytes.Contains(b, csi8Bytes) {
		return b
	}
	return ansiRegex.ReplaceAll(b, nil)
}

// StripANSIString is StripANSI of a string. Text without an introducer is returned as it is, without a copy.
func StripANSIString(s string) string {
	if strings.IndexByte(s, '\x1b') < 0 && !strings.Contains(s, csi8) {
		return s
	}
	return string(ansiRegex.ReplaceAll([]byte(s), nil))
}

// SanitizeBinaryOutput mirrors upstream shell.ts sanitizeBinaryOutput: drop
// control characters other than tab, newline and carriage return, and the
// Unicode format characters U+FFF9..U+FFFB (they crash string-width).
// Invalid UTF-8 bytes become U+FFFD, as upstream's decoder would have
// produced before sanitizing. Text with nothing to change is returned as it
// is, without a copy.
func SanitizeBinaryOutput(s string) string {
	if !needsSanitizing(s) {
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

// needsSanitizing reports whether SanitizeBinaryOutput changes s: a control character other than tab, newline and carriage return, a U+FFF9..U+FFFB
// (EF BF B9..BB), or invalid UTF-8.
func needsSanitizing(s string) bool {
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c < 0x20:
			if c != '\t' && c != '\n' && c != '\r' {
				return true
			}
		case c == 0xEF:
			if i+2 < len(s) && s[i+1] == 0xBF && s[i+2] >= 0xB9 && s[i+2] <= 0xBB {
				return true
			}
		}
	}
	return !utf8.ValidString(s)
}
