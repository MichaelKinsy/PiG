// migrations.go provides one-time startup migrations for pig configuration.
//
// Ports packages/coding-agent/src/migrations.ts.

package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	jsjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
	"github.com/MichaelKinsy/PiG/internal/text"
	"github.com/MichaelKinsy/PiG/tui"
)

const (
	migrationGuideURL = "https://github.com/earendil-works/pi/blob/main/packages/coding-agent/CHANGELOG.md#extensions-migration"
	extensionsDocURL  = "https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/extensions.md"
)

// RunMigrations runs the startup migrations, including the persisted keybinding-name rewrite. Auth writes return errors; malformed or unwritable keybindings are left alone, as in upstream runMigrations.
func RunMigrations(cwd, agentDir string) (migratedAuthProviders []string, deprecationWarnings []string, err error) {
	migratedAuthProviders, err = migrateAuthToAuthJSON(agentDir)
	if err != nil {
		return nil, nil, err
	}
	migrateSessionsFromAgentRoot(agentDir)
	migrateToolsToBin(agentDir)
	migrateKeybindingsConfigFile(agentDir)
	deprecationWarnings = migrateExtensionSystem(cwd, agentDir)
	return migratedAuthProviders, deprecationWarnings, nil
}

// migrateKeybindingsConfigFile mirrors migrations.ts:157-173: rewrite only migrated objects, with canonical names taking precedence over aliases and best-effort read/parse/write handling. Property names retain lone UTF-16 units rather than merging them with U+FFFD.
func migrateKeybindingsConfigFile(agentDir string) {
	path := KeybindingsFile(agentDir)
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var raw map[string]json.RawMessage
	if jsjson.Unmarshal(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")), &raw) != nil || raw == nil {
		return
	}
	values := make(map[string]any, len(raw))
	for key, value := range raw {
		values[key] = value
	}
	config, migrated := migrateKeybindingsConfig(values)
	if !migrated {
		return
	}
	out, err := marshalMigratedKeybindings(config)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, out, 0o666) // upstream: packages/coding-agent/src/migrations.ts:migrateKeybindingsConfigFile
}

// marshalMigratedKeybindings retains the TUI_KEYBINDINGS and app definition order before UTF-16-sorted extras, including lone surrogates, as core/keybindings.ts:342-358 requires. Raw values retain nested object order through JSON.parse/stringify canonicalization.
func marshalMigratedKeybindings(config map[string]any) ([]byte, error) {
	order := []string{
		tui.KBEditorCursorUp, tui.KBEditorCursorDown, tui.KBEditorHistoryPrevious, tui.KBEditorHistoryNext,
		tui.KBEditorCursorLeft, tui.KBEditorCursorRight, tui.KBEditorCursorWordLeft, tui.KBEditorCursorWordRight,
		tui.KBEditorCursorLineStart, tui.KBEditorCursorLineEnd, tui.KBEditorJumpForward, tui.KBEditorJumpBackward,
		tui.KBEditorPageUp, tui.KBEditorPageDown, tui.KBEditorDeleteCharBack, tui.KBEditorDeleteCharForward,
		tui.KBEditorDeleteWordBack, tui.KBEditorDeleteWordForward, tui.KBEditorDeleteToLineStart, tui.KBEditorDeleteToLineEnd,
		tui.KBEditorYank, tui.KBEditorYankPop, tui.KBEditorUndo,
		tui.KBInputNewLine, tui.KBInputSubmit, tui.KBInputTab, tui.KBInputCopy,
		tui.KBSelectUp, tui.KBSelectDown, tui.KBSelectPageUp, tui.KBSelectPageDown, tui.KBSelectConfirm, tui.KBSelectCancel,
		tui.KBAltScreenPageUp, tui.KBAltScreenPageDown, tui.KBAltScreenHalfPageUp, tui.KBAltScreenHalfPageDown,
		tui.KBAltScreenLineUp, tui.KBAltScreenLineDown, tui.KBAltScreenPreviousPrompt, tui.KBAltScreenNextPrompt,
		tui.KBAltScreenSearch, tui.KBAltScreenSearchNext, tui.KBAltScreenSearchPrevious, tui.KBAltScreenSearchClose,
		tui.KBAltScreenTop, tui.KBAltScreenBottom,
	}
	order = append(order, appKeybindingOrder...)
	var extras []string
	for key := range config {
		if !slices.Contains(order, key) {
			extras = append(extras, key)
		}
	}
	slices.SortFunc(extras, func(a, b string) int {
		return slices.Compare(jsstring.ToUTF16(a), jsstring.ToUTF16(b))
	})
	var compact bytes.Buffer
	compact.WriteByte('{')
	first := true
	for _, key := range append(order, extras...) {
		value, ok := config[key]
		if !ok {
			continue
		}
		if !first {
			compact.WriteByte(',')
		}
		first = false
		name, _ := jsjson.Marshal(key)
		compact.Write(name)
		compact.WriteByte(':')
		compact.Write(value.(json.RawMessage))
	}
	compact.WriteByte('}')
	canonical, err := jsonstringify.Canonicalize(compact.Bytes())
	if err != nil {
		return nil, err
	}
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, canonical, "", "  "); err != nil {
		return nil, err
	}
	formatted.WriteByte('\n')
	return formatted.Bytes(), nil
}

// migrateAuthToAuthJSON migrates legacy oauth.json and settings.json apiKeys
// into auth.json. Returns the list of provider names migrated.
// Mirrors upstream migrateAuthToAuthJson (migrations.ts:20-72).
func migrateAuthToAuthJSON(agentDir string) ([]string, error) {
	authPath := filepath.Join(agentDir, "auth.json")
	oauthPath := filepath.Join(agentDir, "oauth.json")
	settingsPath := filepath.Join(agentDir, "settings.json")

	// Skip if auth.json already exists.
	if _, err := os.Stat(authPath); err == nil {
		return nil, nil
	}

	// migrated is a JS object: JSON.stringify writes it in Object.entries order, array-index names first (stringifyIndented applies that order).
	migrated := orderedjson.New()
	var providers []string

	// Migrate oauth.json.
	if data, err := os.ReadFile(oauthPath); err == nil {
		if oauth, err := orderedJSONObject(text.StripBomBytes(data)); err == nil {
			for _, provider := range oauth.Keys() {
				raw, _ := oauth.Get(provider)
				cred := orderedjson.New()
				cred.Set("type", json.RawMessage(`"oauth"`))
				if fields, err := orderedjson.Parse(raw); err == nil {
					for _, key := range fields.Keys() {
						value, _ := fields.Get(key)
						cred.Set(key, value)
					}
				}
				migrated.Set(provider, rawObject(cred))
				providers = append(providers, provider)
			}
			_ = os.Rename(oauthPath, oauthPath+".migrated") // upstream: coding-agent/src/migrations.ts:migrateAuthToAuthJson
		}
	}

	// Migrate settings.json apiKeys.
	if data, err := os.ReadFile(settingsPath); err == nil {
		if settings, err := orderedJSONObject(text.StripBomBytes(data)); err == nil {
			if apiKeysRaw, ok := settings.Get("apiKeys"); ok && orderedjson.IsObject(apiKeysRaw) {
				apiKeys, _ := orderedJSONObject(apiKeysRaw)
				for _, provider := range apiKeys.Keys() {
					raw, _ := apiKeys.Get(provider)
					if migrated.Has(provider) {
						continue
					}
					if bytes.HasPrefix(bytes.TrimSpace(raw), []byte(`"`)) {
						entry := orderedjson.New()
						entry.Set("type", json.RawMessage(`"api_key"`))
						entry.Set("key", raw)
						migrated.Set(provider, rawObject(entry))
						providers = append(providers, provider)
					}
				}
				settings.Delete("apiKeys")
				if updated, err := stringifyIndented(rawObject(settings)); err == nil {
					_ = os.WriteFile(settingsPath, updated, 0644) // upstream: coding-agent/src/migrations.ts:migrateAuthToAuthJson
				}
			}
		}
	}

	if migrated.Len() > 0 {
		if err := os.MkdirAll(filepath.Dir(authPath), 0o755); err != nil {
			return nil, fmt.Errorf("migrate credentials to auth.json: %w", err)
		}
		out, err := stringifyIndented(rawObject(migrated))
		if err != nil {
			return nil, fmt.Errorf("migrate credentials to auth.json: %w", err)
		}
		if err := os.WriteFile(authPath, out, 0o600); err != nil {
			return nil, fmt.Errorf("migrate credentials to auth.json: %w", err)
		}
	}

	return providers, nil
}

// orderedJSONObject parses data as JSON.parse then Object.entries see it: a repeated key keeps its first position and last value, and array-index names come first in ascending order.
func orderedJSONObject(data []byte) (*orderedjson.Object, error) {
	canonical, err := jsonstringify.Canonicalize(data)
	if err != nil {
		return nil, err
	}
	return orderedjson.Parse(canonical)
}

// stringifyIndented is JSON.stringify(value, null, 2) of a JSON document: entries order and two-space indentation, and no HTML escaping.
func stringifyIndented(raw []byte) ([]byte, error) {
	canonical, err := jsonstringify.Canonicalize(raw)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, canonical, "", "  "); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// rawObject is the object's JSON text. It does not go through encoding/json, which would escape <, > and & where JSON.stringify does not.
func rawObject(o *orderedjson.Object) json.RawMessage {
	encoded, _ := o.MarshalJSON()
	return encoded
}

// migrateSessionsFromAgentRoot moves .jsonl files from the agent root
// to proper session directories based on their cwd header.
// See: https://github.com/earendil-works/pi/issues/320
// Mirrors upstream migrateSessionsFromAgentRoot (migrations.ts:81-128).
func migrateSessionsFromAgentRoot(agentDir string) {
	entries, err := os.ReadDir(agentDir)
	if err != nil {
		return
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		filePath := filepath.Join(agentDir, e.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}

		// Read first line for session header.
		firstLine, _, _ := strings.Cut(string(data), "\n")
		if firstLine == "" {
			continue
		}

		var header struct {
			Type string `json:"type"`
			CWD  string `json:"cwd"`
		}
		if json.Unmarshal([]byte(firstLine), &header) != nil || header.Type != "session" || header.CWD == "" {
			continue
		}

		// Compute correct session directory.
		// Same encoding as session-manager.ts (one leading separator stripped).
		correctDir := filepath.Join(agentDir, "sessions", encodeCwdForSessionDir(header.CWD))
		_ = os.MkdirAll(correctDir, 0755) // upstream: coding-agent/src/migrations.ts:migrateSessionsFromAgentRoot

		newPath := filepath.Join(correctDir, e.Name())
		if _, err := os.Stat(newPath); err == nil {
			continue // Target exists.
		}
		_ = os.Rename(filePath, newPath) // upstream: coding-agent/src/migrations.ts:migrateSessionsFromAgentRoot
	}
}

// migrateToolsToBin moves fd/rg binaries from tools/ to bin/.
// Mirrors upstream migrateToolsToBin (migrations.ts:184-216).
func migrateToolsToBin(agentDir string) {
	toolsDir := filepath.Join(agentDir, "tools")
	binDir := filepath.Join(agentDir, "bin")

	if _, err := os.Stat(toolsDir); err != nil {
		return
	}

	for _, bin := range []string{"fd", "rg", "fd.exe", "rg.exe"} {
		oldPath := filepath.Join(toolsDir, bin)
		newPath := filepath.Join(binDir, bin)

		if _, err := os.Stat(oldPath); err != nil {
			continue
		}
		_ = os.MkdirAll(binDir, 0755) // upstream: coding-agent/src/migrations.ts:migrateToolsToBin
		if _, err := os.Stat(newPath); err != nil {
			_ = os.Rename(oldPath, newPath) // upstream: coding-agent/src/migrations.ts:migrateToolsToBin
		} else {
			_ = os.Remove(oldPath)
		}
	}
}

// migrateExtensionSystem renames commands/ → prompts/ and checks for
// deprecated hooks/tools directories.
// Mirrors upstream migrateExtensionSystem (migrations.ts:241-257).
func migrateExtensionSystem(cwd, agentDir string) []string {
	projectDir := ProjectConfigDir(cwd)

	migrateCommandsToPrompts(agentDir, "Global")
	migrateCommandsToPrompts(projectDir, "Project")

	var warnings []string
	warnings = append(warnings, checkDeprecatedExtensionDirs(agentDir, "Global")...)
	warnings = append(warnings, checkDeprecatedExtensionDirs(projectDir, "Project")...)
	return warnings
}

func migrateCommandsToPrompts(baseDir, label string) {
	commandsDir := filepath.Join(baseDir, "commands")
	promptsDir := filepath.Join(baseDir, "prompts")

	if _, err := os.Stat(commandsDir); err != nil {
		return
	}
	if _, err := os.Stat(promptsDir); err == nil {
		return // prompts/ already exists
	}
	if err := os.Rename(commandsDir, promptsDir); err != nil {
		fmt.Printf("Warning: Could not migrate %s commands/ to prompts/: %v\n", label, err)
		return
	}
	fmt.Printf("Migrated %s commands/ → prompts/\n", label)
}

func checkDeprecatedExtensionDirs(baseDir, label string) []string {
	var warnings []string

	hooksDir := filepath.Join(baseDir, "hooks")
	if _, err := os.Stat(hooksDir); err == nil {
		warnings = append(warnings, fmt.Sprintf("%s hooks/ directory found. Hooks have been renamed to extensions.", label))
	}

	toolsDir := filepath.Join(baseDir, "tools")
	if entries, err := os.ReadDir(toolsDir); err == nil {
		hasCustom := false
		for _, e := range entries {
			name := strings.ToLower(e.Name())
			if name != "fd" && name != "rg" && name != "fd.exe" && name != "rg.exe" && !strings.HasPrefix(e.Name(), ".") {
				hasCustom = true
				break
			}
		}
		if hasCustom {
			warnings = append(warnings, fmt.Sprintf("%s tools/ directory contains custom tools. Custom tools have been merged into extensions.", label))
		}
	}

	return warnings
}
