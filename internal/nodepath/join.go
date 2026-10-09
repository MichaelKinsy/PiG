package nodepath

import "strings"

// Ports Node 24 lib/path.js normalize, join and basename for both flavors; Resolve and IsAbsolute are in nodepath.go.

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
					if j == length {
						// A UNC root only: nothing is left to process.
						return `\\` + firstPart + `\` + path[last:] + `\`
					}
					if j != last {
						if firstPart != "." && firstPart != "?" {
							device = `\\` + firstPart + `\` + path[last:j]
							rootEnd = j
						} else {
							// A device root such as \\.\PHYSICALDRIVE0.
							device = `\\` + firstPart
							rootEnd = 4
						}
						hasDevice = true
					}
				}
			}
		} else {
			rootEnd = 1
		}
	case isDeviceRoot(code) && at(path, 1) == ':':
		device = path[:2]
		hasDevice = true
		rootEnd = 2
		if length > 2 && isSeparator(at(path, 2)) {
			// A separator after the drive name makes the path absolute.
			isAbsolute = true
			rootEnd = 3
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
	return Win32Normalize(joined)
}
