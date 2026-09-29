// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright Joyent, Inc. and other Node contributors
// SPDX-FileCopyrightText: Copyright Node.js contributors
// SPDX-License-Identifier: MIT

package nodespawn

import (
	"strconv"
	"strings"
	"unicode/utf16"
)

const withoutNullBytes = "must be a string without null bytes"

// checkArguments is the argument validation of Node's normalizeSpawnArguments
// (lib/child_process.js, Node 24.19.0) for spawn(file, args, { cwd, env }). It
// runs on every platform before anything starts, and spawn throws its
// ERR_INVALID_ARG_VALUE: a file with a NUL, an empty file, then the first
// argument with a NUL, a cwd with a NUL (getValidatedPath), and the first env
// name or value with a NUL. An empty cwd is valid and inherits PiG's.
func checkArguments(file string, args []string, cwd string, env []string) *Error {
	if strings.IndexByte(file, 0) >= 0 {
		return invalidArgValue("file", file, withoutNullBytes)
	}
	if file == "" {
		return invalidArgValue("file", file, "cannot be empty")
	}
	for i, arg := range args {
		if strings.IndexByte(arg, 0) >= 0 {
			return invalidArgValue("args["+strconv.Itoa(i)+"]", arg, withoutNullBytes)
		}
	}
	if strings.IndexByte(cwd, 0) >= 0 {
		return invalidArgValue("options.cwd", cwd, "must be a string, Uint8Array, or URL without null bytes")
	}
	for _, pair := range env {
		name, value := envName(pair)
		property := "options.env['" + name + "']"
		if strings.IndexByte(name, 0) >= 0 {
			return invalidArgValue(property, name, withoutNullBytes)
		}
		if strings.IndexByte(value, 0) >= 0 {
			return invalidArgValue(property, value, withoutNullBytes)
		}
	}
	return nil
}

// envName splits an environment entry into its name and value. A name may
// start with "=", as Windows' per-drive directories do.
func envName(pair string) (string, string) {
	if i := strings.IndexByte(pair[min(1, len(pair)):], '='); i >= 0 {
		i += min(1, len(pair))
		return pair[:i], pair[i+1:]
	}
	return pair, ""
}

// invalidArgValue is Node's ERR_INVALID_ARG_VALUE TypeError
// (lib/internal/errors.js): "The argument 'name' reason. Received value", or
// "The property ..." when name contains a dot, where value is util.inspect's
// rendering cut to 128 UTF-16 units and "...".
func invalidArgValue(name, value, reason string) *Error {
	kind := "argument"
	if strings.Contains(name, ".") {
		kind = "property"
	}
	return &Error{
		Code:    "ERR_INVALID_ARG_VALUE",
		Thrown:  true,
		message: "The " + kind + " '" + name + "' " + reason + ". Received " + inspectReceived(value),
	}
}

const (
	// receivedLimit is the length of the inspected value ERR_INVALID_ARG_VALUE
	// keeps.
	receivedLimit = 128
	// inspectMinLineLength is util.inspect's kMinLineLength.
	inspectMinLineLength = 16
	// inspectBreakLength is util.inspect's default breakLength.
	inspectBreakLength = 80
)

// inspectReceived is util.inspect(value) for a string with the default
// options (lib/internal/util/inspect.js formatPrimitive), cut as
// ERR_INVALID_ARG_VALUE cuts it. A string longer than 76 UTF-16 units is split
// after each line feed, and the escaped pieces are joined by " +\n  ". Only
// the first 129 units are built, so a large argument costs one scan.
//
// A cut that splits a surrogate pair leaves JavaScript a lone high surrogate;
// a Go string holds U+FFFD there, as its JSON encoding does.
func inspectReceived(value string) string {
	out := unitWriter{limit: receivedLimit + 1}
	pieces := []string{value}
	if length := utf16Length(value); length > inspectMinLineLength && length > inspectBreakLength-4 {
		pieces = strings.SplitAfter(value, "\n")
		if len(pieces) > 1 && pieces[len(pieces)-1] == "" {
			pieces = pieces[:len(pieces)-1]
		}
	}
	for i, piece := range pieces {
		if i > 0 {
			out.writeString(" +\n  ")
		}
		writeEscapedString(&out, piece)
		if out.full() {
			break
		}
	}
	if out.count <= receivedLimit {
		return string(utf16.Decode(out.units))
	}
	return string(utf16.Decode(out.units[:receivedLimit])) + "..."
}

// writeEscapedString is util.inspect's strEscape: the string in single quotes,
// or in double quotes when it has a single quote and no double quote, or in
// backticks when it has both and neither a backtick nor "${". C0 controls,
// DEL and C1 controls, the backslash, and a single quote inside single quotes
// are escaped. Go strings hold no lone surrogates, which strEscape would
// escape.
func writeEscapedString(out *unitWriter, s string) {
	quote, escapeSingle := byte('\''), true
	if strings.Contains(s, "'") {
		switch {
		case !strings.Contains(s, `"`):
			quote, escapeSingle = '"', false
		case !strings.Contains(s, "`") && !strings.Contains(s, "${"):
			quote, escapeSingle = '`', false
		}
	}
	out.writeRune(rune(quote))
	for _, r := range s {
		switch {
		case r == '\'' && escapeSingle:
			out.writeString(`\'`)
		case r == '\\':
			out.writeString(`\\`)
		case r < 0x20 || (r >= 0x7f && r < 0xa0):
			out.writeString(inspectMeta(r))
		default:
			out.writeRune(r)
		}
		if out.full() {
			return
		}
	}
	out.writeRune(rune(quote))
}

// inspectMeta is util.inspect's meta entry for a C0 or C1 control or DEL.
func inspectMeta(r rune) string {
	switch r {
	case '\b':
		return `\b`
	case '\t':
		return `\t`
	case '\n':
		return `\n`
	case '\f':
		return `\f`
	case '\r':
		return `\r`
	}
	const hex = "0123456789ABCDEF"
	return `\x` + string(hex[r>>4]) + string(hex[r&0xf])
}

// unitWriter collects UTF-16 code units up to limit.
type unitWriter struct {
	units []uint16
	count int
	limit int
}

func (w *unitWriter) full() bool { return w.count >= w.limit }

func (w *unitWriter) writeRune(r rune) {
	if w.full() {
		return
	}
	w.units = utf16.AppendRune(w.units, r)
	w.count = len(w.units)
}

func (w *unitWriter) writeString(s string) {
	for _, r := range s {
		w.writeRune(r)
	}
}

// utf16Length is JavaScript's String length of s.
func utf16Length(s string) int {
	length := 0
	for _, r := range s {
		if r > 0xffff {
			length += 2
		} else {
			length++
		}
	}
	return length
}
