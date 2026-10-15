//go:build !pig_strip_node_extensions

package subprocess

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Pi's interactive mode starts loading the remaining highlight.js grammars only after startup has emitted session_start and rendered
// (interactive-mode.ts:1100-1122), so a session_start handler never waits behind that load. The Node runtime starts the load when the
// startup session_start handling ends, not when the host's ready frame arrives.
func TestNodeRuntimeLoadsHighlightLanguagesAfterStartupSessionStart(t *testing.T) {
	nodeCellRequireNode(t)
	for _, isolation := range []string{"strict", "shared-ok"} {
		t.Run(isolation, func(t *testing.T) {
			dir := t.TempDir()
			entry, trace := filepath.Join(dir, "highlight-order.mjs"), filepath.Join(dir, "trace")
			// erlang is not in the eager grammar set, so highlightCode treats it as an unknown language until the whole set loaded.
			source := fmt.Sprintf(`import { appendFileSync } from "node:fs";
		import { highlightCode } from "@earendil-works/pi-coding-agent";
		const code = "-module(m).\nf() -> ok.";
		const loaded = () => highlightCode(code, "erlang").join("\n") !== highlightCode(code, "no-such-language").join("\n");
		export default function(pi) {
		  pi.registerCommand("probe", { description: "probe", handler: async () => appendFileSync(%[1]q, "probe " + loaded() + "\n") });
		  pi.on("session_start", () => appendFileSync(%[1]q, "session_start " + loaded() + "\n"));
		}`, trace)
			if err := os.WriteFile(entry, []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			h := NewHost(t.TempDir())
			h.SetMode("tui")
			t.Cleanup(func() { h.Shutdown("test complete") })
			loaded, errs := h.LoadAll(t.Context(), []ExtConfig{{Name: "highlight-order", Source: entry, Enabled: true, Isolation: isolation}})
			if len(errs) != 0 || len(loaded) != 1 {
				t.Fatalf("load: %v, %v", loaded, errs)
			}
			probe := func() string {
				if err := loaded[0].Commands["probe"].Handler(t.Context(), ""); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(trace)
				if err != nil {
					t.Fatal(err)
				}
				lines := strings.Split(strings.TrimSpace(string(data)), "\n")
				return lines[len(lines)-1]
			}
			// Before session_start the load has not started, however long the host waits to emit it.
			for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
				if got := probe(); got != "probe false" {
					t.Fatalf("before session_start: %q, want the grammars not loaded yet", got)
				}
			}
			if _, err := inproc.NewRunner(loaded, dir).Emit(t.Context(), extension.SessionStartEvent{Type: "session_start", Reason: "startup"}); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(trace)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), "session_start false\n") {
				t.Fatalf("session_start handler ran with the grammars loaded or not at all:\n%s", data)
			}
			pollUntil(t, 20*time.Second, "the grammars never loaded after session_start", func() bool { return probe() == "probe true" })
		})
	}
}

// An extension that handles no startup event, or removed its handler, waits for nothing: the load starts when the ready frame arrives.
func TestNodeRuntimeLoadsHighlightLanguagesAtReadyWithoutStartupHandlers(t *testing.T) {
	nodeCellRequireNode(t)
	for name, subscribe := range map[string]string{
		"none":         ``,
		"unsubscribed": `pi.on("session_start", () => {})();`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			entry, trace := filepath.Join(dir, "highlight-ready.mjs"), filepath.Join(dir, "trace")
			source := fmt.Sprintf(`import { appendFileSync } from "node:fs";
import { highlightCode } from "@earendil-works/pi-coding-agent";
const code = "-module(m).\nf() -> ok.";
const loaded = () => highlightCode(code, "erlang").join("\n") !== highlightCode(code, "no-such-language").join("\n");
export default function(pi) {
  pi.registerCommand("probe", { description: "probe", handler: async () => appendFileSync(%q, "probe " + loaded() + "\n") });
  %s
}`, trace, subscribe)
			if err := os.WriteFile(entry, []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			h := NewHost(t.TempDir())
			h.SetMode("tui")
			t.Cleanup(func() { h.Shutdown("test complete") })
			loaded, errs := h.LoadAll(t.Context(), []ExtConfig{{Name: "highlight-ready", Source: entry, Enabled: true}})
			if len(errs) != 0 || len(loaded) != 1 {
				t.Fatalf("load: %v, %v", loaded, errs)
			}
			pollUntil(t, 20*time.Second, "the grammars never loaded", func() bool {
				if err := loaded[0].Commands["probe"].Handler(t.Context(), ""); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(trace)
				return err == nil && strings.HasSuffix(string(data), "probe true\n")
			})
		})
	}
}
