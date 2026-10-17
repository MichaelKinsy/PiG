// Package nodepath ports the parts of Node's path module Pi resolves file
// paths with. Resolve is path.resolve for the host platform. Win32Resolve and
// PosixResolve are path.win32.resolve and path.posix.resolve with the
// process state they read (working directory, per-drive working directories)
// passed in, so either flavor runs on any OS.
//
// Ports Node 24 lib/path.js resolve and normalizeString.
package nodepath

import (
	"bytes"
	"os"
	"runtime"
	"slices"
	"strings"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Env is the process state Node's path.resolve reads.
type Env struct {
	// Cwd is process.cwd().
	Cwd string
	// CwdErr is the error process.cwd() throws when the working directory cannot be read. Resolve returns it when, and only when, Node would call process.cwd().
	CwdErr error
	// DriveCwd is process.env["=<device>"], the Windows per-drive working directory, for a device such as "C:". It may be nil.
	DriveCwd func(device string) string
}

// Process returns the current process state.
func Process() Env {
	cwd, err := processCwd()
	return Env{Cwd: cwd, CwdErr: err, DriveCwd: func(device string) string { return os.Getenv("=" + device) }}
}

// Cwd is process.cwd(): the physical working directory (getcwd(3)), never a $PWD that reaches it through a symbolic link, as os.Getwd may return.
func Cwd() (string, error) { return processCwd() }

// Join is path.join(...paths) for the host platform. Unlike filepath.Join it keeps a trailing separator, as Node's does.
func Join(paths ...string) string {
	if runtime.GOOS == "windows" {
		return Win32Join(paths...)
	}
	return PosixJoin(paths...)
}

// Resolve is path.resolve(...paths) for the host platform. It returns the error process.cwd() throws when a relative path needs a working directory that cannot be read.
func Resolve(paths ...string) (string, error) {
	if runtime.GOOS == "windows" {
		return Win32Resolve(Process(), paths...)
	}
	// posix.resolve reads process.cwd() only when no argument is absolute. On Unix os.Getwd stats "." and $PWD, so skip it when the result cannot depend on it.
	if slices.ContainsFunc(paths, PosixIsAbsolute) {
		return PosixResolve(Env{}, paths...)
	}
	return PosixResolve(Process(), paths...)
}

const (
	backslash = '\\'
	slash     = '/'
)

func at(s string, i int) int {
	if i < 0 || i >= len(s) {
		return -1
	}
	return int(s[i])
}

func isSeparator(c int) bool { return c == slash || c == backslash }

func isDeviceRoot(c int) bool { return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' }

// cleanSpan reports the span path[start:end] that normalizeString returns when its result is a substring of path: the
// separator between two segments is always one separator byte and no segment is "." or "..". A path with no segment
// gives the empty span. ok is false when normalizeString has to build its result.
func cleanSpan(path string, separator byte, isSep func(int) bool) (start, end int, ok bool) {
	start, end = -1, 0
	segment := -1
	for i := 0; i <= len(path); i++ {
		if i < len(path) && !isSep(int(path[i])) {
			if segment < 0 {
				segment = i
			}
			continue
		}
		if segment < 0 {
			continue
		}
		if name := path[segment:i]; name == "." || name == ".." {
			return 0, 0, false
		}
		if start < 0 {
			start = segment
		} else if segment != end+1 || path[end] != separator {
			return 0, 0, false
		}
		end, segment = i, -1
	}
	if start < 0 {
		return 0, 0, true
	}
	return start, end, true
}

// normalizeString resolves "." and ".." segments the way Node's normalizeString does. A path that is already normal is
// returned as a substring of path, without allocating.
func normalizeString(path string, allowAboveRoot bool, separator byte, isSep func(int) bool) string {
	if start, end, ok := cleanSpan(path, separator, isSep); ok {
		return path[start:end]
	}
	return buildNormal(path, allowAboveRoot, separator, isSep)
}

// buildNormal is Node's normalizeString, building the result in one buffer.
func buildNormal(path string, allowAboveRoot bool, separator byte, isSep func(int) bool) string {
	res := make([]byte, 0, len(path)+2)
	lastSegmentLength := 0
	lastSlash := -1
	dots := 0
	code := 0
	for i := 0; i <= len(path); i++ {
		switch {
		case i < len(path):
			code = int(path[i])
		case isSep(code):
			return string(res)
		default:
			code = slash
		}
		switch {
		case isSep(code):
			switch {
			case lastSlash == i-1 || dots == 1:
			case dots == 2:
				if len(res) < 2 || lastSegmentLength != 2 || res[len(res)-1] != '.' || res[len(res)-2] != '.' {
					if len(res) > 2 {
						lastSlashIndex := len(res) - lastSegmentLength - 1
						if lastSlashIndex == -1 {
							res = res[:0]
							lastSegmentLength = 0
						} else {
							res = res[:lastSlashIndex]
							lastSegmentLength = len(res) - 1 - bytes.LastIndexByte(res, separator)
						}
						lastSlash = i
						dots = 0
						continue
					} else if len(res) != 0 {
						res = res[:0]
						lastSegmentLength = 0
						lastSlash = i
						dots = 0
						continue
					}
				}
				if allowAboveRoot {
					if len(res) > 0 {
						res = append(res, separator)
					}
					res = append(res, ".."...)
					lastSegmentLength = 2
				}
			default:
				if len(res) > 0 {
					res = append(res, separator)
				}
				res = append(res, path[lastSlash+1:i]...)
				lastSegmentLength = i - lastSlash - 1
			}
			lastSlash = i
			dots = 0
		case code == '.' && dots != -1:
			dots++
		default:
			dots = -1
		}
	}
	return string(res)
}

func isPosixSeparator(c int) bool { return c == slash }

// posixCwd is path.posix's view of process.cwd(). On Windows Node turns the backslashes into slashes and drops the drive:
// everything before the first "/" (slice(indexOf("/")), so a cwd with no "/" keeps its last character).
func posixCwd(cwd string) string {
	if runtime.GOOS != "windows" {
		return cwd
	}
	cwd = strings.ReplaceAll(cwd, `\`, "/")
	if at := strings.IndexByte(cwd, '/'); at >= 0 {
		return cwd[at:]
	}
	return cwd[max(len(cwd)-1, 0):]
}

// PosixResolve is path.posix.resolve(...paths). It returns env.CwdErr when Node would call process.cwd() and that throws.
func PosixResolve(env Env, paths ...string) (string, error) {
	if len(paths) == 0 || len(paths) == 1 && (paths[0] == "" || paths[0] == ".") {
		if env.CwdErr != nil {
			return "", env.CwdErr
		}
		if cwd := posixCwd(env.Cwd); at(cwd, 0) == slash {
			return cwd, nil
		}
	}
	// The rightmost nonempty argument decides the result alone when it is absolute; already normal, it resolves to a
	// substring of itself.
	for _, path := range slices.Backward(paths) {

		if path == "" {
			continue
		}
		if path[0] == slash {
			if start, end, ok := cleanSpan(path, slash, isPosixSeparator); ok {
				if start == end {
					return "/", nil
				}
				return path[start-1 : end], nil
			}
		}
		break
	}
	resolvedPath := ""
	resolvedAbsolute := false
	for i := len(paths) - 1; i >= -1 && !resolvedAbsolute; i-- {
		var path string
		switch {
		case i >= 0:
			path = paths[i]
		case env.CwdErr != nil:
			return "", env.CwdErr
		default:
			path = posixCwd(env.Cwd)
		}
		if len(path) == 0 {
			continue
		}
		resolvedPath = path + "/" + resolvedPath
		resolvedAbsolute = path[0] == slash
	}
	resolvedPath = normalizeString(resolvedPath, !resolvedAbsolute, slash, isPosixSeparator)
	if resolvedAbsolute {
		return "/" + resolvedPath, nil
	}
	if resolvedPath != "" {
		return resolvedPath, nil
	}
	return ".", nil
}

// driveCwdElsewhere is Node's check that a per-drive cwd does not point to device:
// path.slice(0, 2).toLowerCase() !== device.toLowerCase() && path.charCodeAt(2) === backslash,
// both indexed in UTF-16 code units.
func driveCwdElsewhere(path, device string) bool {
	units := utf16.Encode([]rune(path))
	if len(units) < 3 || units[2] != backslash {
		return false
	}
	return jsstring.ToLower(string(utf16.Decode(units[:2]))) != jsstring.ToLower(device)
}

// Win32Resolve is path.win32.resolve(...paths). Unlike filepath.Abs it keeps a
// trailing dot in a segment such as `x.` and it resolves a rooted path without
// a drive on the working directory's drive of the process, not of any base
// argument. It returns env.CwdErr when Node would call process.cwd() and that throws.
func Win32Resolve(env Env, paths ...string) (string, error) {
	resolvedDevice := ""
	resolvedTail := ""
	resolvedAbsolute := false

	for i := len(paths) - 1; i >= -1; i-- {
		var path string
		switch {
		case i >= 0:
			path = paths[i]
			if len(path) == 0 {
				continue
			}
		case len(resolvedDevice) == 0:
			if env.CwdErr != nil {
				return "", env.CwdErr
			}
			path = env.Cwd
			if len(paths) == 0 || (len(paths) == 1 && (paths[0] == "" || paths[0] == ".") && isSeparator(at(path, 0))) {
				return strings.ReplaceAll(path, "/", `\`), nil
			}
		default:
			if env.DriveCwd != nil {
				path = env.DriveCwd(resolvedDevice)
			}
			if path == "" {
				if env.CwdErr != nil {
					return "", env.CwdErr
				}
				path = env.Cwd
			}
			if driveCwdElsewhere(path, resolvedDevice) {
				path = resolvedDevice + `\`
			}
		}

		length := len(path)
		rootEnd := 0
		device := ""
		isAbsolute := false
		code := at(path, 0)

		switch {
		case length == 1:
			if isSeparator(code) {
				rootEnd = 1
				isAbsolute = true
			}
		case isSeparator(code):
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
							if firstPart != "." && firstPart != "?" {
								device = `\\` + firstPart + `\` + path[last:j]
								rootEnd = j
							} else {
								device = `\\` + firstPart
								rootEnd = 4
							}
						}
					}
				}
			} else {
				rootEnd = 1
			}
		case isDeviceRoot(code) && at(path, 1) == ':':
			device = path[:2]
			rootEnd = 2
			if length > 2 && isSeparator(at(path, 2)) {
				isAbsolute = true
				rootEnd = 3
			}
		}

		if len(device) > 0 {
			if len(resolvedDevice) > 0 {
				if jsstring.ToLower(device) != jsstring.ToLower(resolvedDevice) {
					continue
				}
			} else {
				resolvedDevice = device
			}
		}

		if resolvedAbsolute {
			if len(resolvedDevice) > 0 {
				break
			}
		} else {
			resolvedTail = path[min(rootEnd, len(path)):] + `\` + resolvedTail
			resolvedAbsolute = isAbsolute
			if isAbsolute && len(resolvedDevice) > 0 {
				break
			}
		}
	}

	resolvedTail = normalizeString(resolvedTail, !resolvedAbsolute, backslash, isSeparator)
	if resolvedAbsolute {
		return resolvedDevice + `\` + resolvedTail, nil
	}
	if result := resolvedDevice + resolvedTail; result != "" {
		return result, nil
	}
	return ".", nil
}

// IsAbsolute is path.isAbsolute for the host platform.
func IsAbsolute(path string) bool {
	if runtime.GOOS == "windows" {
		return Win32IsAbsolute(path)
	}
	return PosixIsAbsolute(path)
}

// PosixIsAbsolute is path.posix.isAbsolute.
func PosixIsAbsolute(path string) bool { return at(path, 0) == slash }

// Win32IsAbsolute is path.win32.isAbsolute. A path rooted at a separator is
// absolute even without a drive, so `\x` is absolute and `C:x` is not.
func Win32IsAbsolute(path string) bool {
	if len(path) == 0 {
		return false
	}
	code := at(path, 0)
	return isSeparator(code) || isDeviceRoot(code) && len(path) > 2 && at(path, 1) == ':' && isSeparator(at(path, 2))
}

// ToNamespacedPath is path.toNamespacedPath for the host platform: the identity
// on Unix, and on Windows the `\\?\` long form of an absolute drive or UNC path.
func ToNamespacedPath(path string) (string, error) {
	if runtime.GOOS == "windows" {
		return Win32ToNamespacedPath(Process(), path)
	}
	return path, nil
}

// Win32ToNamespacedPath is path.win32.toNamespacedPath. It returns path itself
// when it is empty or resolves to at most two UTF-16 code units, the `\\?\` long form
// of the resolved path when that is a drive or UNC path, and the resolved path
// otherwise, so a device or long path such as `\\.\pipe/x` comes back resolved.
func Win32ToNamespacedPath(env Env, path string) (string, error) {
	if path == "" {
		return path, nil
	}
	resolved, err := Win32Resolve(env, path)
	if err != nil {
		return "", err
	}
	if utf16Len(resolved) <= 2 {
		return path, nil
	}
	if resolved[0] == backslash {
		if at(resolved, 1) == backslash {
			if code := at(resolved, 2); code != '?' && code != '.' {
				return `\\?\UNC\` + resolved[2:], nil
			}
		}
	} else if isDeviceRoot(int(resolved[0])) && at(resolved, 1) == ':' && at(resolved, 2) == backslash {
		return `\\?\` + resolved, nil
	}
	return resolved, nil
}

// utf16Len is a JavaScript string's length: its UTF-16 code units.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}
