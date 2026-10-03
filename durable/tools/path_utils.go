package tools

import (
	"context"
	"regexp"
	"strings"

	"github.com/MichaelKinsy/PiG/durable/env"

	"golang.org/x/text/unicode/norm"
)

// Ports packages/durable/src/tools/path-utils.ts.

var unicodeSpaces = regexp.MustCompile("[\u00A0\u2000-\u200A\u202F\u205F\u3000]")

var meridiemAfterSpace = regexp.MustCompile(`(?i) (AM|PM)\.`)

const narrowNoBreakSpace = "\u202F"

func normalizeToolPath(path string) string {
	normalized := unicodeSpaces.ReplaceAllString(path, " ")
	return strings.TrimPrefix(normalized, "@")
}

func resolveToolPath(ctx context.Context, executionEnv env.ExecutionEnv, path string) (string, error) {
	return executionEnv.AbsolutePath(ctx, normalizeToolPath(path))
}

// resolveReadToolPath resolves path, then looks for the file under the variants
// macOS gives names it creates: a narrow no-break space before AM/PM, decomposed
// characters, and a curly apostrophe. The first variant that exists wins; none
// existing leaves the resolved path.
func resolveReadToolPath(ctx context.Context, executionEnv env.ExecutionEnv, path string) (string, error) {
	resolved, err := resolveToolPath(ctx, executionEnv, path)
	if err != nil {
		return "", err
	}
	decomposed := norm.NFD.String(resolved)
	variants := []string{
		resolved,
		meridiemAfterSpace.ReplaceAllString(resolved, narrowNoBreakSpace+"${1}."),
		decomposed,
		strings.ReplaceAll(resolved, "'", "\u2019"),
		strings.ReplaceAll(decomposed, "'", "\u2019"),
	}
	tried := make(map[string]bool, len(variants))
	for _, variant := range variants {
		if tried[variant] {
			continue
		}
		tried[variant] = true
		exists, err := executionEnv.Exists(ctx, variant)
		if err != nil {
			return "", err
		}
		if exists {
			return variant, nil
		}
	}
	return resolved, nil
}
