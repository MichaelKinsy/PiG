// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package tools

import (
	"context"
	"os"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/agent"
)

// sessionVariables are the variables upstream's bash tool controls.
var sessionVariables = []string{"PI_SESSION_ID", "PI_SESSION_FILE", "PI_PROVIDER", "PI_MODEL", "PI_REASONING_LEVEL"}

// sessionGuideline is upstream's bash prompt guideline for the variables.
const sessionGuideline = "You can inspect PI_* environment variables for current model and session details."

// GetShellEnv mirrors upstream getShellEnv: the process environment with
// binDir (upstream getBinDir, <agentDir>/bin, where managed rg and fd live)
// prepended to PATH unless PATH already lists it. The PATH key is matched
// case-insensitively, as on Windows. An empty binDir leaves PATH unchanged.
func GetShellEnv(binDir string) []string {
	env := os.Environ()
	if binDir == "" {
		return env
	}
	pathIndex := -1
	for i, kv := range env {
		if name, _, _ := strings.Cut(kv, "="); strings.EqualFold(name, "path") {
			pathIndex = i
			break
		}
	}
	if pathIndex < 0 {
		return append(env, "PATH="+binDir)
	}
	name, current, _ := strings.Cut(env[pathIndex], "=")
	if slices.Contains(strings.Split(current, string(os.PathListSeparator)), binDir) {
		return env
	}
	updated := binDir
	if current != "" {
		updated += string(os.PathListSeparator) + current
	}
	env[pathIndex] = name + "=" + updated
	return env
}

// sessionEnvironment mirrors upstream resolveSpawnContext: it always removes
// inherited session variables, so a nested pig never sees its parent's values,
// then adds the current session's values when expose is set.
func sessionEnvironment(ctx context.Context, expose bool, binDir string) []string {
	base := GetShellEnv(binDir)
	env := make([]string, 0, len(base)+len(sessionVariables))
	for _, kv := range base {
		if name, _, _ := strings.Cut(kv, "="); !slices.Contains(sessionVariables, name) {
			env = append(env, kv)
		}
	}
	session, ok := agent.ToolEnvironmentFrom(ctx)
	if !expose || !ok {
		return env
	}
	for _, v := range [][2]string{
		{"PI_SESSION_ID", session.SessionID},
		{"PI_SESSION_FILE", session.SessionFile},
		{"PI_PROVIDER", session.Provider},
		{"PI_MODEL", session.Model},
		{"PI_REASONING_LEVEL", session.ThinkingLevel},
	} {
		if v[1] != "" {
			env = append(env, v[0]+"="+v[1])
		}
	}
	return env
}
