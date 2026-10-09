// Package configroot resolves PiG's config root, the directory PiG keeps its settings, documentation bundle and state under. It is a leaf package so
// that code the coding agent imports (the system prompt builder) and the coding agent itself name one directory through one implementation.
//
// The policy is the same in the host and in every extension SDK (Go, Python, Rust, Node): PIG_HOME, else XDG_CONFIG_HOME/pig, else ~/.pig. An empty
// variable falls through to the next choice. A leading ~ or ~/ expands to the home directory. Any other value stays literal. When the home directory
// is needed and cannot be found, resolution fails; it never yields a relative path. Pi-sharing mode (PIG_USE_PI_DIRS=1) moves only the agent and
// project directories (D2), so nothing here reads it.
package configroot

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/nodepath"
)

// ExpandTilde expands a leading ~ in a filesystem path, as upstream expandTildePath (utils/paths.ts normalizePath) does. It has no error result:
// when os.UserHomeDir fails, the home directory is the empty string. Node's os.homedir() instead falls back to the passwd database and throws when
// that fails too. A PiG-owned root uses [ExpandHome] instead, which reports the failure.
func ExpandTilde(path string) string {
	expanded, _ := expandHome(path, true)
	return expanded
}

// ExpandHome expands a leading ~ or ~/ in path and returns an error when the home directory is needed and unavailable. A path with no leading ~
// ("~user", a ~ elsewhere, an absolute or relative path) is returned unchanged and needs no home directory.
func ExpandHome(path string) (string, error) { return expandHome(path, false) }

func expandHome(path string, lenient bool) (string, error) {
	rest, ok := strings.CutPrefix(path, "~/")
	if path != "~" && !ok {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil && !lenient {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, rest), nil
}

// Resolve is the config root: PIG_HOME, else XDG_CONFIG_HOME/pig, else ~/.pig. An empty variable is ignored and a leading ~ in either is expanded. It
// returns an error, never a relative path, when the home directory is needed and cannot be found.
func Resolve() (string, error) {
	if v := os.Getenv("PIG_HOME"); v != "" {
		return ExpandHome(v)
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		base, err := ExpandHome(v)
		if err != nil {
			return "", err
		}
		return filepath.Join(base, "pig"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, ".pig"), nil
}

// UnresolvedError is the value [Dir] panics with. The pig command recovers it, prints the message and exits 1.
type UnresolvedError struct{ Err error }

func (e *UnresolvedError) Error() string {
	return "cannot resolve PiG's config directories: " + e.Err.Error() + "; set PIG_HOME"
}

func (e *UnresolvedError) Unwrap() error { return e.Err }

// Dir is [Resolve] for a caller that has no way to report a failure. It panics with an [*UnresolvedError] instead of returning a path relative to the
// working directory; the pig command reports that panic as an error and exits.
func Dir() string {
	root, err := Resolve()
	if err != nil {
		panic(&UnresolvedError{Err: err})
	}
	return root
}

// DocsDir is the absolute documentation directory under the config root: config.ts getDocsPath is resolve(join(<package dir>, "docs")), and PiG's
// bundle lives under the config root (D22). A relative PIG_HOME resolves against the working directory, as Node's resolve does.
func DocsDir() string {
	// pig additive (D22): PiG has no installed package directory; its documentation bundle lives under the config root.
	// pig additive (D92): the literal "docs" is pigdocs.SubDir, so a Binary that strips docs does not link pigdocs.
	dir := filepath.Join(Dir(), "docs")
	if resolved, err := nodepath.Resolve(dir); err == nil {
		return resolved
	}
	return dir
}
