package main

import (
	"context"
	"fmt"
	"os"

	piglet "github.com/MichaelKinsy/PiG/coding/piglet"

	"golang.org/x/term"
)

func runPigletAgentEnvironment(ctx context.Context, p *piglet.Piglet, flags CLIFlags, cwd string) (bool, int, error) {
	unsafeHost, err := unknownBoolFlag(flags.UnknownFlags, "unsafe-host")
	if err != nil {
		return false, 0, err
	}
	engine, err := unknownStringFlag(flags.UnknownFlags, "container-engine", "auto")
	if err != nil {
		return false, 0, err
	}
	workspace, err := unknownStringFlag(flags.UnknownFlags, "workspace", cwd)
	if err != nil {
		return false, 0, err
	}
	if p == nil || p.AgentEnv == nil {
		for _, flag := range []string{"container-engine", "workspace"} {
			if _, set := flags.UnknownFlags[flag]; set {
				return false, 0, fmt.Errorf("--%s requires a Piglet with agentEnv", flag)
			}
		}
		if unsafeHost {
			return false, 0, fmt.Errorf("--unsafe-host requires a Piglet with agentEnv")
		}
		return false, 0, nil
	}
	result, err := piglet.RunAgentEnvironment(ctx, p, piglet.AgentEnvironmentRuntimeOptions{
		Engine: engine, Workspace: workspace, Args: os.Args[1:], PigVersion: PigVersion,
		UnsafeHost: unsafeHost,
		IO: piglet.RuntimeIO{
			Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr,
			TTY: term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())),
		},
	})
	if err != nil {
		return false, 0, err
	}
	return !result.Continue, result.ExitCode, nil
}

func unknownBoolFlag(flags map[string]any, name string) (bool, error) {
	value, ok := flags[name]
	if !ok {
		return false, nil
	}
	boolean, ok := value.(bool)
	if !ok || !boolean {
		return false, fmt.Errorf("--%s does not accept a value", name)
	}
	return true, nil
}

func unknownStringFlag(flags map[string]any, name, fallback string) (string, error) {
	value, ok := flags[name]
	if !ok {
		return fallback, nil
	}
	text, ok := value.(string)
	if !ok || text == "" {
		return "", fmt.Errorf("--%s requires a value", name)
	}
	return text, nil
}

func pigletWasRequested(flags map[string]any) bool {
	if _, ok := flags["piglet"]; ok {
		return true
	}
	return os.Getenv("PIG_PIGLET_PATH") != "" || os.Getenv("PIG_PIGLET_NAME") != ""
}
