package main

import (
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

// chalkColorLevel ports the stdout/stderr color-level decision of chalk 6.0.0
// (the version pinned by upstream packages/coding-agent/package.json), i.e.
// chalk/source/vendor/supports-color/index.js _supportsColor with
// sniffFlags=true. Upstream main.ts:422 prints chalk.dim(...), which emits SGR
// codes only when this level is non-zero. NO_COLOR is intentionally not
// consulted: the pinned supports-color does not implement it.
func chalkColorLevel(env map[string]string, argv []string, isTTY bool) int {
	hasFlag := func(flag string) bool {
		prefix := "--"
		if strings.HasPrefix(flag, "-") {
			prefix = ""
		} else if len(flag) == 1 {
			prefix = "-"
		}
		pos := slices.Index(argv, prefix+flag)
		term := slices.Index(argv, "--")
		return pos != -1 && (term == -1 || pos < term)
	}
	has := func(key string) bool { _, ok := env[key]; return ok }

	flagForce := -1
	if hasFlag("no-color") || hasFlag("no-colors") || hasFlag("color=false") || hasFlag("color=never") {
		flagForce = 0
	} else if hasFlag("color") || hasFlag("colors") || hasFlag("color=true") || hasFlag("color=always") {
		flagForce = 1
	}
	numericForce := false
	if v, ok := env["FORCE_COLOR"]; ok {
		numericForce = regexp.MustCompile(`^\d+$`).MatchString(v)
		switch {
		case v == "false":
			flagForce = 0
		case v == "true" || v == "":
			flagForce = 1
		case numericForce:
			n, err := strconv.Atoi(v)
			if err != nil || n > 3 {
				n = 3
			}
			flagForce = n
		}
	}
	if flagForce == 0 {
		return 0
	}
	if hasFlag("color=16m") || hasFlag("color=full") || hasFlag("color=truecolor") {
		return 3
	}
	if hasFlag("color=256") {
		return 2
	}
	if flagForce != -1 && numericForce {
		return flagForce
	}
	if has("TF_BUILD") && has("AGENT_NAME") {
		return 1
	}
	if !isTTY && flagForce == -1 {
		return 0
	}
	minLevel := max(flagForce, 0)
	termName := env["TERM"]
	if termName == "dumb" {
		return minLevel
	}
	if runtime.GOOS == "windows" {
		return 2
	}
	if has("CI") {
		if slices.ContainsFunc([]string{"GITHUB_ACTIONS", "GITEA_ACTIONS", "CIRCLECI"}, has) {
			return 3
		}
		if slices.ContainsFunc([]string{"TRAVIS", "APPVEYOR", "GITLAB_CI", "BUILDKITE", "DRONE"}, has) {
			return 1
		}
		if env["CI_NAME"] == "codeship" {
			return 1
		}
		return minLevel
	}
	if v, ok := env["TEAMCITY_VERSION"]; ok {
		if regexp.MustCompile(`^(?:9\.0*[1-9]\d*\.|\d{2,}\.)`).MatchString(v) {
			return 1
		}
		return 0
	}
	if env["COLORTERM"] == "truecolor" {
		return 3
	}
	switch termName {
	case "xterm-kitty", "xterm-ghostty", "wezterm":
		return 3
	}
	if program, ok := env["TERM_PROGRAM"]; ok {
		major, _, _ := strings.Cut(env["TERM_PROGRAM_VERSION"], ".")
		version, err := strconv.Atoi(major)
		switch program {
		case "iTerm.app":
			if err == nil && version >= 3 {
				return 3
			}
			return 2
		case "Apple_Terminal":
			return 2
		}
	}
	if regexp.MustCompile(`(?i)-256(?:color)?$`).MatchString(termName) {
		return 2
	}
	if regexp.MustCompile(`(?i)^screen|^xterm|^vt100|^vt220|^rxvt|color|ansi|cygwin|linux`).MatchString(termName) {
		return 1
	}
	if has("COLORTERM") {
		return 1
	}
	return minLevel
}

// environMap splits os.Environ-style entries into a map (last value wins).
func environMap(environ []string) map[string]string {
	env := make(map[string]string, len(environ))
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	return env
}
