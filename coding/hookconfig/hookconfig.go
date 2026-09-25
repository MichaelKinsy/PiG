// Package hookconfig parses and validates command hook files shared by Package
// validation and the runtime hook bridge.
package hookconfig

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Config is the supported Open Plugin / Claude Code command-hook subset.
type Config struct {
	Description string                 `json:"description,omitempty"`
	Hooks       map[string][]HookGroup `json:"hooks"`
}

// HookGroup applies actions when its optional matcher accepts the event value.
type HookGroup struct {
	Matcher string       `json:"matcher,omitempty"`
	Hooks   []HookAction `json:"hooks"`
}

// HookAction is one supported command action.
type HookAction struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// Parse validates data and returns its typed hook configuration. Pig rejects
// unsupported declarations instead of silently dropping them at runtime.
func Parse(path string, data []byte) (Config, error) {
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(config.Hooks) == 0 {
		return Config{}, fmt.Errorf("parse %s: hooks must define at least one event", path)
	}
	for event, groups := range config.Hooks {
		switch event {
		case "SessionStart", "PostToolUse", "Stop", "SessionEnd":
		case "PreToolUse":
			return Config{}, fmt.Errorf("parse %s: PreToolUse is not supported by Pig", path)
		default:
			return Config{}, fmt.Errorf("parse %s: unsupported hook event %q", path, event)
		}
		if len(groups) == 0 {
			return Config{}, fmt.Errorf("parse %s: %s has no hook groups", path, event)
		}
		for groupIndex, group := range groups {
			if group.Matcher != "" {
				if _, err := regexp.Compile(group.Matcher); err != nil {
					return Config{}, fmt.Errorf("parse %s: %s group %d matcher: %w", path, event, groupIndex, err)
				}
			}
			if len(group.Hooks) == 0 {
				return Config{}, fmt.Errorf("parse %s: %s group %d has no hook actions", path, event, groupIndex)
			}
			for actionIndex, action := range group.Hooks {
				if action.Type != "command" {
					return Config{}, fmt.Errorf("parse %s: %s group %d action %d has unsupported type %q", path, event, groupIndex, actionIndex, action.Type)
				}
				if strings.TrimSpace(action.Command) == "" {
					return Config{}, fmt.Errorf("parse %s: %s group %d action %d requires command", path, event, groupIndex, actionIndex)
				}
			}
		}
	}
	return config, nil
}
