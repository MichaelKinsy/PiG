package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() (err error) {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	root, err := os.MkdirTemp("", "pig-user-bash-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(root)) }()
	host := subprocess.NewHost(root)
	defer host.Shutdown("probe done")
	ext, err := host.Load(context.Background(), subprocess.ExtConfig{Name: "user-bash-validation", Source: filepath.Join(cwd, "test/parity/scenarios/extensions-runtime/testdata/ext/user-bash-validation.mjs"), Enabled: true})
	if err != nil {
		return err
	}
	runner := inproc.NewRunner([]extension.Extension{*ext}, cwd)
	var reported [][2]string
	runner.AddErrorListener(func(err *extension.ExtensionError) { reported = append(reported, [2]string{err.Event, err.Error}) })
	for _, command := range []string{"valid", "undefined-exit", "missing-exit", "null-exit", "null-path", "both-with-null-operations", "empty", "incomplete", "throws", "observe"} {
		reported = [][2]string{}
		result, err := runner.EmitUserBash(context.Background(), extension.UserBashEvent{Type: "user_bash", Command: command, Cwd: cwd, ExcludeFromContext: false})
		failure := ""
		if err != nil {
			failure = err.Error()
		}
		row := []any{command, result, failure, reported}
		if command == "undefined-exit" && result != nil {
			record := result.Result.(map[string]any)
			exitCode, present := record["exitCode"]
			row = []any{command, present, exitCode, failure, reported}
		}
		if err := json.NewEncoder(os.Stdout).Encode(row); err != nil {
			return err
		}
	}
	return nil
}
