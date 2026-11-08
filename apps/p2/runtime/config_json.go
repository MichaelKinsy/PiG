package runtime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Ports apps/p2/src/runtime/config-json.ts: JSON configuration helpers (adapted from earendil-works/pi, MIT; see
// third-party/pi.LICENSE).

// StripBOM removes a leading byte order mark (`text.replace(/^\uFEFF/, "")`).
func StripBOM(text string) string { return strings.TrimPrefix(text, "\uFEFF") }

// NormalizePath expands a leading `~` to the home directory and makes the path absolute and clean.
func NormalizePath(path string) string {
	home, _ := os.UserHomeDir()
	switch {
	case path == "~":
		path = home
	case strings.HasPrefix(path, "~/"):
		path = home + "/" + path[2:]
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return absolute
}

var (
	jsonCommentOrString = regexp.MustCompile(`"(?:\\.|[^"\\])*"|//[^\n]*`)
	jsonCommaOrString   = regexp.MustCompile(`"(?:\\.|[^"\\])*"|,([\s\v\p{Zs}\x{FEFF}\x{2028}\x{2029}]*[}\]])`)
)

// StripJSONComments strips `//` line comments and trailing commas from JSON, leaving string literals untouched.
func StripJSONComments(input string) string {
	withoutComments := jsonCommentOrString.ReplaceAllStringFunc(input, func(match string) string {
		if match[0] == '"' {
			return match
		}
		return ""
	})
	return jsonCommaOrString.ReplaceAllStringFunc(withoutComments, func(match string) string {
		if match[0] == '"' {
			return match
		}
		return match[1:]
	})
}

// OptionalText reads a text file, or returns nil when it does not exist; any other failure is the error `Could not
// read resource file`.
func OptionalText(path string) (*string, error) {
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("Could not read resource file")
	}
	text := string(content)
	return &text, nil
}
