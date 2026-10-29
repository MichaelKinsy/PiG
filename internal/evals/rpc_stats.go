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
	//nolint:gosec // G204 (class c): program is pigPath passed by the caller (AgentRun.PigPath, same configured resolution as the harness); args are fixed --mode rpc plus the caller's session and model flags.
	command := exec.CommandContext(ctx, pigPath, append([]string{"--mode", "rpc"}, args...)...)
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
