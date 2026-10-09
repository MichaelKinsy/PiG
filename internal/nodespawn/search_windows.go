// SPDX-FileCopyrightText: Copyright Joyent, Inc. and other Node contributors
// SPDX-FileCopyrightText: Copyright Node.js contributors
// SPDX-License-Identifier: MIT

//go:build windows

package nodespawn

import (
	"errors"
	"iter"
	"strings"
	"syscall"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
)

// notFoundError is libuv's ENOENT for a program search_path does not find,
// which the child emits.
func notFoundError(file string) *Error {
	return &Error{Code: "ENOENT", Syscall: "spawn " + file, errno: syscall.ENOENT}
}

// batchFileError is ProcessWrap::Spawn's UV_EINVAL for a batch file, which
// child_process.spawn throws.
func batchFileError() *Error {
	return &Error{Code: "EINVAL", Syscall: "spawn", Thrown: true, errno: syscall.EINVAL}
}

// spawnFailure is the error Node's spawn reports when uv_spawn fails with the
// Windows error err. libuv translates it (uv_translate_sys_error); Node emits
// EACCES, EAGAIN, EMFILE, ENFILE, and ENOENT as the child's error and throws
// any other code (lib/internal/child_process.js ChildProcess.spawn).
func spawnFailure(file string, err error) error {
	errno, ok := errors.AsType[syscall.Errno](err)
	if !ok {
		return err
	}
	code, ok := nodeerrno.Code(errno)
	if !ok {
		code = "UNKNOWN"
	}
	switch code {
	case "EACCES", "EAGAIN", "EMFILE", "ENFILE", "ENOENT":
		return &Error{Code: code, Syscall: "spawn " + file, errno: errno}
	}
	return &Error{Code: code, Syscall: "spawn", Thrown: true, errno: errno}
}

// windowsBatchFile is the pattern of Node's IsWindowsBatchFile
// (src/util-inl.h): the last extension, ignoring trailing spaces and dots.
// Node matches it with MSVC's std::regex, where \s is a C-locale space and $
// also matches before a line feed.
var windowsBatchFile = lazyregexp.New(`\.([a-zA-Z0-9]+)[\t\n\v\f\r ]*[.\t\n\v\f\r ]*(?m:$)`)

// isWindowsBatchFile is Node's IsWindowsBatchFile. Node rejects a file it
// matches before libuv searches for it: CreateProcessW would start a batch
// file through cmd.exe without quoting the arguments for cmd.exe.
func isWindowsBatchFile(file string) bool {
	match := windowsBatchFile.FindStringSubmatch(file)
	if match == nil {
		return false
	}
	extension := strings.ToLower(match[1])
	return extension == "cmd" || extension == "bat"
}

// searchPath is libuv's search_path (src/win/process.c, libuv 1.52.1 as
// bundled by Node 24.19.0) without the
// UV_PROCESS_WINDOWS_FILE_PATH_EXACT_NAME flag, which Node never sets. It
// returns "" when no candidate is a file.
//
// A file with a directory part is looked up only there, relative to cwd. A
// bare name is looked up in cwd when searchCwd is set
// (NeedCurrentDirectoryForExePathW), then in each directory of path. A name
// with an extension is tried as given first. Then libuv appends .com and .exe;
// it does not use PATHEXT, so "npm" never resolves to npm.cmd.
//
// libuv works on UTF-16 code units. Every character it tests is ASCII, and
// UTF-8 continuation bytes are never ASCII, so the same scan over UTF-8 bytes
// builds the same candidates.
func searchPath(file, cwd, path string, searchCwd bool, isFile func(string) bool) string {
	if file == "" || file == "." {
		return ""
	}
	nameStart := len(file)
	for nameStart > 0 && !isPathDelimiter(file[nameStart-1]) {
		nameStart--
	}
	dot := strings.IndexByte(file[nameStart:], '.')
	nameHasExt := dot >= 0 && nameStart+dot+1 < len(file)
	if nameStart > 0 {
		return walkExtensions(file[:nameStart], file[nameStart:], cwd, nameHasExt, isFile)
	}
	if searchCwd {
		if result := walkExtensions("", file, cwd, nameHasExt, isFile); result != "" {
			return result
		}
	}
	for dir := range pathDirectories(path) {
		if result := walkExtensions(dir, file, cwd, nameHasExt, isFile); result != "" {
			return result
		}
	}
	return ""
}

// walkExtensions is libuv's path_search_walk_ext.
func walkExtensions(dir, name, cwd string, nameHasExt bool, isFile func(string) bool) string {
	if nameHasExt {
		if candidate := joinCandidate(dir, name, "", cwd); isFile(candidate) {
			return candidate
		}
	}
	for _, extension := range [...]string{"com", "exe"} {
		if candidate := joinCandidate(dir, name, extension, cwd); isFile(candidate) {
			return candidate
		}
	}
	return ""
}

// joinCandidate is the path libuv's search_path_join_test builds from cwd,
// dir, name, and extension.
func joinCandidate(dir, name, extension, cwd string) string {
	switch {
	case len(dir) > 2 && isSlash(dir[0]) && isSlash(dir[1]):
		// A UNC path ignores cwd.
		cwd = ""
	case len(dir) >= 1 && isSlash(dir[0]):
		// A full path without a drive letter uses cwd's drive only: libuv
		// copies two code units of cwd. When cwd is shorter, the padding
		// terminator ends the candidate after cwd.
		prefix, complete := utf16Prefix(cwd, 2)
		if !complete {
			return cwd
		}
		cwd = prefix
	case len(dir) >= 2 && dir[1] == ':' && (len(dir) < 3 || !isSlash(dir[2])):
		// A drive-relative path uses cwd when cwd is on the same drive.
		if len(cwd) < 2 || !asciiEqualFold(cwd[:2], dir[:2]) {
			cwd = ""
		} else {
			dir = dir[2:]
		}
	case len(dir) > 2 && dir[1] == ':':
		// An absolute path with a drive letter ignores cwd.
		cwd = ""
	}
	var candidate strings.Builder
	candidate.WriteString(cwd)
	if cwd != "" && !isPathDelimiter(cwd[len(cwd)-1]) {
		candidate.WriteByte('\\')
	}
	candidate.WriteString(dir)
	if dir != "" && !isPathDelimiter(dir[len(dir)-1]) {
		candidate.WriteByte('\\')
	}
	candidate.WriteString(name)
	if extension != "" {
		if name != "" && name[len(name)-1] != '.' {
			candidate.WriteByte('.')
		}
		candidate.WriteString(extension)
	}
	return candidate.String()
}

// pathDirectories yields the directories of a PATH value as libuv's
// search_path slices them. Entries are separated by semicolons, except within
// an entry that starts with a double or single quote, which extends to the
// matching quote. Empty entries are skipped. One quote is removed from each
// end of an entry.
func pathDirectories(path string) iter.Seq[string] {
	return func(yield func(string) bool) {
		end := 0
		for end < len(path) {
			if end != 0 || path[0] == ';' {
				end++
			}
			start := end
			if start < len(path) && (path[start] == '"' || path[start] == '\'') {
				if closing := strings.IndexByte(path[start+1:], path[start]); closing >= 0 {
					end = start + 1 + closing
				} else {
					end = len(path)
				}
			}
			if separator := strings.IndexByte(path[end:], ';'); separator >= 0 {
				end += separator
			} else {
				end = len(path)
			}
			if end == start {
				continue
			}
			dir := path[start:end]
			if dir[0] == '"' || dir[0] == '\'' {
				dir = dir[1:]
			}
			if dir == "" {
				// libuv reads the removed quote as the entry's last
				// character and underflows the entry length here. The
				// port skips the entry.
				continue
			}
			if last := dir[len(dir)-1]; last == '"' || last == '\'' {
				dir = dir[:len(dir)-1]
			}
			if !yield(dir) {
				return
			}
		}
	}
}

func isSlash(c byte) bool { return c == '\\' || c == '/' }

func isPathDelimiter(c byte) bool { return c == '\\' || c == '/' || c == ':' }

// utf16Prefix returns the first n UTF-16 code units of s, as libuv copies
// them from a wide string, and whether s has n units.
func utf16Prefix(s string, n int) (string, bool) {
	units := utf16.Encode([]rune(s))
	if len(units) < n {
		return s, false
	}
	return string(utf16.Decode(units[:n])), true
}

// asciiEqualFold is _wcsnicmp's comparison of two ASCII-led prefixes.
func asciiEqualFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range len(a) {
		if lowerASCII(a[i]) != lowerASCII(b[i]) {
			return false
		}
	}
	return true
}

func lowerASCII(c byte) byte {
	if 'A' <= c && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}
