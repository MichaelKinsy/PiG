package codingagent

import (
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"

	"golang.org/x/term"
)

// chalkModifiersEnabled reports whether upstream's chalk, which draws
// theme.bold, italic, underline, inverse and strikethrough, emits styles in
// this process: its supports-color level for stdout is above zero.
func chalkModifiersEnabled() bool {
	return chalkColorLevel(os.Args[1:], os.Getenv, os.LookupEnv, stdoutIsTerminal(), runtime.GOOS, windowsBuild()) > 0
}

// stdoutTTYProbe is whether file, the os.Stdout of the probe, is a terminal.
type stdoutTTYProbe struct {
	file *os.File
	tty  bool
}

var stdoutTTY atomic.Pointer[stdoutTTYProbe]

// stdoutIsTerminal probes os.Stdout once per file. The probe is an fcntl (os.File.Fd puts a descriptor Go made non-blocking back in blocking mode on every call) and an ioctl, and the extension state push reads the theme palette, and so this, for every handler dispatch. Upstream's chalk probes once, at import: supports-color's `stdout: createSupportsColor({isTTY: tty.isatty(1)})` (chalk/source/vendor/supports-color/index.js:186). The environment stays live, so only the descriptor is cached, and a caller that swaps os.Stdout is probed again.
func stdoutIsTerminal() bool {
	file := os.Stdout
	if probe := stdoutTTY.Load(); probe != nil && probe.file == file {
		return probe.tty
	}
	tty := term.IsTerminal(int(file.Fd()))
	stdoutTTY.Store(&stdoutTTYProbe{file: file, tty: tty})
	return tty
}

// ChalkApply is chalk 6.0.0 applyStyle for one style with a color level above zero: an empty string stays empty, an existing close code inside the
// text is followed by the open code again, and each line break is wrapped in close and open codes (a preceding carriage return stays before the break).
func ChalkApply(open, closeSeq, text string) string {
	if text == "" {
		return ""
	}
	if strings.Contains(text, "\x1b") {
		text = strings.ReplaceAll(text, closeSeq, closeSeq+open)
	}
	if strings.Contains(text, "\n") {
		var b strings.Builder
		for line, rest, found := strings.Cut(text, "\n"); ; line, rest, found = strings.Cut(rest, "\n") {
			if !found {
				b.WriteString(line)
				break
			}
			if before, ok := strings.CutSuffix(line, "\r"); ok {
				b.WriteString(before + closeSeq + "\r\n" + open)
			} else {
				b.WriteString(line + closeSeq + "\n" + open)
			}
		}
		text = b.String()
	}
	return open + text + closeSeq
}

var (
	chalkTeamCityRe = lazyregexp.New(`^(?:9\.0*[1-9]\d*\.|\d{2,}\.)`)
	chalk256Re      = lazyregexp.NewJSIgnoreCase(`-256(?:color)?$`)
	chalkBasicRe    = lazyregexp.NewJSIgnoreCase(`^screen|^xterm|^vt100|^vt220|^rxvt|color|ansi|cygwin|linux`)
	chalkNumericRe  = lazyregexp.New(`^\d+$`)
)

// ChalkColorLevel is chalkColorLevel for this platform: chalk's supports-color level for a stream given the arguments and the
// environment that getenv and lookup read.
func ChalkColorLevel(args []string, getenv func(string) string, lookup func(string) (string, bool), streamIsTTY bool) int {
	return chalkColorLevel(args, getenv, lookup, streamIsTTY, runtime.GOOS, windowsBuild())
}

// chalkColorLevel is chalk's vendored supports-color _supportsColor for a
// stream (upstream: chalk/source/vendor/supports-color/index.js).
func chalkColorLevel(args []string, getenv func(string) string, lookup func(string) (string, bool), streamIsTTY bool, goos string, winBuild [3]int) int {
	hasFlag := func(flag string) bool {
		prefix := "--"
		if strings.HasPrefix(flag, "-") {
			prefix = ""
		} else if len(flag) == 1 {
			prefix = "-"
		}
		position := slices.Index(args, prefix+flag)
		terminator := slices.Index(args, "--")
		return position != -1 && (terminator == -1 || position < terminator)
	}
	has := func(key string) bool { _, ok := lookup(key); return ok }

	flagForceColor := -1
	switch {
	case hasFlag("no-color") || hasFlag("no-colors") || hasFlag("color=false") || hasFlag("color=never"):
		flagForceColor = 0
	case hasFlag("color") || hasFlag("colors") || hasFlag("color=true") || hasFlag("color=always"):
		flagForceColor = 1
	}
	forceEnv, forceSet := lookup("FORCE_COLOR")
	numericForce := forceSet && chalkNumericRe.MatchString(forceEnv)
	if forceSet {
		switch {
		case forceEnv == "false":
			flagForceColor = 0
		case forceEnv == "true" || forceEnv == "":
			flagForceColor = 1
		case numericForce:
			level, _ := strconv.Atoi(forceEnv)
			flagForceColor = min(level, 3)
		}
	}
	if flagForceColor == 0 {
		return 0
	}
	if hasFlag("color=16m") || hasFlag("color=full") || hasFlag("color=truecolor") {
		return 3
	}
	if hasFlag("color=256") {
		return 2
	}
	if flagForceColor != -1 && numericForce {
		return flagForceColor
	}
	if has("TF_BUILD") && has("AGENT_NAME") {
		return 1
	}
	if !streamIsTTY && flagForceColor == -1 {
		return 0
	}
	minimum := max(flagForceColor, 0)
	termName := getenv("TERM")
	if termName == "dumb" {
		return minimum
	}
	if goos == "windows" {
		if winBuild[0] >= 10 && winBuild[2] >= 10586 {
			if winBuild[2] >= 14931 {
				return 3
			}
			return 2
		}
		return 1
	}
	if has("CI") {
		if slices.ContainsFunc([]string{"GITHUB_ACTIONS", "GITEA_ACTIONS", "CIRCLECI"}, has) {
			return 3
		}
		if slices.ContainsFunc([]string{"TRAVIS", "APPVEYOR", "GITLAB_CI", "BUILDKITE", "DRONE"}, has) {
			return 1
		}
		if getenv("CI_NAME") == "codeship" {
			return 1
		}
		return minimum
	}
	if has("TEAMCITY_VERSION") {
		if chalkTeamCityRe.MatchString(getenv("TEAMCITY_VERSION")) {
			return 1
		}
		return 0
	}
	if getenv("COLORTERM") == "truecolor" || termName == "xterm-kitty" || termName == "xterm-ghostty" || termName == "wezterm" {
		return 3
	}
	if has("TERM_PROGRAM") {
		version := jsnumber.ParseInt(strings.SplitN(getenv("TERM_PROGRAM_VERSION"), ".", 2)[0])
		switch getenv("TERM_PROGRAM") {
		case "iTerm.app":
			if version >= 3 {
				return 3
			}
			return 2
		case "Apple_Terminal":
			return 2
		}
	}
	if chalk256Re.MatchString(termName) {
		return 2
	}
	if chalkBasicRe.MatchString(termName) {
		return 1
	}
	if has("COLORTERM") {
		return 1
	}
	return minimum
}

// chalkStyled is `chalk.<style>(text)` on upstream's default chalk instance: ChalkApply at a nonzero stdout level, the text unchanged otherwise.
func chalkStyled(open, closeSeq, text string) string {
	if !chalkModifiersEnabled() {
		return text
	}
	return ChalkApply(open, closeSeq, text)
}
