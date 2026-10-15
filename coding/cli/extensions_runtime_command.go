package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/coding/extension/host/tsruntime"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// extensionsRuntimeStatus is the JSON shape of `pig extensions runtime status --json`.
type extensionsRuntimeStatus struct {
	Request   tsruntime.Request     `json:"request"`
	Platform  string                `json:"platform"`
	Root      string                `json:"root"`
	Selection tsruntime.Selection   `json:"selection"`
	Installed []tsruntime.Installed `json:"installed"`
	// NodeOnly lists the extension sets that failed to start on a slot runtime and started on Node: they run on Node until a file of the set changes.
	//portlint:allow emptydrop PiG-only `pig extensions runtime status --json` (pig additive (D19), no Pi counterpart); an absent nodeOnly means no set runs on Node only
	NodeOnly []tsruntime.NodeOnlyEntry `json:"nodeOnly,omitempty"`
}

// runExtensionsRuntime runs `pig extensions runtime status`: the TypeScript extension runtime this configuration requests, the
// runtime a launch would use with the reason for a fallback to Node, and the runtimes installed in the slot.
// pig additive (D19): Pi has one extension runtime, its own process.
func runExtensionsRuntime(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "status" {
		printExtensionsRuntimeHelp(stderr)
		return 2
	}
	jsonOutput := false
	for _, arg := range args[1:] {
		switch arg {
		case "--json":
			jsonOutput = true
		case "-h", "--help":
			printExtensionsRuntimeHelp(stdout)
			return 0
		default:
			_, _ = fmt.Fprintf(stderr, "pig extensions runtime status: unknown argument %q\n", arg)
			return 2
		}
	}
	agentDir, err := codingagent.ResolveAgentDir()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "pig extensions runtime status:", err)
		return 1
	}
	cwd, err := os.Getwd()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "pig extensions runtime status:", err)
		return 1
	}
	subprocess.SetTypeScriptRuntimeSetting(codingagent.NewSettingsManager(cwd, agentDir).GetExtensionsTypescriptRuntime())
	status := extensionsRuntimeStatus{Request: subprocess.TypeScriptRuntimeRequest(), Platform: tsruntime.Platform(), Root: tsruntime.Root(agentDir)}
	status.Selection = tsruntime.Selection{Request: status.Request, Runtime: tsruntime.Node}
	if status.Request.ID != tsruntime.Node {
		// A runtime with images per extension set has none for the empty set: its status names the sets it holds.
		status.Selection = tsruntime.NewResolver(status.Root, tsruntime.NewVerifier(tsruntime.Lock)).Resolve(ctx, status.Request, tsruntime.Set{})
	}
	status.NodeOnly = tsruntime.ListNodeOnly(status.Root)
	if status.Installed, err = tsruntime.List(status.Root); err != nil {
		_, _ = fmt.Fprintln(stderr, "pig extensions runtime status:", err)
		return 1
	}
	if jsonOutput {
		//portlint:allow jsonescape PiG-only `pig extensions runtime status --json` (pig additive (D19), no Pi counterpart)
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(status); err != nil {
			_, _ = fmt.Fprintln(stderr, "pig extensions runtime status:", err)
			return 1
		}
		return 0
	}
	writeExtensionsRuntimeStatus(stdout, status)
	return 0
}

func writeExtensionsRuntimeStatus(w io.Writer, status extensionsRuntimeStatus) {
	_, _ = fmt.Fprintf(w, "Requested: %s (%s)\n", status.Request.ID, status.Request.Source)
	sel := status.Selection
	if sel.IsNode() {
		_, _ = fmt.Fprintln(w, "Selected:  node")
	} else {
		_, _ = fmt.Fprintf(w, "Selected:  %s (%s %s, %s)\n", sel.Runtime, sel.Kind, sel.Version, sel.Executable)
	}
	for _, skip := range sel.Skipped {
		_, _ = fmt.Fprintf(w, "Skipped:   %s (%s: %s)\n", skip.Runtime, skip.Reason, skip.Detail)
	}
	_, _ = fmt.Fprintf(w, "Platform:  %s\n", status.Platform)
	defer func() {
		if len(status.NodeOnly) == 0 {
			return
		}
		_, _ = fmt.Fprintln(w, "Node-only extension sets (they run on Node until a file of the set changes):")
		for _, entry := range status.NodeOnly {
			names := make([]string, len(entry.Members))
			for i, member := range entry.Members {
				names[i] = member.Name
			}
			_, _ = fmt.Fprintf(w, "  %s  %s\n", strings.Join(names, ", "), entry.Detail)
		}
	}()
	root := status.Root
	if root == "" {
		root = "(unknown)"
	}
	_, _ = fmt.Fprintf(w, "Slot:      %s\n", root)
	if len(status.Installed) == 0 {
		_, _ = fmt.Fprintln(w, "Installed: none")
		return
	}
	_, _ = fmt.Fprintln(w, "Installed:")
	for _, item := range status.Installed {
		if item.Manifest == nil {
			_, _ = fmt.Fprintf(w, "  %s  invalid: %s\n", item.ID, item.Error)
			continue
		}
		m := item.Manifest
		details := []string{string(m.Kind), m.Version, m.Platform, "from " + m.Provider}
		if m.Fallback != "" {
			details = append(details, "falls back to "+m.Fallback)
		}
		if len(m.Sets) > 0 {
			details = append(details, fmt.Sprintf("%d extension sets", len(m.Sets)))
		}
		_, _ = fmt.Fprintf(w, "  %s  %s\n", item.ID, strings.Join(details, ", "))
	}
}

func printExtensionsRuntimeHelp(w io.Writer) {
	_, _ = fmt.Fprintf(w, "Usage:\n  pig extensions runtime status [--json]\n\nReports the runtime that runs TypeScript and JavaScript extensions: the extensionsTypescriptRuntime setting or %s, the runtime a launch uses (Node when the requested runtime cannot run, with the reason) and the runtimes installed under %s or <agent dir>/runtimes/typescript.\n", tsruntime.EnvRuntime, tsruntime.EnvRoot)
}
