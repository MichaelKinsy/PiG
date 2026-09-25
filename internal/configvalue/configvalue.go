// Package configvalue mirrors upstream pi's
// packages/coding-agent/src/core/resolve-config-value.ts (v0.78.1).
//
// A configuration string value is one of:
//
//  1. A "!cmd" prefix → strip the "!", run the rest as a shell command,
//     return the trimmed stdout. Cached for the process lifetime.
//  2. A template that may reference environment variables with "$VAR" or
//     "${VAR}". "$$" escapes a literal "$" and "$!" escapes a literal "!".
//     Any other text is literal. A template resolves to undefined (empty)
//     if any referenced env var is unset or empty.
//  3. A plain string with no "$" → a literal that resolves to itself.
//
// IMPORTANT: unlike the pre-v0.78.1 contract, a bare identifier is NO
// LONGER implicitly matched against the environment. {"apiKey":"OPENAI_API_KEY"}
// now resolves to the literal string "OPENAI_API_KEY". Environment
// references require an explicit "$" sigil: {"apiKey":"$OPENAI_API_KEY"}.
//
// Lives in its own package (rather than internal/codingagent/ where the
// plan named it) because ai/auth.go is a consumer and
// internal/codingagent already imports ai. A neutral package
// sidesteps the cycle while preserving the public API.
package configvalue

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

// commandExecutor is the indirection point for tests. Production uses
// runShellCommand; test files swap in a fake.
type commandExecutor func(ctx context.Context, payload string) (string, bool)

var (
	cacheMu  sync.Mutex
	cache                    = map[string]cacheEntry{}
	executor commandExecutor = runShellCommand
)

type cacheEntry struct {
	value string
	ok    bool // false means the !cmd produced no usable output
}

// commandTimeout matches upstream's 10s execSync timeout.
const commandTimeout = 10 * time.Second

// Env-var name patterns, mirroring resolve-config-value.ts.
var (
	envVarNameRe       = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	envVarNamePrefixRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*`)
)

// templatePart is one segment of a parsed config-value template: either a
// literal run of text or a named environment-variable reference.
type templatePart struct {
	isEnv bool
	value string // literal text, or env-var name when isEnv
}

func appendLiteral(parts []templatePart, value string) []templatePart {
	if value == "" {
		return parts
	}
	if n := len(parts); n > 0 && !parts[n-1].isEnv {
		parts[n-1].value += value
		return parts
	}
	return append(parts, templatePart{value: value})
}

// parseConfigValueTemplate parses a non-command config value into literal
// and env-reference parts. Direct port of upstream parseConfigValueTemplate.
func parseConfigValueTemplate(config string) []templatePart {
	var parts []templatePart
	index := 0

	for index < len(config) {
		rel := strings.IndexByte(config[index:], '$')
		if rel < 0 {
			parts = appendLiteral(parts, config[index:])
			break
		}
		dollarIndex := index + rel
		parts = appendLiteral(parts, config[index:dollarIndex])

		var nextChar byte
		if dollarIndex+1 < len(config) {
			nextChar = config[dollarIndex+1]
		}

		if nextChar == '$' || nextChar == '!' {
			parts = appendLiteral(parts, string(nextChar))
			index = dollarIndex + 2
			continue
		}

		if nextChar == '{' {
			rel := strings.IndexByte(config[dollarIndex+2:], '}')
			if rel < 0 {
				parts = appendLiteral(parts, "$")
				index = dollarIndex + 1
				continue
			}
			endIndex := dollarIndex + 2 + rel
			name := config[dollarIndex+2 : endIndex]
			if envVarNameRe.MatchString(name) {
				parts = append(parts, templatePart{isEnv: true, value: name})
			} else {
				parts = appendLiteral(parts, config[dollarIndex:endIndex+1])
			}
			index = endIndex + 1
			continue
		}

		match := envVarNamePrefixRe.FindString(config[dollarIndex+1:])
		if match != "" {
			parts = append(parts, templatePart{isEnv: true, value: match})
			index = dollarIndex + 1 + len(match)
			continue
		}

		parts = appendLiteral(parts, "$")
		index = dollarIndex + 1
	}

	return parts
}

func isCommand(config string) bool {
	return strings.HasPrefix(config, "!")
}

func resolveEnvConfigValue(name string, env map[string]string) (string, bool) {
	if env != nil {
		if v := env[name]; v != "" {
			return v, true
		}
	}
	v := os.Getenv(name)
	if v == "" {
		return "", false
	}
	return v, true
}

func templateEnvVarNames(parts []templatePart) []string {
	var names []string
	for _, p := range parts {
		if !p.isEnv {
			continue
		}
		if contains(names, p.value) {
			continue
		}
		names = append(names, p.value)
	}
	return names
}

func contains(names []string, name string) bool {
	return slices.Contains(names, name)
}

// resolveTemplate concatenates the parts, substituting env references.
// Returns ok=false if any referenced env var is unset/empty (matching
// upstream's "return undefined" on a missing variable). Provider-scoped env
// overrides in env take precedence over the process environment.
func resolveTemplate(parts []templatePart, env map[string]string) (string, bool) {
	var sb strings.Builder
	for _, p := range parts {
		if !p.isEnv {
			sb.WriteString(p.value)
			continue
		}
		v, ok := resolveEnvConfigValue(p.value, env)
		if !ok {
			return "", false
		}
		sb.WriteString(v)
	}
	return sb.String(), true
}

// GetConfigValueEnvVarName returns the single env-var name a config value
// references, or "" if it is a command, a literal, or a multi-part template.
func GetConfigValueEnvVarName(config string) string {
	if isCommand(config) {
		return ""
	}
	parts := parseConfigValueTemplate(config)
	if len(parts) == 1 && parts[0].isEnv {
		return parts[0].value
	}
	return ""
}

// GetConfigValueEnvVarNames returns every distinct env-var name a config
// value references (empty for commands and literals).
func GetConfigValueEnvVarNames(config string) []string {
	if isCommand(config) {
		return nil
	}
	return templateEnvVarNames(parseConfigValueTemplate(config))
}

// GetMissingConfigValueEnvVarNames returns the referenced env-var names that
// are currently unset/empty. Provider-scoped overrides in env are consulted
// before the process environment.
func GetMissingConfigValueEnvVarNames(config string, env map[string]string) []string {
	var missing []string
	for _, name := range GetConfigValueEnvVarNames(config) {
		if _, ok := resolveEnvConfigValue(name, env); !ok {
			missing = append(missing, name)
		}
	}
	return missing
}

// IsCommandConfigValue reports whether a config value is a "!cmd" command.
func IsCommandConfigValue(config string) bool {
	return isCommand(config)
}

// IsConfigValueConfigured reports whether all referenced env vars are set.
// Provider-scoped overrides in env are consulted before the process
// environment.
func IsConfigValueConfigured(config string, env map[string]string) bool {
	return len(GetMissingConfigValueEnvVarNames(config, env)) == 0
}

// Resolve returns the runtime value for a config string, or "" when a
// command fails or a referenced env var is unset. Mirrors upstream
// resolveConfigValue (string | undefined), collapsing undefined to "".
// Provider-scoped overrides in env take precedence over the process
// environment.
func Resolve(s string, env map[string]string) string {
	if isCommand(s) {
		v, _ := executeCached(s)
		return v
	}
	v, _ := resolveTemplate(parseConfigValueTemplate(s), env)
	return v
}

// ResolveUncached is Resolve but skips the !cmd cache.
func ResolveUncached(s string, env map[string]string) string {
	if isCommand(s) {
		v, _ := executeUncached(s)
		return v
	}
	v, _ := resolveTemplate(parseConfigValueTemplate(s), env)
	return v
}

// ResolveOrError mirrors upstream resolveConfigValueOrThrow: it errors when
// a command fails or a referenced env var is unset, with the same messages.
// A pure literal never errors (it resolves to itself). Provider-scoped
// overrides in env take precedence over the process environment.
func ResolveOrError(s, description string, env map[string]string) (string, error) {
	if isCommand(s) {
		v, ok := executeUncached(s)
		if !ok {
			return "", fmt.Errorf("failed to resolve %s from shell command: %s", description, strings.TrimPrefix(s, "!"))
		}
		return v, nil
	}
	parts := parseConfigValueTemplate(s)
	if v, ok := resolveTemplate(parts, env); ok {
		return v, nil
	}
	missing := GetMissingConfigValueEnvVarNames(s, env)
	switch len(missing) {
	case 1:
		return "", fmt.Errorf("failed to resolve %s from environment variable: %s", description, missing[0])
	case 0:
		return "", fmt.Errorf("failed to resolve %s", description)
	default:
		return "", fmt.Errorf("failed to resolve %s from environment variables: %s", description, strings.Join(missing, ", "))
	}
}

// ResolveHeaders maps each header value through Resolve and drops entries
// that resolve to empty strings. Returns nil if every entry drops out.
// Provider-scoped overrides in env take precedence over the process
// environment.
func ResolveHeaders(in map[string]string, env map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		if r := Resolve(v, env); r != "" {
			out[k] = r
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ClearCache wipes the !cmd cache. Test-only.
func ClearCache() {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cache = map[string]cacheEntry{}
}

// SetExecutorForTest swaps the shell-out implementation. Test-only.
// The returned function restores the previous executor.
func SetExecutorForTest(fn func(ctx context.Context, payload string) (string, bool)) func() {
	cacheMu.Lock()
	prev := executor
	executor = commandExecutor(fn)
	cacheMu.Unlock()
	return func() {
		cacheMu.Lock()
		executor = prev
		cacheMu.Unlock()
	}
}

func executeCached(input string) (string, bool) {
	cacheMu.Lock()
	if entry, hit := cache[input]; hit {
		cacheMu.Unlock()
		return entry.value, entry.ok
	}
	exec := executor
	cacheMu.Unlock()

	v, ok := runExecutor(exec, input)

	cacheMu.Lock()
	cache[input] = cacheEntry{value: v, ok: ok}
	cacheMu.Unlock()
	return v, ok
}

func executeUncached(input string) (string, bool) {
	cacheMu.Lock()
	exec := executor
	cacheMu.Unlock()
	return runExecutor(exec, input)
}

func runExecutor(exec commandExecutor, input string) (string, bool) {
	if exec == nil {
		return "", false
	}
	payload := strings.TrimPrefix(input, "!")
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	return exec(ctx, payload)
}
