//go:build unix

package main

import (
	"context"
	"os"

	"github.com/MichaelKinsy/PiG/internal/evals"
	"github.com/MichaelKinsy/PiG/internal/evals/evalsuites"
)

// Paths of the evaluator image (internal/evals/docker/Dockerfile).
const imageRoot = "/opt/pig-evals"

func entrypoint(ctx context.Context, args []string) (int, error) {
	config := evals.EntrypointConfig{
		PackageRoot: "/repo/packages/evals", Runner: imageRoot + "/pig-evals",
		Binaries:   []string{imageRoot + "/pig", imageRoot + "/pig-eval-extension"},
		AuthSource: "/run/pi-eval-secrets/auth.json", HostAgentDir: "/tmp/pi-eval-host-agent", ArtifactDir: "/artifacts",
		Suites: evals.Runner{Suites: evalsuites.All(), PackageRoot: "/repo/packages/evals"},
	}
	return evals.RunEntrypoint(ctx, config, args, os.Stdout)
}
