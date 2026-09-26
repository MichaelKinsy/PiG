package codingagent

import (
	"os"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/term"
)

// chalkModifiersEnabled reports whether upstream's chalk, which draws
// theme.bold, italic, underline, inverse and strikethrough, emits styles in
// this process: its supports-color level for stdout is above zero.
func chalkModifiersEnabled() bool {
	return chalkColorLevel(os.Args[1:], os.Getenv, os.LookupEnv, term.IsTerminal(int(os.Stdout.Fd())), runtime.GOOS, windowsBuild()) > 0
}

var (
	chalkTeamCityRe = regexp.MustCompile(`^(?:9\.0*[1-9]\d*\.|\d{2,}\.)`)
	chalk256Re      = regexp.MustCompile(`(?i)-256(?:color)?$`)
	chalkBasicRe    = regexp.MustCompile(`(?i)^screen|^xterm|^vt100|^vt220|^rxvt|color|ansi|cygwin|linux`)
	chalkNumericRe  = regexp.MustCompile(`^\d+$`)
)

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
		version, _ := strconv.Atoi(strings.SplitN(getenv("TERM_PROGRAM_VERSION"), ".", 2)[0])
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
