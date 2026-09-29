// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright Joyent, Inc. and other Node contributors
// SPDX-License-Identifier: MIT

package crossspawn

import (
	"path"
	"strings"
)

// win32Resolve is Node's path.win32.resolve(args...) (lib/path.js) for a
// caller that has read the process state Node reads. cwd is process.cwd().
// driveCwd returns process.env["=X:"], the current directory Windows keeps for
// drive X:, or "" when there is none.
func win32Resolve(driveCwd func(device string) string, cwd string, args ...string) string {
	resolvedDevice, resolvedTail, resolvedAbsolute := "", "", false
	for i := len(args) - 1; i >= -1; i-- {
		var p string
		switch {
		case i >= 0:
			if p = args[i]; p == "" {
				continue
			}
		case resolvedDevice == "":
			p = cwd
		default:
			// A drive was named without a root: use that drive's current
			// directory, else the process directory, and default to the
			// drive root when that directory is on another drive.
			if p = driveCwd(resolvedDevice); p == "" {
				p = cwd
			}
			if p == "" || (!strings.EqualFold(utf16Prefix(p, 2), resolvedDevice) && len(p) > 2 && p[2] == '\\') {
				p = resolvedDevice + `\`
			}
		}
		device, rootEnd, absolute := win32Root(p)
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
			resolvedTail = p[rootEnd:] + `\` + resolvedTail
			resolvedAbsolute = absolute
			if absolute && resolvedDevice != "" {
				break
			}
		}
	}
	resolvedTail = win32NormalizeTail(resolvedTail, !resolvedAbsolute)
	if resolvedAbsolute {
		return resolvedDevice + `\` + resolvedTail
	}
	if result := resolvedDevice + resolvedTail; result != "" {
		return result
	}
	return "."
}

// utf16Prefix is s.slice(0, n) for the ASCII and BMP text a drive check sees.
func utf16Prefix(s string, n int) string {
	runes := []rune(s)
	return string(runes[:min(n, len(runes))])
}

func isWin32Separator(c byte) bool { return c == '/' || c == '\\' }

// win32Root is the root matching of path.win32.resolve: the device (a drive
// or UNC share), the index where the tail starts, and whether the path is
// absolute.
func win32Root(p string) (device string, rootEnd int, absolute bool) {
	n := len(p)
	if n == 0 {
		return "", 0, false
	}
	c := p[0]
	switch {
	case n == 1:
		if isWin32Separator(c) {
			return "", 1, true
		}
	case isWin32Separator(c):
		absolute = true
		if !isWin32Separator(p[1]) {
			return "", 1, true
		}
		j, last := 2, 2
		for j < n && !isWin32Separator(p[j]) {
			j++
		}
		if j < n && j != last {
			first := p[last:j]
			last = j
			for j < n && isWin32Separator(p[j]) {
				j++
			}
			if j < n && j != last {
				last = j
				for j < n && !isWin32Separator(p[j]) {
					j++
				}
				if j == n || j != last {
					if first != "." && first != "?" {
						device, rootEnd = `\\`+first+`\`+p[last:j], j
					} else {
						device, rootEnd = `\\`+first, 4
					}
				}
			}
		}
	case (c|0x20 >= 'a' && c|0x20 <= 'z') && p[1] == ':':
		device, rootEnd = p[:2], 2
		if n > 2 && isWin32Separator(p[2]) {
			absolute, rootEnd = true, 3
		}
	}
	return device, rootEnd, absolute
}

// win32NormalizeTail is normalizeString(tail, allowAboveRoot, '\\'): it drops
// empty and "." segments and resolves "..", keeping leading ".." only when the
// path is not absolute.
func win32NormalizeTail(tail string, allowAboveRoot bool) string {
	s := strings.TrimLeft(strings.ReplaceAll(tail, `\`, "/"), "/")
	var cleaned string
	if allowAboveRoot {
		if cleaned = path.Clean(s); cleaned == "." {
			cleaned = ""
		}
	} else {
		cleaned = strings.TrimPrefix(path.Clean("/"+s), "/")
	}
	return strings.ReplaceAll(cleaned, "/", `\`)
}
