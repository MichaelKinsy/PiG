// migrations.go provides one-time startup migrations for pig configuration.
//
// upstream: coding-agent/src/migrations.ts

package codingagent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	migrationGuideURL = "https://github.com/earendil-works/pi/blob/main/packages/coding-agent/CHANGELOG.md#extensions-migration"
	extensionsDocURL  = "https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/extensions.md"
)

// RunMigrations runs all one-time startup migrations. A failed auth.json
// write is an error, as upstream's uncaught write throws.
// Mirrors upstream runMigrations (migrations.ts:259-268).
func RunMigrations(cwd, agentDir string) (migratedAuthProviders []string, deprecationWarnings []string, err error) {
	migratedAuthProviders, err = migrateAuthToAuthJSON(agentDir)
	if err != nil {
		return nil, nil, err
	}
	migrateSessionsFromAgentRoot(agentDir)
	migrateToolsToBin(agentDir)
	// Keybindings migration is handled inline by KeybindingsManager.Reload.
	deprecationWarnings = migrateExtensionSystem(cwd, agentDir)
	return migratedAuthProviders, deprecationWarnings, nil
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

	migrated := make(map[string]any)
	var providers []string

	// Migrate oauth.json.
	if data, err := os.ReadFile(oauthPath); err == nil {
		var oauth map[string]any
		if json.Unmarshal(data, &oauth) == nil {
			for provider, cred := range oauth {
				credMap, ok := cred.(map[string]any)
				if !ok {
					credMap = map[string]any{}
				}
				credMap["type"] = "oauth"
				migrated[provider] = credMap
				providers = append(providers, provider)
			}
			_ = os.Rename(oauthPath, oauthPath+".migrated") // upstream: coding-agent/src/migrations.ts:migrateAuthToAuthJson
		}
	}

	// Migrate settings.json apiKeys.
	if data, err := os.ReadFile(settingsPath); err == nil {
		var settings map[string]any
		if json.Unmarshal(data, &settings) == nil {
			if apiKeys, ok := settings["apiKeys"].(map[string]any); ok {
				for provider, key := range apiKeys {
					if _, already := migrated[provider]; !already {
						if keyStr, ok := key.(string); ok {
							migrated[provider] = map[string]any{"type": "api_key", "key": keyStr}
							providers = append(providers, provider)
						}
					}
				}
				delete(settings, "apiKeys")
				updated, _ := json.MarshalIndent(settings, "", "  ")
				_ = os.WriteFile(settingsPath, updated, 0644) // upstream: coding-agent/src/migrations.ts:migrateAuthToAuthJson
			}
		}
	}

	if len(migrated) > 0 {
		if err := os.MkdirAll(filepath.Dir(authPath), 0o755); err != nil {
			return nil, fmt.Errorf("migrate credentials to auth.json: %w", err)
		}
		out, err := json.MarshalIndent(migrated, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("migrate credentials to auth.json: %w", err)
		}
		if err := os.WriteFile(authPath, out, 0o600); err != nil {
			return nil, fmt.Errorf("migrate credentials to auth.json: %w", err)
		}
	}

	return providers, nil
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
	// Upstream joins cwd with CONFIG_DIR_NAME; PiG's is ".pig", never Pi's ".pi".
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
