package evals

import (
	"context"
	"os/exec"
	"time"
)

// AgentDirEnvironment is the environment variable that selects the agent directory (D2).
func AgentDirEnvironment() string { return agentDirEnvironment() }

// RPCSessionStats starts pig in RPC mode with args and env in dir and returns the data of get_session_stats.
func RPCSessionStats(ctx context.Context, pigPath, dir string, args, env []string) ([]byte, error) {
	command := exec.CommandContext(ctx, pigPath, append([]string{"--mode", "rpc"}, args...)...) //nolint:gosec // G204: the program and arguments come from the user, the project config, or PiG's own toolchain lookup; the CLI runs as the user with no sandbox and launches them as Pi's child_process.spawn does
	command.Dir, command.Env = dir, env
	process, err := startAgentProcess(command)
	if err != nil {
		return nil, err
	}
	defer func() { _ = process.close() }()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return process.request(ctx, map[string]any{"type": "get_session_stats"})
}
