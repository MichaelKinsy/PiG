//go:build !pig_strip_node_extensions

package coding

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// The real Node transport must admit the synchronous handler body before SetSessionName returns, including when an earlier Promise remains pending (Pi agent-session.ts:3563 and runner.ts:995).
func TestSessionNameNotificationNodePrefix(t *testing.T) {
	for _, packed := range []bool{false, true} {
		t.Run(fmt.Sprintf("packed=%t", packed), func(t *testing.T) {
			root := t.TempDir()
			trace := filepath.Join(root, "trace")
			if err := os.WriteFile(trace, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			var configs []subprocess.ExtConfig
			names := []string{"name-observer"}
			if packed {
				names = append(names, "sibling")
			}
			for _, name := range names {
				path := filepath.Join(root, name+".mjs")
				source := fmt.Sprintf(`import {appendFileSync, writeFileSync} from "node:fs";
export default function(pi) {
  writeFileSync(%q, String(process.pid));
  if (%q === "sibling") return;
  pi.on("session_info_changed", async event => {
    appendFileSync(%q, event.name + "\n");
    if (event.name === "first") await new Promise(() => {});
  });
}`, filepath.Join(root, name+".pid"), name, trace)
				if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
					t.Fatal(err)
				}
				configs = append(configs, subprocess.ExtConfig{Name: name, Source: path, Enabled: true})
			}
			host := subprocess.NewHost(t.TempDir())
			t.Cleanup(func() { host.Shutdown("name test complete") })
			var loaded []extension.Extension
			if packed {
				var errs []error
				loaded, errs = host.LoadAll(t.Context(), configs)
				if len(errs) != 0 || len(loaded) != len(configs) {
					t.Fatalf("loaded = %d, errors = %v", len(loaded), errs)
				}
			} else {
				ext, err := host.Load(t.Context(), configs[0])
				if err != nil {
					t.Fatal(err)
				}
				loaded = []extension.Extension{*ext}
			}
			if packed {
				firstPID, err := os.ReadFile(filepath.Join(root, "name-observer.pid"))
				if err != nil {
					t.Fatal(err)
				}
				siblingPID, err := os.ReadFile(filepath.Join(root, "sibling.pid"))
				if err != nil || string(firstPID) != string(siblingPID) {
					t.Fatalf("members did not share a process: %q, %q, %v", firstPID, siblingPID, err)
				}
			}
			h := newRecoveryHarness(t, harnessOptions{extension: loaded[0]})
			setNameThroughBridge(t, h.session, "first")
			if err := h.session.SetSessionName("second"); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(trace)
			if err != nil || string(data) != "first\nsecond\n" {
				t.Fatalf("immediate extension trace = %q, %v", data, err)
			}
			if err := h.session.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
