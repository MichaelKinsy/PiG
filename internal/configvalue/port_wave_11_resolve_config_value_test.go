package configvalue

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPortWave11ResolveConfigValue(t *testing.T) {
	// upstream: packages/coding-agent/test/resolve-config-value.test.ts:27
	t.Run("resolves literals, environment templates, and escapes", func(t *testing.T) {
		setupConfigValueTest(t)
		t.Setenv("TEST_CONFIG_LEFT", "left")
		t.Setenv("TEST_CONFIG_RIGHT", "right")
		assertResolvedConfigValue(t, "literal-key", "literal-key")
		assertResolvedConfigValue(t, "$TEST_CONFIG_LEFT", "left")
		assertResolvedConfigValue(t, "${TEST_CONFIG_LEFT}_$TEST_CONFIG_RIGHT", "left_right")
		assertResolvedConfigValue(t, "$$TEST_CONFIG_LEFT", "$TEST_CONFIG_LEFT")
		assertResolvedConfigValue(t, "$!literal-$TEST_CONFIG_RIGHT", "!literal-right")
	})

	// upstream: packages/coding-agent/test/resolve-config-value.test.ts:42
	t.Run("uses credential-scoped environment before process.env", func(t *testing.T) {
		setupConfigValueTest(t)
		t.Setenv("TEST_CONFIG_SCOPED", "process")
		got := Resolve("$TEST_CONFIG_SCOPED", map[string]string{"TEST_CONFIG_SCOPED": "credential"})
		if got != "credential" {
			t.Fatalf("resolveConfigValue(scoped) = %q, want credential", got)
		}
	})

	// upstream: packages/coding-agent/test/resolve-config-value.test.ts:51
	t.Run("executes shell commands and trims their output", func(t *testing.T) {
		setupConfigValueTest(t)
		assertResolvedConfigValue(t, "!echo '  spaced-key  '", "spaced-key")
		assertResolvedConfigValue(t, "!printf 'line1\\nline2'", "line1\nline2")
		assertResolvedConfigValue(t, "!echo 'hello world' | tr ' ' '-'", "hello-world")
	})

	// upstream: packages/coding-agent/test/resolve-config-value.test.ts:57-62 (all three table rows)
	for _, command := range []string{"!exit 1", "!nonexistent-command-12345", "!printf ''"} {
		t.Run("returns undefined when command resolution fails: "+command, func(t *testing.T) {
			setupConfigValueTest(t)
			// Resolve represents upstream's undefined command result as an empty string.
			assertResolvedConfigValue(t, command, "")
		})
	}

	// upstream: packages/coding-agent/test/resolve-config-value.test.ts:64
	t.Run("caches successful and failed commands until explicitly cleared", func(t *testing.T) {
		tempDir := setupConfigValueTest(t)
		counterFile := filepath.Join(tempDir, "counter")
		if err := os.WriteFile(counterFile, []byte("0"), 0o600); err != nil {
			t.Fatal(err)
		}
		escapedPath := strings.ReplaceAll(strings.ReplaceAll(counterFile, `\`, "/"), `"`, `\"`)
		success := `!sh -c 'count=$(cat "` + escapedPath + `"); echo $((count + 1)) > "` + escapedPath + `"; echo value'`

		assertResolvedConfigValue(t, success, "value")
		assertResolvedConfigValue(t, success, "value")
		assertConfigCommandCounter(t, counterFile, "1")

		ClearCache()
		assertResolvedConfigValue(t, success, "value")
		assertConfigCommandCounter(t, counterFile, "2")

		failure := `!sh -c 'count=$(cat "` + escapedPath + `"); echo $((count + 1)) > "` + escapedPath + `"; exit 1'`
		assertResolvedConfigValue(t, failure, "")
		assertResolvedConfigValue(t, failure, "")
		assertConfigCommandCounter(t, counterFile, "3")
	})

	// upstream: packages/coding-agent/test/resolve-config-value.test.ts:84
	t.Run("does not cache environment values", func(t *testing.T) {
		setupConfigValueTest(t)
		t.Setenv("TEST_CONFIG_DYNAMIC", "first")
		assertResolvedConfigValue(t, "$TEST_CONFIG_DYNAMIC", "first")
		t.Setenv("TEST_CONFIG_DYNAMIC", "second")
		assertResolvedConfigValue(t, "$TEST_CONFIG_DYNAMIC", "second")
	})

	// upstream: packages/coding-agent/test/resolve-config-value.test.ts:95
	t.Run("uncached resolution executes a command on every call", func(t *testing.T) {
		tempDir := setupConfigValueTest(t)
		counterFile := filepath.Join(tempDir, "uncached-counter")
		if err := os.WriteFile(counterFile, []byte("0"), 0o600); err != nil {
			t.Fatal(err)
		}
		escapedPath := strings.ReplaceAll(strings.ReplaceAll(counterFile, `\`, "/"), `"`, `\"`)
		command := `!sh -c 'count=$(cat "` + escapedPath + `"); echo $((count + 1)) > "` + escapedPath + `"; echo value'`
		if got := ResolveUncached(command, nil); got != "value" {
			t.Fatalf("first resolveConfigValueUncached = %q, want value", got)
		}
		if got := ResolveUncached(command, nil); got != "value" {
			t.Fatalf("second resolveConfigValueUncached = %q, want value", got)
		}
		assertConfigCommandCounter(t, counterFile, "2")
	})
}
