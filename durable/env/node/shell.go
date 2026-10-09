package node

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/nodespawn"
)

const (
	maxTimeoutMs      = 2_147_483_647
	maxTimeoutSeconds = maxTimeoutMs / 1000.0
)

// resolveTimeout converts a timeout in seconds; nil means no timeout.
func resolveTimeout(timeout *float64) (time.Duration, error) {
	if timeout == nil {
		return 0, nil
	}
	seconds := *timeout
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 {
		return 0, durableenv.NewExecutionError(durableenv.ExecutionErrorTimeout, "Invalid timeout: must be a finite number of seconds", nil)
	}
	timeoutMs := seconds * 1000
	if timeoutMs > maxTimeoutMs {
		return 0, durableenv.NewExecutionError(durableenv.ExecutionErrorTimeout, "Invalid timeout: maximum is "+formatNumber(maxTimeoutSeconds)+" seconds", nil)
	}
	// Upstream arms setTimeout(timeoutMs), and Node runs a delay below 1 ms
	// after 1 ms (lib/internal/timers.js Timeout), so a valid timeout never
	// rounds to no timer at all.
	return max(time.Duration(timeoutMs*float64(time.Millisecond)), time.Millisecond), nil
}

// formatNumber renders a float like JavaScript's Number#toString.
func formatNumber(value float64) string { return jsnumber.String(value) }

type shellConfig struct {
	shell            string
	args             []string
	commandFromStdin bool
}

var legacyWslBashPath = regexp.MustCompile(`^[a-z]:\\windows\\(?:system32|sysnative)\\bash\.exe$`)

func isLegacyWslBashPath(path string) bool {
	return legacyWslBashPath.MatchString(strings.ToLower(strings.ReplaceAll(path, "/", `\`)))
}

// getBashShellConfig passes the command as an argument, except for legacy WSL
// bash, which reads it from stdin.
func getBashShellConfig(shell string) shellConfig {
	if isLegacyWslBashPath(shell) {
		return shellConfig{shell: shell, args: []string{"-s"}, commandFromStdin: true}
	}
	return shellConfig{shell: shell, args: []string{"-c"}}
}

func getShellConfig(ctx context.Context, customShellPath string) (shellConfig, error) {
	if customShellPath != "" {
		if pathExists(customShellPath) {
			return getBashShellConfig(customShellPath), nil
		}
		return shellConfig{}, durableenv.NewExecutionError(durableenv.ExecutionErrorShellUnavailable, "Custom shell path not found: "+customShellPath, nil)
	}
	return platformShellConfig(ctx)
}

// findBashOnPath asks `which bash` (or `where bash.exe`) for the first match.
func findBashOnPath(ctx context.Context) string {
	name, args := "which", []string{"bash"}
	if isWindows {
		name, args = "where", []string{"bash.exe"}
	}
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(runCtx, name, args...)
	// Upstream's runCommand spawns with stdio ["ignore", "pipe", "ignore"]
	// and windowsHide: true.
	nodespawn.HideWindow(command, nodespawn.Ignore, nodespawn.Pipe, nodespawn.Ignore)
	nodespawn.SetProgram(command)
	output, err := command.Output()
	if err != nil || len(output) == 0 {
		return ""
	}
	firstMatch, _, _ := strings.Cut(strings.TrimSpace(strings.ReplaceAll(string(output), "\r\n", "\n")), "\n")
	if firstMatch != "" && pathExists(firstMatch) {
		return firstMatch
	}
	return ""
}

// getShellEnv is upstream's getShellEnv object as nodespawn.SetEnvProperties
// properties: {...process.env, ...baseEnv, ...extraEnv}, or {...extraEnv} when
// inheritance is off. A later property of a name replaces the value in place.
// Go maps keep no insertion order, so each map adds its new names in sorted
// order; only the child's environment order outside Windows can show it.
func getShellEnv(baseEnv, extraEnv map[string]string, inheritEnv *bool) []nodespawn.EnvProperty {
	var environment []nodespawn.EnvProperty
	if inheritEnv == nil || *inheritEnv {
		environment = appendEnvObject(nodespawn.EnvProperties(nodespawn.ProcessEnv()), baseEnv)
	}
	return appendEnvObject(environment, extraEnv)
}

// appendEnvObject appends the properties of values in name order.
func appendEnvObject(environment []nodespawn.EnvProperty, values map[string]string) []nodespawn.EnvProperty {
	for _, name := range slices.Sorted(maps.Keys(values)) {
		environment = append(environment, nodespawn.EnvProperty{Name: name, Value: values[name]})
	}
	return environment
}

// spawnErrorMessage mirrors Node's "spawn <file> <CODE>" message, or the
// message of the error Node's spawn reports for a program it does not start.
func spawnErrorMessage(shell string, err error) string {
	if spawnErr, ok := errors.AsType[*nodespawn.Error](err); ok {
		return spawnErr.Error()
	}
	code := errnoCode(err)
	if code == "" && errors.Is(err, exec.ErrNotFound) {
		// os/exec reports a program it finds no executable file for as
		// exec.ErrNotFound; libuv reports ENOENT for it.
		code = "ENOENT"
	}
	if code != "" {
		return fmt.Sprintf("spawn %s %s", shell, code)
	}
	return err.Error()
}
