package models

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports apps/p2/plugins/models/config-value.ts (adapted upstream from Pi, MIT): configuration values that may be shell
// commands (`!command`), environment variables (`$NAME`, `${NAME}`), or literals (`$$` and `$!` escape). Used by the
// credentials and by models.json.

var (
	envVarNameRe       = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	envVarNamePrefixRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*`)
)

// templatePart is a literal or an environment variable of a template.
type templatePart struct {
	env   bool
	value string // the literal, or the variable's name
}

func appendLiteral(parts []templatePart, value string) []templatePart {
	if value == "" {
		return parts
	}
	if last := len(parts) - 1; last >= 0 && !parts[last].env {
		parts[last].value += value
		return parts
	}
	return append(parts, templatePart{value: value})
}

func parseConfigValueTemplate(config string) []templatePart {
	var parts []templatePart
	index := 0
	for index < len(config) {
		dollar := strings.IndexByte(config[index:], '$')
		if dollar < 0 {
			parts = appendLiteral(parts, config[index:])
			break
		}
		dollar += index
		parts = appendLiteral(parts, config[index:dollar])
		var next byte
		if dollar+1 < len(config) {
			next = config[dollar+1]
		}
		switch {
		case next == '$' || next == '!':
			parts = appendLiteral(parts, string(next))
			index = dollar + 2
		case next == '{':
			end := strings.IndexByte(config[dollar+2:], '}')
			if end < 0 {
				parts = appendLiteral(parts, "$")
				index = dollar + 1
				continue
			}
			end += dollar + 2
			name := config[dollar+2 : end]
			if envVarNameRe.MatchString(name) {
				parts = append(parts, templatePart{env: true, value: name})
			} else {
				parts = appendLiteral(parts, config[dollar:end+1])
			}
			index = end + 1
		default:
			if match := envVarNamePrefixRe.FindString(config[dollar+1:]); match != "" {
				parts = append(parts, templatePart{env: true, value: match})
				index = dollar + 1 + len(match)
				continue
			}
			parts = appendLiteral(parts, "$")
			index = dollar + 1
		}
	}
	return parts
}

// resolveEnvConfigValue is a variable's value: env's, else the process's; ok is false for an empty or unset one.
func resolveEnvConfigValue(name string, env map[string]string) (string, bool) {
	if value := env[name]; value != "" {
		return value, true
	}
	if value := os.Getenv(name); value != "" {
		return value, true
	}
	return "", false
}

func templateEnvVarNames(parts []templatePart) []string {
	var names []string
	for _, part := range parts {
		if !part.env {
			continue
		}
		duplicate := false
		for _, name := range names {
			duplicate = duplicate || name == part.value
		}
		if !duplicate {
			names = append(names, part.value)
		}
	}
	return names
}

func resolveTemplate(parts []templatePart, env map[string]string) (string, bool) {
	var resolved strings.Builder
	for _, part := range parts {
		if !part.env {
			resolved.WriteString(part.value)
			continue
		}
		value, ok := resolveEnvConfigValue(part.value, env)
		if !ok {
			return "", false
		}
		resolved.WriteString(value)
	}
	return resolved.String(), true
}

// GetConfigValueEnvVarNames are the environment variables a value references, in order, once each (none for a command).
func GetConfigValueEnvVarNames(config string) []string {
	if IsCommandConfigValue(config) {
		return nil
	}
	return templateEnvVarNames(parseConfigValueTemplate(config))
}

func missingConfigValueEnvVarNames(config string, env map[string]string) []string {
	var missing []string
	for _, name := range GetConfigValueEnvVarNames(config) {
		if _, ok := resolveEnvConfigValue(name, env); !ok {
			missing = append(missing, name)
		}
	}
	return missing
}

// IsCommandConfigValue is whether the value is a shell command (`!command`).
func IsCommandConfigValue(config string) bool { return strings.HasPrefix(config, "!") }

// ResolveConfigValueUncached resolves a value (a command runs every time, under $SHELL with a 10 s limit); ok is
// false when a command fails or prints nothing, or a referenced variable is unset. The resolved value is added to the
// process logs' secrets: actual values are protected, not only the references in the configuration files.
func ResolveConfigValueUncached(config string, env map[string]string) (string, bool) {
	var resolved string
	var ok bool
	if IsCommandConfigValue(config) {
		resolved, ok = runConfigCommand(config[1:])
	} else {
		resolved, ok = resolveTemplate(parseConfigValueTemplate(config), env)
	}
	if ok {
		p2runtime.ProcessLogs.Redactor.AddSecret(resolved)
	}
	return resolved, ok
}

// ResolveConfigValueOrError is ResolveConfigValueUncached, or the error naming description and what was missing.
func ResolveConfigValueOrError(config, description string, env map[string]string) (string, error) {
	if resolved, ok := ResolveConfigValueUncached(config, env); ok {
		return resolved, nil
	}
	if IsCommandConfigValue(config) {
		return "", fmt.Errorf("Failed to resolve %s from shell command", description)
	}
	missing := missingConfigValueEnvVarNames(config, env)
	switch len(missing) {
	case 0:
		return "", errors.New("Failed to resolve " + description)
	case 1:
		return "", fmt.Errorf("Failed to resolve %s from environment variable: %s", description, missing[0])
	default:
		return "", fmt.Errorf("Failed to resolve %s from environment variables: %s", description, strings.Join(missing, ", "))
	}
}

// Header is a header of models.json: a name and its (unresolved) value.
type Header struct{ Name, Value string }

// ResolveHeadersOrError resolves each header value as an API key is, in order; nil when there are none. Names that differ
// only in case are one header, the last in order winning with its spelling (as Pi's mergeHeaders folds them, which keeps
// the last of a case-variant pair whichever way the map is read later).
func ResolveHeadersOrError(headers []Header, description string, env map[string]string) (map[string]string, error) {
	if len(headers) == 0 {
		return nil, nil
	}
	resolved := make(map[string]string, len(headers))
	for _, header := range headers {
		value, err := ResolveConfigValueOrError(header.Value, fmt.Sprintf(`%s header "%s"`, description, header.Name), env)
		if err != nil {
			return nil, err
		}
		lower := jsstring.ToLower(header.Name)
		for name := range resolved {
			if jsstring.ToLower(name) == lower {
				delete(resolved, name)
			}
		}
		resolved[header.Name] = value
	}
	return resolved, nil
}
