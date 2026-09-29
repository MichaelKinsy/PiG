// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright Joyent, Inc. and other Node contributors
// SPDX-License-Identifier: MIT

// Package nodespawn builds the Windows command line that Node's
// child_process.spawn(file, args) passes to CreateProcessW when neither shell
// nor windowsVerbatimArguments is set. Node prepends file to args as argv[0],
// and libuv's make_program_args (src/win/process.c, libuv 1.52.1 as bundled by
// Node 24.19.0) quotes every element with quote_cmd_arg and joins them with
// single spaces.
//
// The quoting differs from Go's syscall.EscapeArg: libuv quotes an argument
// that contains a double quote even when it has no space or tab. A receiver
// that does not parse its command line with the Microsoft C runtime rules,
// such as an MSYS2 or Cygwin program like Git Bash, then sees different argv.
package nodespawn

import (
	"slices"
	"strings"
)

// QuoteCmdArg is libuv's quote_cmd_arg. An empty argument becomes "". An
// argument without space, tab, or double quote is unchanged. One without a
// double quote or backslash is wrapped in double quotes. Otherwise it is
// wrapped in double quotes, each double quote is preceded by a backslash, and
// each backslash run that precedes a double quote or the end is doubled.
//
// libuv works on UTF-16 code units. Every character it tests is ASCII, and
// UTF-8 continuation bytes are never ASCII, so the same scan over UTF-8 bytes
// produces the same line once the result is converted to UTF-16.
func QuoteCmdArg(source string) string {
	if source == "" {
		// Need double quotation for empty argument.
		return `""`
	}
	if !strings.ContainsAny(source, " \t\"") {
		// No quotation needed.
		return source
	}
	if !strings.ContainsAny(source, "\"\\") {
		// No embedded double quotes or backslashes, so wrap quote marks
		// around the whole thing.
		return `"` + source + `"`
	}
	// libuv scans backwards, writing each character and then its escape,
	// and reverses the written span. The reversal restores the order of
	// multi-byte sequences as it does for UTF-16 code units.
	target := make([]byte, 0, 2*len(source))
	quoteHit := true
	for i := len(source); i > 0; i-- {
		c := source[i-1]
		target = append(target, c)
		switch {
		case quoteHit && c == '\\':
			target = append(target, '\\')
		case c == '"':
			quoteHit = true
			target = append(target, '\\')
		default:
			quoteHit = false
		}
	}
	slices.Reverse(target)
	return `"` + string(target) + `"`
}

// CommandLine is libuv's make_program_args with verbatim_arguments off: args,
// argv[0] included, each quoted by QuoteCmdArg and joined by single spaces.
func CommandLine(args []string) string {
	var line strings.Builder
	for i, arg := range args {
		if i > 0 {
			line.WriteByte(' ')
		}
		line.WriteString(QuoteCmdArg(arg))
	}
	return line.String()
}
