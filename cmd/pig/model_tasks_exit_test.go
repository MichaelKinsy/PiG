package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
)

func TestExitProcessDrainsModelTasksBeforeExtensionTransports(t *testing.T) {
	const helper = "PIG_TEST_MODEL_TASK_EXIT_HELPER"
	if os.Getenv(helper) != "" {
		path := os.Getenv(helper)
		appendTrace := func(text string) {
			file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				panic(err)
			}
			if _, err := file.WriteString(text + "\n"); err != nil {
				panic(err)
			}
			if err := file.Close(); err != nil {
				panic(err)
			}
		}
		services, err := coding.NewServices(coding.ServicesOptions{CWD: filepath.Dir(path), AgentDir: filepath.Join(filepath.Dir(path), "agent")})
		if err != nil {
			t.Fatal(err)
		}
		started := make(chan struct{})
		services.Registry().StartModelTask(context.Background(), func(ctx context.Context) {
			close(started)
			<-ctx.Done()
			appendTrace("model tasks drained")
		})
		<-started
		stopModelServices = services.Close
		stopStartupExtensions = func() { appendTrace("extension transports stopped") }
		stopProfiles = func() { appendTrace("profiles stopped") }
		exitProcess(7)
		return
	}
	path := filepath.Join(t.TempDir(), "exit-order.txt")
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestExitProcessDrainsModelTasksBeforeExtensionTransports$")
	command.Env = append(os.Environ(), helper+"="+path)
	output, err := command.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("exit=%v output=%s", err, output)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "model tasks drained\nextension transports stopped\nprofiles stopped\n" {
		t.Fatalf("exit cleanup order=%q", data)
	}
}
