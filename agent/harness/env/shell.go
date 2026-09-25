package env

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/agent/harness"
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
		return 0, &harness.ExecutionError{Code: harness.ExecutionErrorTimeout, Message: "Invalid timeout: must be a finite number of seconds"}
	}
	timeoutMs := seconds * 1000
	if timeoutMs > maxTimeoutMs {
		return 0, &harness.ExecutionError{Code: harness.ExecutionErrorTimeout, Message: "Invalid timeout: maximum is " + formatNumber(maxTimeoutSeconds) + " seconds"}
	}
	return time.Duration(timeoutMs * float64(time.Millisecond)), nil
}

// formatNumber renders a float like JavaScript's Number#toString for the
// ordinary magnitudes used in messages.
func formatNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

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
		return shellConfig{}, &harness.ExecutionError{Code: harness.ExecutionErrorShellUnavailable, Message: "Custom shell path not found: " + customShellPath}
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
	command.SysProcAttr = hiddenProcessAttributes()
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

// getShellEnv layers the configured and per-call environment over the process
// environment, or uses only the per-call values when inheritance is off.
func getShellEnv(baseEnv, extraEnv map[string]string, inheritEnv *bool) []string {
	merged := map[string]string{}
	if inheritEnv == nil || *inheritEnv {
		for _, entry := range os.Environ() {
			if key, value, ok := strings.Cut(entry, "="); ok {
				merged[key] = value
			}
		}
		maps.Copy(merged, baseEnv)
	}
	maps.Copy(merged, extraEnv)
	environment := make([]string, 0, len(merged))
	for key, value := range merged {
		environment = append(environment, key+"="+value)
	}
	return environment
}

// spawnErrorMessage mirrors Node's "spawn <file> <CODE>" message.
func spawnErrorMessage(shell string, err error) string {
	code := errnoCode(err)
	if code == "" && errors.Is(err, exec.ErrNotFound) {
		// On Windows exec rejects a file without an executable extension
		// before spawning; libuv reports ENOENT for it.
		code = "ENOENT"
	}
	if code != "" {
		return fmt.Sprintf("spawn %s %s", shell, code)
	}
	return err.Error()
}
