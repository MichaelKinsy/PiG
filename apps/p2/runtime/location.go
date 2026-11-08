package runtime

import (
	"regexp"
	"strings"
)

// Ports apps/p2/src/runtime/location.ts: where a conversation works, shared by every process (no Node imports: the web
// and TUI bundles use it too). The schema is part of the vendored browser code; this file is the Go side.

// Location is where a conversation works: its cwd, on this machine (Env nil) or on a remote environment. The main
// conversation works at the session's location; a subagent spawned with its own cwd at its own, on its caller's
// environment.
type Location struct {
	Cwd string
	Env *string
}

// SameLocation reports whether a and b are the same place.
func SameLocation(a, b Location) bool { return sameEnv(a.Env, b.Env) && a.Cwd == b.Cwd }

func sameEnv(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// LocationText is a location as people read it: `gpu:/path` on a remote environment, the bare path on this machine.
func LocationText(location Location) string {
	if location.Env != nil && *location.Env != "" {
		return *location.Env + ":" + location.Cwd
	}
	return location.Cwd
}

// envPrefix is `envs.ENV_NAME`: the names an environment may have (`ENV_PREFIX`).
var envPrefix = regexp.MustCompile(`(?s)^([a-z0-9][a-z0-9_-]{0,31}):(.*)$`)

// LocalEnv is `local:`, which names this device, unless an environment is called "local" (`LOCAL`).
const LocalEnv = "local"

// LocationInput is a location as typed: `[env:]path`, the path absolute, home-relative or relative (resolved by the
// receiver). Env nil is this machine.
type LocationInput struct {
	Env  *string
	Path string
}

var windowsDrive = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

// IsRooted reports whether path is absolute (POSIX, Windows drive or UNC) or home-relative: the server resolves these
// itself.
func IsRooted(path string) bool {
	return path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "/") ||
		windowsDrive.MatchString(path) || strings.HasPrefix(path, `\\`)
}

// JoinPath joins base with a relative path, `.` and `..` resolved, in base's separator style.
func JoinPath(base, path string) string {
	windows := windowsDrive.MatchString(base) || strings.Contains(base, `\`)
	separator, root := "/", "/"
	if windows {
		separator = `\`
		root = separator
		if len(base) >= 2 && isASCIILetter(base[0]) && base[1] == ':' {
			root = base[:2] + separator
		}
	}
	rest := base
	if windows {
		rest = base[min(len(root), len(base)):]
	}
	var parts []string
	for _, part := range splitSlashes(rest) {
		if part != "" {
			parts = append(parts, part)
		}
	}
	for _, part := range splitSlashes(path) {
		switch part {
		case "", ".":
		case "..":
			if len(parts) > 0 {
				parts = parts[:len(parts)-1]
			}
		default:
			parts = append(parts, part)
		}
	}
	return root + strings.Join(parts, separator)
}

func isASCIILetter(b byte) bool { return b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' }

func splitSlashes(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool { return r == '/' || r == '\\' })
}

// LocationFallback is what a location typed without a prefix stays in: Env, and Cwd (empty: none) for a relative path.
type LocationFallback struct {
	Env *string
	Cwd string
}

// LocationError is a refused location text; its message is what the user reads.
type LocationError struct{ Message string }

func (err *LocationError) Error() string { return err.Message }

// ParseLocation parses `[env:]path`. A configured environment's name selects it; `local:` is this device; any other
// prefix of a name's form longer than one letter is an unknown environment (one letter is a Windows drive). Without a
// prefix the location stays in fallback.Env; a relative path joins fallback.Cwd (`/cd sub`), else the home directory.
func ParseLocation(text string, envs []string, fallback LocationFallback) (LocationInput, error) {
	input := jsTrim(text)
	match := envPrefix.FindStringSubmatch(input)
	env := fallback.Env
	path := input
	if match != nil && (contains(envs, match[1]) || match[1] == LocalEnv || len(match[1]) > 1) {
		switch {
		case contains(envs, match[1]):
			name := match[1]
			env = &name
		case match[1] == LocalEnv:
			env = nil
		default:
			return LocationInput{}, &LocationError{Message: `Unknown environment "` + match[1] + `" (/envs lists them)`}
		}
		path = jsTrim(match[2])
	}
	sameAsFallback := sameEnv(env, fallback.Env) && fallback.Cwd != ""
	switch {
	case path == "":
		if sameAsFallback {
			path = fallback.Cwd
		} else {
			path = "~"
		}
	case !IsRooted(path):
		if sameAsFallback {
			path = JoinPath(fallback.Cwd, path)
		} else {
			path = "~/" + path
		}
	}
	return LocationInput{Env: env, Path: path}, nil
}

func contains(list []string, item string) bool {
	for _, candidate := range list {
		if candidate == item {
			return true
		}
	}
	return false
}

// EnvPrefix splits `env:rest` (ENV_PREFIX): the environment-shaped prefix and the rest, ok false when text has none.
func EnvPrefix(text string) (env, rest string, ok bool) {
	match := envPrefix.FindStringSubmatch(text)
	if match == nil {
		return "", "", false
	}
	return match[1], match[2], true
}
