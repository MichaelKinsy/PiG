package nodepath

import (
	"runtime"
	"strings"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Relative is path.relative(from, to) for the host platform.
func Relative(from, to string) (string, error) {
	if runtime.GOOS == "windows" {
		return Win32Relative(Process(), from, to)
	}
	return PosixRelative(Process(), from, to)
}

func units(s string) []uint16 { return utf16.Encode([]rune(s)) }

// jsSlice is String.prototype.slice for non-negative indices: out-of-range ends clamp. The indices come from the lowercased path, whose length can differ from the original's.
func jsSlice(u []uint16, start, end int) []uint16 {
	end = min(end, len(u))
	if start >= end {
		return nil
	}
	return u[start:end]
}

func text(u []uint16) string { return string(utf16.Decode(u)) }

// PosixRelative is path.posix.relative(from, to) with the process state path.resolve reads passed in. It returns env.CwdErr when resolving a relative argument needs a working directory that cannot be read.
func PosixRelative(env Env, from, to string) (string, error) {
	if from == to {
		return "", nil
	}
	fromResolved, err := PosixResolve(env, from)
	if err != nil {
		return "", err
	}
	toResolved, err := PosixResolve(env, to)
	if err != nil {
		return "", err
	}
	if fromResolved == toResolved {
		return "", nil
	}
	f, t := units(fromResolved), units(toResolved)
	const fromStart, toStart = 1, 1
	fromEnd := len(f)
	fromLen, toLen := fromEnd-fromStart, len(t)-toStart
	length := min(fromLen, toLen)
	lastCommonSep := -1
	i := 0
	for ; i < length; i++ {
		code := f[fromStart+i]
		if code != t[toStart+i] {
			break
		}
		if code == slash {
			lastCommonSep = i
		}
	}
	if i == length {
		if toLen > length {
			if t[toStart+i] == slash {
				return text(t[toStart+i+1:]), nil
			}
			if i == 0 {
				return text(t[toStart+i:]), nil
			}
		} else if fromLen > length {
			if f[fromStart+i] == slash {
				lastCommonSep = i
			} else if i == 0 {
				lastCommonSep = 0
			}
		}
	}
	var out []uint16
	for i = fromStart + lastCommonSep + 1; i <= fromEnd; i++ {
		if i == fromEnd || f[i] == slash {
			if len(out) == 0 {
				out = append(out, '.', '.')
			} else {
				out = append(out, '/', '.', '.')
			}
		}
	}
	return text(append(out, t[toStart+lastCommonSep:]...)), nil
}

// Win32Relative is path.win32.relative(from, to) with the process state path.resolve reads passed in. It returns env.CwdErr when resolving a relative argument needs a working directory that cannot be read.
func Win32Relative(env Env, from, to string) (string, error) {
	if from == to {
		return "", nil
	}
	fromOrig, err := Win32Resolve(env, from)
	if err != nil {
		return "", err
	}
	toOrig, err := Win32Resolve(env, to)
	if err != nil {
		return "", err
	}
	if fromOrig == toOrig {
		return "", nil
	}
	fromLower, toLower := jsstring.ToLower(fromOrig), jsstring.ToLower(toOrig)
	if fromLower == toLower {
		return "", nil
	}
	f, t := units(fromLower), units(toLower)
	toOrigUnits := units(toOrig)
	// Lowercasing changed a length, so indices into the lowercased paths do not address the originals: Node compares
	// the segments instead.
	if len(units(fromOrig)) != len(f) || len(toOrigUnits) != len(t) {
		return win32RelativeSegments(fromOrig, toOrig), nil
	}
	fromStart := 0
	for fromStart < len(f) && f[fromStart] == backslash {
		fromStart++
	}
	fromEnd := len(f)
	for fromEnd-1 > fromStart && f[fromEnd-1] == backslash {
		fromEnd--
	}
	fromLen := fromEnd - fromStart
	toStart := 0
	for toStart < len(t) && t[toStart] == backslash {
		toStart++
	}
	toEnd := len(t)
	for toEnd-1 > toStart && t[toEnd-1] == backslash {
		toEnd--
	}
	toLen := toEnd - toStart
	length := min(fromLen, toLen)
	lastCommonSep := -1
	i := 0
	for ; i < length; i++ {
		code := f[fromStart+i]
		if code != t[toStart+i] {
			break
		}
		if code == backslash {
			lastCommonSep = i
		}
	}
	if i != length {
		if lastCommonSep == -1 {
			return toOrig, nil
		}
	} else {
		if toLen > length {
			if t[toStart+i] == backslash {
				return text(jsSlice(toOrigUnits, toStart+i+1, len(toOrigUnits))), nil
			}
			if i == 2 {
				return text(jsSlice(toOrigUnits, toStart+i, len(toOrigUnits))), nil
			}
		}
		if fromLen > length {
			if f[fromStart+i] == backslash {
				lastCommonSep = i
			} else if i == 2 {
				lastCommonSep = 3
			}
		}
		if lastCommonSep == -1 {
			lastCommonSep = 0
		}
	}
	var out []uint16
	for i = fromStart + lastCommonSep + 1; i <= fromEnd; i++ {
		if i == fromEnd || f[i] == backslash {
			if len(out) == 0 {
				out = append(out, '.', '.')
			} else {
				out = append(out, '\\', '.', '.')
			}
		}
	}
	toStart += lastCommonSep
	if len(out) > 0 {
		return text(append(out, jsSlice(toOrigUnits, toStart, toEnd)...)), nil
	}
	if toStart < len(toOrigUnits) && toOrigUnits[toStart] == backslash {
		toStart++
	}
	return text(jsSlice(toOrigUnits, toStart, toEnd)), nil
}

// win32RelativeSegments is the branch of path.win32.relative for resolved paths whose lowercase form has another length
// than the original: the paths split at backslashes, a trailing empty segment dropped, and compared segment by segment
// in lowercase.
func win32RelativeSegments(fromOrig, toOrig string) string {
	fromSplit, toSplit := strings.Split(fromOrig, `\`), strings.Split(toOrig, `\`)
	if fromSplit[len(fromSplit)-1] == "" {
		fromSplit = fromSplit[:len(fromSplit)-1]
	}
	if toSplit[len(toSplit)-1] == "" {
		toSplit = toSplit[:len(toSplit)-1]
	}
	fromLen, toLen := len(fromSplit), len(toSplit)
	length := min(fromLen, toLen)
	i := 0
	for ; i < length; i++ {
		if jsstring.ToLower(fromSplit[i]) != jsstring.ToLower(toSplit[i]) {
			break
		}
	}
	switch {
	case i == 0:
		return toOrig
	case i == length && toLen > length:
		return strings.Join(toSplit[i:], `\`)
	case i == length && fromLen > length:
		return strings.Repeat(`..\`, fromLen-1-i) + ".."
	case i == length:
		return ""
	}
	return strings.Repeat(`..\`, fromLen-i) + strings.Join(toSplit[i:], `\`)
}
