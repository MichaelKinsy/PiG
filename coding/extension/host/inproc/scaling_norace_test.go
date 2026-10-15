//go:build !race && linux

package inproc_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/perfgate"
)

// extensionSizes are the loaded-extension counts N, 10N and 100N of the extension scaling gates.
var extensionSizes = [3]int{10, 100, 1000}

// loadedExtensions is count extensions, each with three tools, one command, one flag and a tool_call handler that does nothing.
func loadedExtensions(count int) []extension.Extension {
	exts := make([]extension.Extension, count)
	for i := range exts {
		ext := extension.Extension{
			Name:      fmt.Sprintf("ext%d", i),
			Path:      fmt.Sprintf("/ext/%d", i),
			Handlers:  map[string][]extension.HandlerFn{"tool_call": {func(...any) (any, error) { return nil, nil }}},
			Tools:     map[string]extension.RegisteredTool{},
			Commands:  map[string]extension.RegisteredCommand{},
			Flags:     map[string]extension.ExtensionFlag{},
			Shortcuts: map[extension.KeyID]extension.ExtensionShortcut{},
		}
		for j := range 3 {
			name := fmt.Sprintf("tool_%d_%d", i, j)
			ext.Tools[name] = extension.RegisteredTool{Definition: extension.ToolDefinition{Name: name, Description: "a tool"}}
			ext.ToolOrder = append(ext.ToolOrder, name)
		}
		command := fmt.Sprintf("cmd%d", i)
		ext.Commands[command] = extension.RegisteredCommand{Name: command, Description: "a command"}
		ext.CommandOrder = append(ext.CommandOrder, command)
		exts[i] = ext
	}
	return exts
}

// Starting a Runner over N extensions copies nothing per extension: the allocation of construction does not grow with N (exponent 0).
func TestRunnerConstructionScalingGate(t *testing.T) {
	perfgate.CheckScaling(t, "extension.runner_new", extensionSizes, func(count int) func() {
		exts := loadedExtensions(count)
		return func() { _ = inproc.NewRunner(exts, ".") }
	})
}

// Listing the tools and the commands of N extensions walks each extension once.
func TestRunnerRegistryListingScalingGate(t *testing.T) {
	perfgate.CheckScaling(t, "extension.runner_tools", extensionSizes, func(count int) func() {
		runner := inproc.NewRunner(loadedExtensions(count), ".")
		return func() { _ = runner.Tools() }
	})
	perfgate.CheckScaling(t, "extension.runner_commands", extensionSizes, func(count int) func() {
		runner := inproc.NewRunner(loadedExtensions(count), ".")
		return func() { _ = runner.Commands() }
	})
}

// Finding one tool or command by name searches the extensions in load order: the last extension is the worst case. Pi re-resolves the commands on every lookup (runner.ts:550 getCommand) and so does PiG, so the command lookup is linear in N.
func TestRunnerLookupScalingGate(t *testing.T) {
	perfgate.CheckScaling(t, "extension.runner_tool_lookup", extensionSizes, func(count int) func() {
		runner := inproc.NewRunner(loadedExtensions(count), ".")
		last := fmt.Sprintf("tool_%d_2", count-1)
		return func() {
			if _, ok := runner.GetToolDefinition(last); !ok {
				panic("tool not found")
			}
		}
	})
	perfgate.CheckScaling(t, "extension.runner_command_lookup", extensionSizes, func(count int) func() {
		runner := inproc.NewRunner(loadedExtensions(count), ".")
		last := fmt.Sprintf("cmd%d", count-1)
		return func() {
			if _, ok := runner.Command(last); !ok {
				panic("command not found")
			}
		}
	})
}

// One event reaches every extension's handler: dispatching tool_call to N handlers allocates in proportion to N.
func TestRunnerEventDispatchScalingGate(t *testing.T) {
	event := extension.CustomToolCallEvent{ToolCallEventBase: extension.ToolCallEventBase{Type: "tool_call", ToolCallID: "c1"}, ToolName: "bash"}
	perfgate.CheckScaling(t, "extension.runner_dispatch", extensionSizes, func(count int) func() {
		runner := inproc.NewRunner(loadedExtensions(count), ".")
		return func() {
			if _, err := runner.EmitToolCall(context.Background(), event); err != nil {
				panic(err)
			}
		}
	})
}
