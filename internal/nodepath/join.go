package nodepath

import (
	"runtime"
	"slices"
	"strings"
	"unicode/utf8"
)

// Ports Node 24 lib/path.js (24.19, with the reserved device name handling of the CVE-2025-27210 fix) normalize, join, basename and dirname for both flavors; Resolve and IsAbsolute are in nodepath.go.

// PosixNormalize is path.posix.normalize.
func PosixNormalize(path string) string {
	if len(path) == 0 {
		return "."
	}
	isAbsolute := at(path, 0) == slash
	trailingSeparator := at(path, len(path)-1) == slash
	path = normalizeString(path, !isAbsolute, slash, isPosixSeparator)
	if len(path) == 0 {
		switch {
		case isAbsolute:
			return "/"
		case trailingSeparator:
			return "./"
		}
		return "."
	}
	if trailingSeparator {
		path += "/"
	}
	if isAbsolute {
		return "/" + path
	}
	return path
}

// PosixJoin is path.posix.join.
func PosixJoin(paths ...string) string {
	joined := ""
	started := false
	for _, arg := range paths {
		if len(arg) == 0 {
			continue
		}
		if !started {
			joined, started = arg, true
		} else {
			joined += "/" + arg
		}
	}
	if !started {
		return "."
	}
	return PosixNormalize(joined)
}

// PosixDirname is path.posix.dirname.
func PosixDirname(path string) string {
	if path == "" {
		return "."
	}
	hasRoot := path[0] == '/'
	end := -1
	matchedSlash := true
	for i := len(path) - 1; i >= 1; i-- {
		if path[i] == '/' {
			if !matchedSlash {
				end = i
				break
			}
		} else {
			matchedSlash = false
		}
	}
	if end == -1 {
		if hasRoot {
			return "/"
		}
		return "."
	}
	if hasRoot && end == 1 {
		return "//"
	}
	return path[:end]
}

// PosixBasename is path.posix.basename without a suffix.
func PosixBasename(path string) string { return basename(path, 0, isPosixSeparator) }

// Win32Basename is path.win32.basename without a suffix.
func Win32Basename(path string) string {
	start := 0
	// A drive letter prefix is not the separator of a trailing run of separators.
	if len(path) >= 2 && isDeviceRoot(at(path, 0)) && at(path, 1) == ':' {
		start = 2
	}
	return basename(path, start, isSeparator)
}

// Basename is path.basename without a suffix for the host platform.
func Basename(path string) string {
	if runtime.GOOS == "windows" {
		return Win32Basename(path)
	}
	return PosixBasename(path)
}

// Dirname is path.dirname for the host platform.
func Dirname(path string) string {
	if runtime.GOOS == "windows" {
		return Win32Dirname(path)
	}
	return PosixDirname(path)
}

// Win32Dirname is path.win32.dirname: a UNC root (\\server\share) or a drive root (C: or C:\) is kept, and the search for the last separator stops at it.
func Win32Dirname(path string) string {
	length := len(path)
	if length == 0 {
		return "."
	}
	code := at(path, 0)
	if length == 1 {
		if isSeparator(code) {
			return path
		}
		return "."
	}
	rootEnd, offset := -1, 0
	switch {
	case isSeparator(code):
		rootEnd, offset = 1, 1
		if isSeparator(at(path, 1)) {
			j := 2
			last := j
			for j < length && !isSeparator(at(path, j)) {
				j++
			}
			if j < length && j != last {
				last = j
				for j < length && isSeparator(at(path, j)) {
					j++
				}
				if j < length && j != last {
					last = j
					for j < length && !isSeparator(at(path, j)) {
						j++
					}
					if j == length {
						return path
					}
					if j != last {
						rootEnd, offset = j+1, j+1
					}
				}
			}
		}
	case isDeviceRoot(code) && at(path, 1) == ':':
		rootEnd = 2
		if length > 2 && isSeparator(at(path, 2)) {
			rootEnd = 3
		}
		offset = rootEnd
	}
	end := -1
	matchedSlash := true
	for i := length - 1; i >= offset; i-- {
		if isSeparator(at(path, i)) {
			if !matchedSlash {
				end = i
				break
			}
		} else {
			matchedSlash = false
		}
	}
	if end == -1 {
		if rootEnd == -1 {
			return "."
		}
		end = rootEnd
	}
	return path[:end]
}

func basename(path string, start int, isSep func(int) bool) string {
	end := -1
	matchedSlash := true
	for i := len(path) - 1; i >= start; i-- {
		if isSep(at(path, i)) {
			// A separator that is not part of the run of separators at the end of the string ends the search.
			if !matchedSlash {
				start = i + 1
				break
			}
		} else if end == -1 {
			matchedSlash = false
			end = i + 1
		}
	}
	if end == -1 {
		return ""
	}
	return path[start:end]
}

// Win32Normalize is path.win32.normalize.
func Win32Normalize(path string) string {
	length := len(path)
	if length == 0 {
		return "."
	}
	rootEnd := 0
	device := ""
	hasDevice := false
	isAbsolute := false
	code := at(path, 0)
	// A single character exits early.
	if length == 1 {
		if code == slash {
			return `\`
		}
		return path
	}
	switch {
	case isSeparator(code):
		// A separator first means an absolute path of some kind, UNC or not.
		isAbsolute = true
		if isSeparator(at(path, 1)) {
			j := 2
			last := j
			for j < length && !isSeparator(at(path, j)) {
				j++
			}
			if j < length && j != last {
				firstPart := path[last:j]
				last = j
				for j < length && isSeparator(at(path, j)) {
					j++
				}
				if j < length && j != last {
					last = j
					for j < length && !isSeparator(at(path, j)) {
						j++
					}
					if j == length || j != last {
						switch {
						case firstPart == "." || firstPart == "?":
							// A device root such as \\.\PHYSICALDRIVE0, or a reserved device name such as \\?\COM1:.
							device = `\\` + firstPart
							rootEnd = 4
							colon := strings.IndexByte(path, ':')
							if possible := sliceBytes(path, 4, colon+1); isWindowsReservedName(possible, len(possible)-1) {
								device = `\\?\` + possible
								rootEnd = 4 + len(possible)
							}
						case j == length:
							// A UNC root only: nothing is left to process.
							return `\\` + firstPart + `\` + path[last:] + `\`
						default:
							device = `\\` + firstPart + `\` + path[last:j]
							rootEnd = j
						}
						hasDevice = true
					}
				}
			}
		} else {
			rootEnd = 1
		}
	default:
		if colon := strings.IndexByte(path, ':'); colon > 0 {
			if isDeviceRoot(code) && colon == 1 {
				device = path[:2]
				hasDevice = true
				rootEnd = 2
				if length > 2 && isSeparator(at(path, 2)) {
					// A separator after the drive name makes the path absolute.
					isAbsolute = true
					rootEnd = 3
				}
			} else if isWindowsReservedName(path, colon) {
				device = path[:colon+1]
				hasDevice = true
				rootEnd = colon + 1
			}
		}
	}
	tail := ""
	if rootEnd < length {
		tail = normalizeString(path[rootEnd:], !isAbsolute, backslash, isSeparator)
	}
	if len(tail) == 0 && !isAbsolute {
		tail = "."
	}
	if len(tail) > 0 && isSeparator(at(path, length-1)) {
		tail += `\`
	}
	if !isAbsolute && !hasDevice && strings.Contains(path, ":") {
		// A relative path that is not tied to a device must not become one Windows reads as absolute (CVE-2024-36139).
		if len(tail) >= 2 && isDeviceRoot(at(tail, 0)) && at(tail, 1) == ':' {
			return `.\` + tail
		}
		for index := strings.IndexByte(path, ':'); index != -1; {
			if index == length-1 || isSeparator(at(path, index+1)) {
				return `.\` + tail
			}
			next := strings.IndexByte(path[index+1:], ':')
			if next == -1 {
				break
			}
			index += 1 + next
		}
	}
	if isWindowsReservedName(path, strings.IndexByte(path, ':')) {
		return `.\` + device + tail
	}
	if !hasDevice {
		if isAbsolute {
			return `\` + tail
		}
		return tail
	}
	if isAbsolute {
		return device + `\` + tail
	}
	return device + tail
}

var windowsReservedNames = []string{
	"CON", "PRN", "AUX", "NUL",
	"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
	"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9",
	"COM\u00b9", "COM\u00b2", "COM\u00b3",
	"LPT\u00b9", "LPT\u00b2", "LPT\u00b3",
}

// isWindowsReservedName is Node's isWindowsReservedName: whether path.slice(0, colonIndex) upper-cased is a reserved device name. A
// colonIndex of -1 (no colon) slices off the last UTF-16 code unit, so "CONx" counts.
func isWindowsReservedName(path string, colonIndex int) bool {
	prefix := path
	if colonIndex >= 0 {
		prefix = path[:min(colonIndex, len(path))]
	} else if path != "" {
		r, size := utf8.DecodeLastRuneInString(path)
		if r > 0xFFFF {
			return false
		}
		prefix = path[:len(path)-size]
	}
	return slices.Contains(windowsReservedNames, strings.ToUpper(prefix))
}

// sliceBytes is path.slice(start, end) for start and end at byte offsets: an end at or before start gives "".
func sliceBytes(path string, start, end int) string {
	end = min(end, len(path))
	if start >= end {
		return ""
	}
	return path[start:end]
}

// Win32Join is path.win32.join.
func Win32Join(paths ...string) string {
	joined, firstPart := "", ""
	started := false
	for _, arg := range paths {
		if len(arg) == 0 {
			continue
		}
		if !started {
			joined, firstPart, started = arg, arg, true
		} else {
			joined += `\` + arg
		}
	}
	if !started {
		return "."
	}
	// The joined path must not start with two slashes, which normalize reads as a UNC root, unless the first part
	// clearly is one: exactly two slashes followed by a non-slash character.
	needsReplace := true
	slashCount := 0
	if isSeparator(at(firstPart, 0)) {
		slashCount++
		if len(firstPart) > 1 && isSeparator(at(firstPart, 1)) {
			slashCount++
			if len(firstPart) > 2 {
				if isSeparator(at(firstPart, 2)) {
					slashCount++
				} else {
					needsReplace = false
				}
			}
		}
	}
	if needsReplace {
		for slashCount < len(joined) && isSeparator(at(joined, slashCount)) {
			slashCount++
		}
		if slashCount >= 2 {
			joined = `\` + joined[slashCount:]
		}
	}
	// A reserved device name such as "CON:" keeps the joined path as it is, with forward slashes made backslashes.
	if joinedHasReservedPart(joined) {
		return strings.ReplaceAll(joined, "/", `\`)
	}
	return Win32Normalize(joined)
}

// joinedHasReservedPart reports whether a backslash-separated part of the joined path names a reserved device before its first colon.
func joinedHasReservedPart(joined string) bool {
	for part := range strings.SplitSeq(joined, `\`) {
		if colon := strings.IndexByte(part, ':'); colon != -1 && isWindowsReservedName(part, colon) {
			return true
		}
	}
	return false
}
