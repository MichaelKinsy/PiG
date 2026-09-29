package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	ctx := context.Background()
	cwd, err := os.MkdirTemp("", "dynamic-tools-pig-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(cwd)
	source, err := filepath.Abs(".upstream/current/packages/coding-agent/examples/extensions/dynamic-tools.ts")
	if err != nil {
		return err
	}
	host := subprocess.NewHostWithConfigRoot(cwd, filepath.Join(cwd, "config"))
	defer host.Shutdown("complete")
	bridge := subprocess.NewUIBridge(func() {})
	host.SetUIBridge(bridge)
	loaded, failures := host.LoadAll(ctx, []subprocess.ExtConfig{{Name: "dynamic-tools", Source: source, Enabled: true}})
	if len(failures) != 0 {
		return fmt.Errorf("load: %v", failures)
	}
	runner := inproc.NewRunner(loaded, cwd)
	services, err := coding.NewServices(coding.ServicesOptions{CWD: cwd, AgentDir: filepath.Join(cwd, "agent")})
	if err != nil {
		return err
	}
	session, err := coding.NewSession(services, coding.SessionOptions{Runner: runner, NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		return err
	}
	defer session.Close()
	bridge.SetHostAction("refreshTools", session.RefreshTools)
	if len(session.ActiveToolNames()) != 0 {
		return fmt.Errorf("tools registered before session_start")
	}
	if err := session.BindExtensions(ctx); err != nil {
		return err
	}
	for _, name := range []string{"echo_session", "shout"} {
		if name == "shout" && !runner.ExecuteCommand(ctx, "add-echo-tool", "shout") {
			return fmt.Errorf("missing command")
		}
		found := false
		for _, tool := range session.Tools() {
			if tool.Name() != name {
				continue
			}
			found = true
			result, err := tool.Execute(ctx, "dynamic", []byte(`{"message":"hello"}`), nil)
			if err != nil {
				return err
			}
			if result.IsError {
				return fmt.Errorf("tool failed: %s", result.Text())
			}
			row := struct {
				Active []string `json:"active"`
				Name   string   `json:"name"`
				Text   string   `json:"text"`
			}{session.ActiveToolNames(), name, result.Text()}
			if err := json.NewEncoder(os.Stdout).Encode(row); err != nil {
				return err
			}
		}
		if !found {
			return fmt.Errorf("missing dynamic tool %s", name)
		}
	}
	return nil
}
