package codingagent

import (
	"strings"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// The Windows canonical-path walk follows Node's fs.realpathSync (lib/fs.js splitRoot, nextPart and realpathSync) and path.win32.resolve (lib/path.js) with Windows semantics. The code carries no operating-system dependency: filepath's behavior follows the host, but a rooted link target such as \dir is resolved by Node against the device of the path that reached the link, not the process drive. Tests run it on every host against Node's own functions.

// win32Process supplies the two process facts path.win32.resolve reads when a path names a drive without a directory: process.cwd() and process.env["=D:"].
type win32Process struct {
	cwd    func() string
	getenv func(string) string
}

// nodeEntryKind is what libuv's lstat reports for one path component. A volume mount point is reported as a directory: libuv treats only drive-target mount-point reparse records as symbolic links.
type nodeEntryKind int

const (
	nodeEntryPlain nodeEntryKind = iota
	nodeEntryLink
	nodeEntryMount
)

// nodeRealpathFS is the filesystem Node's realpathSync reads. lstat reports the kind of one component and, for a link, its target. stat fails for a dangling or cyclic link.
type nodeRealpathFS struct {
	lstat func(path string) (kind nodeEntryKind, target string, err error)
	stat  func(path string) error
}

// win32SplitRootRe is the splitRootRe of lib/fs.js.
var win32SplitRootRe = lazyregexp.New(`^(?:[a-zA-Z]:|[\\/]{2}[^\\/]+[\\/][^\\/]+)?[\\/]*`)

// win32UNCRootRe matches the leading part of a UNC or device root.
var win32UNCRootRe = lazyregexp.New(`^[\\/]{2}[^\\/]+[\\/][^\\/]+`)

func isWin32Separator(c byte) bool { return c == '\\' || c == '/' }

func isWin32DeviceRoot(c byte) bool { return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' }

// win32SplitRoot is lib/fs.js splitRoot for Windows.
func win32SplitRoot(path string) string { return win32SplitRootRe.FindString(path) }

// win32TargetNeedsNodeResolution reports a link target whose resolution differs between Node and filepath.EvalSymlinks: a target rooted without a device (\dir) or naming a drive without a directory (D:dir). Node resolves the first on the device of the path that reached the link and the second on the current directory of that drive.
func win32TargetNeedsNodeResolution(target string) bool {
	if target == "" {
		return false
	}
	if isWin32Separator(target[0]) {
		return !win32UNCRootRe.MatchString(target)
	}
	return len(target) >= 2 && isWin32DeviceRoot(target[0]) && target[1] == ':' && (len(target) == 2 || !isWin32Separator(target[2]))
}

// win32Resolve is path.win32.resolve.
func win32Resolve(proc win32Process, args ...string) string {
	resolvedDevice := ""
	resolvedTail := ""
	resolvedAbsolute := false
	for i := len(args) - 1; i >= -1; i-- {
		var path string
		switch {
		case i >= 0:
			path = args[i]
			if path == "" {
				continue
			}
		case resolvedDevice == "":
			path = proc.cwd()
			if len(args) == 0 || len(args) == 1 && (args[0] == "" || args[0] == ".") && path != "" && isWin32Separator(path[0]) {
				return path
			}
		default:
			path = proc.getenv("=" + resolvedDevice)
			if path == "" {
				path = proc.cwd()
			}
			if !strings.EqualFold(safePrefix(path, 2), resolvedDevice) && len(path) > 2 && path[2] == '\\' {
				path = resolvedDevice + `\`
			}
		}

		length := len(path)
		rootEnd := 0
		device := ""
		isAbsolute := false
		var code byte
		if length > 0 {
			code = path[0]
		}
		switch {
		case length == 1:
			if isWin32Separator(code) {
				rootEnd = 1
				isAbsolute = true
			}
		case length > 1 && isWin32Separator(code):
			isAbsolute = true
			if isWin32Separator(path[1]) {
				j := 2
				last := j
				for j < length && !isWin32Separator(path[j]) {
					j++
				}
				if j < length && j != last {
					firstPart := path[last:j]
					last = j
					for j < length && isWin32Separator(path[j]) {
						j++
					}
					if j < length && j != last {
						last = j
						for j < length && !isWin32Separator(path[j]) {
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
		case length > 1 && isWin32DeviceRoot(code) && path[1] == ':':
			device = path[:2]
			rootEnd = 2
			if length > 2 && isWin32Separator(path[2]) {
				isAbsolute = true
				rootEnd = 3
			}
		}

		if device != "" {
			if resolvedDevice != "" {
				if !strings.EqualFold(device, resolvedDevice) {
					continue
				}
			} else {
				resolvedDevice = device
			}
		}

		if resolvedAbsolute {
			if resolvedDevice != "" {
				break
			}
		} else {
			resolvedTail = path[min(rootEnd, length):] + `\` + resolvedTail
			resolvedAbsolute = isAbsolute
			if isAbsolute && resolvedDevice != "" {
				break
			}
		}
	}

	resolvedTail = win32NormalizeString(resolvedTail, !resolvedAbsolute)
	if resolvedAbsolute {
		return resolvedDevice + `\` + resolvedTail
	}
	if joined := resolvedDevice + resolvedTail; joined != "" {
		return joined
	}
	return "."
}

func safePrefix(s string, n int) string {
	if len(s) < n {
		return s
	}
	return s[:n]
}

// win32NormalizeString is normalizeString of lib/path.js with the Windows separator.
func win32NormalizeString(path string, allowAboveRoot bool) string {
	res := ""
	lastSegmentLength := 0
	lastSlash := -1
	dots := 0
	var code byte
	for i := 0; i <= len(path); i++ {
		switch {
		case i < len(path):
			code = path[i]
		case isWin32Separator(code):
			return res
		default:
			code = '/'
		}
		switch {
		case isWin32Separator(code):
			switch {
			case lastSlash == i-1 || dots == 1:
			case dots == 2:
				if len(res) < 2 || lastSegmentLength != 2 || res[len(res)-1] != '.' || res[len(res)-2] != '.' {
					if len(res) > 2 {
						lastSlashIndex := len(res) - lastSegmentLength - 1
						if lastSlashIndex == -1 {
							res = ""
							lastSegmentLength = 0
						} else {
							res = res[:lastSlashIndex]
							lastSegmentLength = len(res) - 1 - strings.LastIndex(res, `\`)
						}
						lastSlash = i
						dots = 0
						continue
					} else if res != "" {
						res = ""
						lastSegmentLength = 0
						lastSlash = i
						dots = 0
						continue
					}
				}
				if allowAboveRoot {
					if res != "" {
						res += `\..`
					} else {
						res = ".."
					}
					lastSegmentLength = 2
				}
			default:
				if res != "" {
					res += `\` + path[lastSlash+1:i]
				} else {
					res = path[lastSlash+1 : i]
				}
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
	return res
}

// nodeRealpathWin32 is fs.realpathSync for a Windows path. It reports through the error what Node throws, and reports viaNode when the walk kept a volume mount or followed a link target that filepath.EvalSymlinks resolves differently.
func nodeRealpathWin32(p string, fsys nodeRealpathFS, proc win32Process) (resolved string, viaNode bool, err error) {
	p = win32Resolve(proc, p)
	current := win32SplitRoot(p)
	base := current
	pos := len(current)
	if _, _, err := fsys.lstat(base); err != nil {
		return "", viaNode, err
	}
	for pos < len(p) {
		next := strings.IndexAny(p[pos:], `\/`)
		previous := current
		if next == -1 {
			last := p[pos:]
			current += last
			base = previous + last
			pos = len(p)
		} else {
			next += pos
			current += p[pos : next+1]
			base = previous + p[pos:next]
			pos = next + 1
		}
		kind, target, err := fsys.lstat(base)
		if err != nil {
			return "", viaNode, err
		}
		if kind != nodeEntryLink {
			if kind == nodeEntryMount {
				viaNode = true
			}
			continue
		}
		if win32TargetNeedsNodeResolution(target) {
			viaNode = true
		}
		if err := fsys.stat(base); err != nil {
			return "", viaNode, err
		}
		p = win32Resolve(proc, win32Resolve(proc, previous, target), p[pos:])
		current = win32SplitRoot(p)
		base = current
		pos = len(current)
		if _, _, err := fsys.lstat(base); err != nil {
			return "", viaNode, err
		}
	}
	return p, viaNode, nil
}

// evalCanonicalPathWith is the Windows canonicalization decision. The Node walk runs first because only it shows whether a link on the way is rooted or drive-relative. filepath.EvalSymlinks follows such a link on the process drive, and can then end at a path that still has a volume, so its result cannot vouch for the walk. When the walk kept a volume mount or followed such a link its result, or its error, stands. Every other path retains the standard library's resolution.
func evalCanonicalPathWith(path string, walk func(string) (string, bool, error), eval func(string) (string, error)) (string, error) {
	resolved, viaNode, err := walk(path)
	if viaNode {
		return resolved, err
	}
	return eval(path)
}
