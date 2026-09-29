// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2018 Made With MOXY Lda <hello@moxy.studio>
// SPDX-FileCopyrightText: Copyright (c) Kevin Mårtensson <kevinmartensson@gmail.com>
// SPDX-FileCopyrightText: Copyright (c) Sindre Sorhus <sindresorhus@gmail.com>
// SPDX-FileCopyrightText: Copyright (c) Isaac Z. Schlueter and Contributors
// SPDX-License-Identifier: MIT AND ISC

// Ports packages/coding-agent/src/utils/child-process.ts (Windows spawnProcess routing through cross-spawn).
package crossspawn

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

var (
	// executableFile matches files CreateProcess starts directly
	// (cross-spawn lib/parse.js isExecutableRegExp).
	executableFile = lazyregexp.New(`(?i)\.(?:com|exe)$`)
	// cmdShim matches npm's .cmd shims, whose arguments cmd.exe parses twice
	// (cross-spawn lib/parse.js isCmdShimRegExp).
	cmdShim = lazyregexp.New(`(?i)node_modules[\\/].bin[\\/][^\\/]+\.cmd$`)
	// metaChars are the characters cmd.exe interprets (cross-spawn lib/util/escape.js metaCharsRegExp).
	metaChars = lazyregexp.New("([()\\][%!^\"`<>&|;, *?])")
	// The lazy lookahead in cross-spawn 7.0.6 captures at most one backslash, not the whole run. Preserve its command-line bytes at both parse depths.
	backslashesQuote    = lazyregexp.New(`(\\?)"`)
	trailingBackslashes = lazyregexp.New(`(\\?)$`)
	shebangLine         = lazyregexp.New(`^#![^\r\n\x{2028}\x{2029}]*`)
)

type windowsCommandPlan struct {
	name    string
	path    string
	args    []string
	cmdLine string
}

func planWindowsCommand(dir, name string, args []string) windowsCommandPlan {
	file := resolveCommand(dir, name)
	if shebang := readShebang(file); shebang != "" {
		args = append([]string{file}, args...)
		name = shebang
		file = resolveCommand(dir, name)
	}
	if executableFile.MatchString(file) {
		return windowsCommandPlan{name: name, path: file, args: args}
	}
	double := cmdShim.MatchString(file)
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, escapeCommand(filepath.Clean(name)))
	for _, arg := range args {
		parts = append(parts, escapeArgument(arg, double))
	}
	shell := os.Getenv("COMSPEC")
	if shell == "" {
		shell = "cmd.exe"
	}
	return windowsCommandPlan{name: shell, cmdLine: shell + ` /d /s /c "` + strings.Join(parts, " ") + `"`}
}

// resolveCommand is cross-spawn lib/util/resolveCommand.js: a PATHEXT lookup,
// then a second lookup without PATHEXT, which finds extensionless shebang
// scripts. Both run from the child cwd.
func resolveCommand(dir, name string) string {
	if dir == "" {
		dir, _ = os.Getwd()
	} else {
		dir, _ = filepath.Abs(dir)
	}
	pathExt := os.Getenv("PATHEXT")
	if pathExt == "" {
		pathExt = ".EXE;.CMD;.BAT;.COM"
	}
	if file := whichSync(dir, name, pathExt); file != "" {
		return file
	}
	return whichSync(dir, name, ";")
}

// whichSync is node-which 2.0.2 whichSync with isexe 2.0.0's Windows check. A
// match keeps the PATHEXT entry's spelling: name resolves to name.CMD when
// PATHEXT lists .CMD, whatever the case of the file on disk.
func whichSync(dir, name, pathExt string) string {
	exts := strings.Split(pathExt, ";")
	if strings.Contains(name, ".") && exts[0] != "" {
		exts = slices.Insert(exts, 0, "")
	}
	pathEnv := []string{""}
	if !strings.ContainsAny(name, `/\`) {
		// Windows searches the cwd before PATH. node-which splits PATH at
		// every separator and then strips one pair of enclosing quotes.
		pathEnv = append([]string{dir}, strings.Split(os.Getenv("PATH"), string(os.PathListSeparator))...)
	}
	for _, part := range pathEnv {
		if len(part) >= 2 && strings.HasPrefix(part, `"`) && strings.HasSuffix(part, `"`) {
			part = part[1 : len(part)-1]
		}
		candidate := resolveIn(dir, filepath.Join(part, name))
		for _, ext := range exts {
			if file := candidate + ext; isExe(file, pathExt) {
				return file
			}
		}
	}
	return ""
}

// resolveIn resolves path as Node's path.resolve(dir, path) does. A path with
// a volume and no root ("D:tool.exe") is relative to that drive's current
// directory, which is dir on dir's own drive. A rooted path without a volume
// stays on dir's volume.
func resolveIn(dir, path string) string {
	switch {
	case filepath.IsAbs(path):
		return path
	case filepath.VolumeName(path) != "":
		cwd, _ := os.Getwd()
		return win32Resolve(func(device string) string { return os.Getenv("=" + device) }, cwd, dir, path)
	case path != "" && os.IsPathSeparator(path[0]):
		return filepath.Join(filepath.VolumeName(dir), path)
	default:
		return filepath.Join(dir, path)
	}
}

// isExe is isexe 2.0.0 windows.js: a regular file whose name ends with a
// PATHEXT entry, compared case-insensitively. An empty entry accepts any file.
func isExe(file, pathExt string) bool {
	info, err := os.Stat(file)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	lower := strings.ToLower(file)
	for ext := range strings.SplitSeq(pathExt, ";") {
		if ext == "" || strings.HasSuffix(lower, strings.ToLower(ext)) {
			return true
		}
	}
	return false
}

// readShebang mirrors cross-spawn's 150-byte read and shebang-command's literal
// space splitting. An interpreter argument stays part of the command name.
func readShebang(file string) string {
	f, err := os.Open(file)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	buffer := make([]byte, 150)
	_, _ = f.Read(buffer)
	line := shebangLine.FindString(string(buffer))
	if line == "" {
		return ""
	}
	line = strings.TrimPrefix(strings.TrimPrefix(line, "#!"), " ")
	parts := strings.Split(line, " ")
	pathParts := strings.Split(parts[0], "/")
	binary := pathParts[len(pathParts)-1]
	argument := ""
	if len(parts) > 1 {
		argument = parts[1]
	}
	if binary == "env" {
		return argument
	}
	if argument != "" {
		return binary + " " + argument
	}
	return binary
}

// escapeCommand is cross-spawn lib/util/escape.js escapeCommand.
func escapeCommand(command string) string {
	return metaChars.ReplaceAllString(command, "^$1")
}

// escapeArgument is cross-spawn lib/util/escape.js escapeArgument: quote the
// argument for the C runtime, then escape cmd.exe metacharacters (twice when
// cmd.exe parses the line a second time).
func escapeArgument(arg string, doubleEscape bool) string {
	arg = backslashesQuote.ReplaceAllString(arg, `$1$1\"`)
	arg = trailingBackslashes.ReplaceAllString(arg, `$1$1`)
	arg = `"` + arg + `"`
	arg = metaChars.ReplaceAllString(arg, "^$1")
	if doubleEscape {
		arg = metaChars.ReplaceAllString(arg, "^$1")
	}
	return arg
}
