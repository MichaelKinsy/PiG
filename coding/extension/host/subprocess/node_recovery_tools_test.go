// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package subprocess

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestPackedRecoveryReplacesRegisteredTools (D20, wire:3fa52897): a recovered packed member registers afresh, and its runner keeps the Extension it
// holds with the tools of the new registration exactly, as the isolated restart does (host.go ReplaceEventHandlers + ReplaceRegisteredTools). The
// member registers a tool named after how often its factory has run, so a recovery that keeps the old Extension's tools shows the first run's tool.
func TestPackedRecoveryReplacesRegisteredTools(t *testing.T) {
	skipWithoutNodeExtensions(t)
	t.Parallel()
	nodeCellRequireNode(t)
	root := t.TempDir()
	runs := filepath.Join(root, "runs")
	var configs []ExtConfig
	for i := range 2 {
		name := fmt.Sprintf("member%d", i)
		entry := filepath.Join(root, name+".mjs")
		source := fmt.Sprintf(`import {appendFileSync, readFileSync, existsSync} from "node:fs";
export default function(pi) {
 const file = %q + ".%s";
 appendFileSync(file, "x");
 const run = readFileSync(file, "utf8").length;
 pi.registerTool({name:%q+"-run"+run,label:"probe",description:"probe",parameters:{type:"object",properties:{}},execute:async()=>({content:[{type:"text",text:"ok"}]})});
}`, runs, name, name)
		if err := os.WriteFile(entry, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		configs = append(configs, ExtConfig{Name: name, Source: entry, Enabled: true, RuntimeKind: "subprocess", RuntimeLanguage: "node", EntrypointKind: "factory", SDKName: "pi-node"})
	}
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	recordCrashNotices(t, h)
	if _, errs := h.LoadAll(t.Context(), configs); len(errs) > 0 {
		t.Fatal(errs)
	}
	toolNames := func(name string) []string {
		h.mu.Lock()
		me := h.exts[name]
		h.mu.Unlock()
		var names []string
		for _, tool := range me.ext.RegisteredTools() {
			names = append(names, tool.Definition.Name)
		}
		slices.Sort(names)
		return names
	}
	if got := toolNames("member0"); !slices.Equal(got, []string{"member0-run1"}) {
		t.Fatalf("first load tools = %v", got)
	}
	h.mu.Lock()
	pid := pidOf(h.exts["member0"])
	h.mu.Unlock()
	if err := killTestProcess(pid); err != nil {
		t.Fatal(err)
	}
	waitRecovered(t, h, []string{"member0", "member1"}, pid)
	if got := toolNames("member0"); !slices.Equal(got, []string{"member0-run2"}) {
		t.Fatalf("recovered tools = %v, want exactly the new registration's [member0-run2]", got)
	}
}
