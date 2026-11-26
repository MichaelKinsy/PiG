package ai

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestSDKRetryAndOverflowPatternsMatchAI verifies that the Go, Python, Rust, and Lisp SDKs
// mirror every entry in package ai's overflow, non-overflow, non-retryable-limit, and
// retryable-provider-error pattern tables without omissions or misplaced entries.
func TestSDKRetryAndOverflowPatternsMatchAI(t *testing.T) {
	wantOverflow := extractGoSliceStrings(t, "overflow.go", "var overflowPatterns =", "}")
	wantNonOverflow := extractGoSliceStrings(t, "overflow.go", "var nonOverflowPatterns =", "}")
	wantNonRetryable := extractGoSliceStrings(t, "assistant_retry.go", "var nonRetryableProviderLimitErrorPattern =", "})")
	wantRetryable := extractGoSliceStrings(t, "assistant_retry.go", "var retryableProviderErrorPattern =", "})")

	if len(wantOverflow) == 0 || len(wantNonOverflow) == 0 || len(wantNonRetryable) == 0 || len(wantRetryable) == 0 {
		t.Fatalf("failed to parse ai pattern lists: overflow=%d nonOverflow=%d nonRetryable=%d retryable=%d",
			len(wantOverflow), len(wantNonOverflow), len(wantNonRetryable), len(wantRetryable))
	}

	sdks := []struct {
		name         string
		overflow     []string
		nonOverflow  []string
		nonRetryable []string
		retryable    []string
	}{
		{
			name:         "go-sdk",
			overflow:     extractGoRawStrings(t, "../extensions/sdk/pi_ai_models.go", "overflowPatterns = lazyEach(", "\n\t)"),
			nonOverflow:  extractGoRawStrings(t, "../extensions/sdk/pi_ai_models.go", "nonOverflowPatterns = lazyEach(", "\n\t)"),
			nonRetryable: extractGoRawStrings(t, "../extensions/sdk/pi_ai_models.go", "nonRetryableProviderLimitErrorPattern = lazyAlternation(", "\n\t)"),
			retryable:    extractGoRawStrings(t, "../extensions/sdk/pi_ai_models.go", "retryableProviderErrorPattern = lazyAlternation(", "\n\t)"),
		},
		{
			name:         "py-sdk",
			overflow:     extractPyStrings(t, "../extensions/sdk-py/pig_sdk/pi_ai_helpers.py", "_OVERFLOW_PATTERNS = _compile_each(", "\n)"),
			nonOverflow:  extractPyStrings(t, "../extensions/sdk-py/pig_sdk/pi_ai_helpers.py", "_NON_OVERFLOW_PATTERNS = _compile_each(", "\n"),
			nonRetryable: extractPyStrings(t, "../extensions/sdk-py/pig_sdk/pi_ai_helpers.py", "_NON_RETRYABLE_PROVIDER_LIMIT_ERROR_PATTERN = _js_regexp(\"|\".join([", "]))"),
			retryable:    extractPyStrings(t, "../extensions/sdk-py/pig_sdk/pi_ai_helpers.py", "_RETRYABLE_PROVIDER_ERROR_PATTERN = _js_regexp(\"|\".join([", "]))"),
		},
		{
			name:         "rs-sdk",
			overflow:     extractRustStrings(t, "../extensions/sdk-rs/src/pi_ai_helpers.rs", "static OVERFLOW_PATTERNS:", "\n    ])"),
			nonOverflow:  extractRustStrings(t, "../extensions/sdk-rs/src/pi_ai_helpers.rs", "static NON_OVERFLOW_PATTERNS:", "\n    ])"),
			nonRetryable: extractRustStrings(t, "../extensions/sdk-rs/src/pi_ai_helpers.rs", "static NON_RETRYABLE_PROVIDER_LIMIT_ERROR_PATTERN:", "\n    ])"),
			retryable:    extractRustStrings(t, "../extensions/sdk-rs/src/pi_ai_helpers.rs", "static RETRYABLE_PROVIDER_ERROR_PATTERN:", "\n    ])"),
		},
		{
			name:         "lisp-sdk",
			overflow:     extractLispStrings(t, "../extensions/sdk-lisp/src/pi-ai.lisp", "(defvar *ai-overflow-patterns*", "))\n"),
			nonOverflow:  extractLispStrings(t, "../extensions/sdk-lisp/src/pi-ai.lisp", "(defvar *ai-non-overflow-patterns*", ")\n"),
			nonRetryable: extractLispStrings(t, "../extensions/sdk-lisp/src/pi-ai.lisp", "(defvar *ai-non-retryable-provider-limit-error*", ")))"),
			retryable:    extractLispStrings(t, "../extensions/sdk-lisp/src/pi-ai.lisp", "(defvar *ai-retryable-provider-error*", ")))"),
		},
	}

	for _, sdk := range sdks {
		t.Run(sdk.name, func(t *testing.T) {
			assertPatternSetEqual(t, "overflow", wantOverflow, sdk.overflow)
			assertPatternSetEqual(t, "nonOverflow", wantNonOverflow, sdk.nonOverflow)
			assertPatternSetEqual(t, "nonRetryable", wantNonRetryable, sdk.nonRetryable)
			assertPatternSetEqual(t, "retryable", wantRetryable, sdk.retryable)
		})
	}
}

func assertPatternSetEqual(t *testing.T, table string, want, got []string) {
	t.Helper()
	wn := normalizePatterns(want)
	gn := normalizePatterns(got)
	if !slices.Equal(wn, gn) {
		var missing, extra []string
		for _, p := range wn {
			if !slices.Contains(gn, p) {
				missing = append(missing, p)
			}
		}
		for _, p := range gn {
			if !slices.Contains(wn, p) {
				extra = append(extra, p)
			}
		}
		t.Errorf("%s mismatch:\n  missing in SDK: %q\n  extra in SDK:   %q", table, missing, extra)
	}
}

func normalizePatterns(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, normalizePattern(s))
	}
	slices.Sort(out)
	return out
}

func normalizePattern(s string) string {
	// Normalize lookaround vs RE2 boundary checks and JS space/dot helper concatenations.
	s = strings.ReplaceAll(s, "([^0-9]|$)", "(?![0-9])")
	s = strings.ReplaceAll(s, "(?:^|[^a-z0-9_])", "(?<![a-z0-9_])")
	s = strings.ReplaceAll(s, "(?:[^a-z0-9_]|$)", "(?![a-z0-9_])")
	s = strings.ReplaceAll(s, "(?:^|[^a-z0-9])", "(?<![a-z0-9])")
	s = strings.ReplaceAll(s, "(?:[^a-z0-9]|$)", "(?![a-z0-9])")
	s = strings.ReplaceAll(s, jsSpaceClass, `\s`)
	s = strings.ReplaceAll(s, jsDotClass, `.`)
	return s
}

func readSection(t *testing.T, relPath, startMarker, endMarker string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(relPath))
	if err != nil {
		t.Fatalf("read %s: %v", relPath, err)
	}
	text := string(raw)
	i := strings.Index(text, startMarker)
	if i < 0 {
		t.Fatalf("%s: missing start marker %q", relPath, startMarker)
	}
	rest := text[i+len(startMarker):]
	j := strings.Index(rest, endMarker)
	if j < 0 {
		t.Fatalf("%s: missing end marker %q", relPath, endMarker)
	}
	return rest[:j]
}

func stripLineComments(section, prefix string) string {
	var b strings.Builder
	for line := range strings.SplitSeq(section, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, prefix) {
			continue
		}
		// Strip trailing inline comments outside quotes (simple heuristic safe for pattern files).
		if idx := strings.Index(line, " "+prefix); idx >= 0 {
			line = line[:idx]
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

var (
	goExprLineRe = regexp.MustCompile(`(?m)^\s*(?:lazyregexp\.NewJSIgnoreCase\((.+)\),|(.+),)\s*$`)
	goStrLitRe   = regexp.MustCompile("`([^`]*)`|\"((?:\\\\.|[^\"\\\\])*)\"")
	pyStrLitRe   = regexp.MustCompile(`r?"((?:\\.|[^"\\])*)"|r'((?:\\.|[^'\\])*)'`)
	rsStrLitRe   = regexp.MustCompile(`r#"(.*?)"#|r"([^"]*)"|"((?:\\.|[^"\\])*)"`)
	lispStrLitRe = regexp.MustCompile(`"((?:\\.|[^"\\])*)"`)
)

func unescapeDoubleQuoted(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			switch s[i] {
			case '\\', '"':
				b.WriteByte(s[i])
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				b.WriteByte('\\')
				b.WriteByte(s[i])
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func evalGoConcat(expr string) string {
	expr = strings.ReplaceAll(expr, "jsSpaceClass", "`"+jsSpaceClass+"`")
	expr = strings.ReplaceAll(expr, "jsDotClass", "`"+jsDotClass+"`")
	matches := goStrLitRe.FindAllStringSubmatch(expr, -1)
	var b strings.Builder
	for _, m := range matches {
		if m[1] != "" {
			b.WriteString(m[1])
		} else {
			b.WriteString(unescapeDoubleQuoted(m[2]))
		}
	}
	return b.String()
}

func extractGoSliceStrings(t *testing.T, relPath, startMarker, endMarker string) []string {
	t.Helper()
	sec := stripLineComments(readSection(t, relPath, startMarker, endMarker), "//")
	var out []string
	for _, m := range goExprLineRe.FindAllStringSubmatch(sec, -1) {
		expr := m[1]
		if expr == "" {
			expr = m[2]
		}
		if s := evalGoConcat(expr); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func extractGoRawStrings(t *testing.T, relPath, startMarker, endMarker string) []string {
	t.Helper()
	sec := stripLineComments(readSection(t, relPath, startMarker, endMarker), "//")
	var out []string
	for _, m := range goStrLitRe.FindAllStringSubmatch(sec, -1) {
		if m[1] != "" {
			out = append(out, m[1])
		} else {
			out = append(out, unescapeDoubleQuoted(m[2]))
		}
	}
	return out
}

func extractPyStrings(t *testing.T, relPath, startMarker, endMarker string) []string {
	t.Helper()
	sec := stripLineComments(readSection(t, relPath, startMarker, endMarker), "#")
	var out []string
	for _, m := range pyStrLitRe.FindAllStringSubmatch(sec, -1) {
		raw := strings.HasPrefix(m[0], "r")
		val := m[1]
		if val == "" {
			val = m[2]
		}
		if !raw {
			val = unescapeDoubleQuoted(val)
		}
		out = append(out, val)
	}
	return out
}

func extractRustStrings(t *testing.T, relPath, startMarker, endMarker string) []string {
	t.Helper()
	sec := stripLineComments(readSection(t, relPath, startMarker, endMarker), "//")
	var out []string
	for _, m := range rsStrLitRe.FindAllStringSubmatch(sec, -1) {
		switch {
		case m[1] != "":
			out = append(out, m[1])
		case m[2] != "":
			out = append(out, m[2])
		default:
			out = append(out, unescapeDoubleQuoted(m[3]))
		}
	}
	return out
}

func extractLispStrings(t *testing.T, relPath, startMarker, endMarker string) []string {
	t.Helper()
	sec := stripLineComments(readSection(t, relPath, startMarker, endMarker), ";")
	var out []string
	for _, m := range lispStrLitRe.FindAllStringSubmatch(sec, -1) {
		out = append(out, unescapeDoubleQuoted(m[1]))
	}
	return out
}
