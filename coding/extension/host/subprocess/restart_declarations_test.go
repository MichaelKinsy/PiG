package subprocess

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// A restarted extension's declarations are exactly what its new process registered. The supervisor's restart of an isolated binary (adoptConn) and a Node cell's recovery (acceptPackedExt) both keep the Extension the runner holds and adopt the new registration into it: each fixture registers its command, flag, shortcut and tool with the generation that loaded it, crashes once, and the recovered Extension must report the second generation, as a fresh load of the same code would. The first registration's values are stale state no Pi process can observe, since Pi has no supervisor restart.
func TestRestartAdoptsTheNewRegistrationsDeclarations(t *testing.T) {
	t.Run("isolated", func(t *testing.T) {
		binPath := buildFixture(t)
		shortSockDir(t)
		t.Setenv("FIXTURE_GENERATION_FILE", filepath.Join(t.TempDir(), "generation"))
		h := newTestHost(t)
		t.Cleanup(func() { h.Shutdown("test done") })
		recordCrashNotices(t, h)
		cfg := ExtConfig{Name: "fixture", Path: binPath, Enabled: true, SupervisorConfig: SupervisorConfig{MaxCrashes: 3, CrashWindow: time.Minute, InitialDelay: time.Millisecond, MaxDelay: 10 * time.Millisecond, BackoffFactor: 2}}
		if _, err := h.Load(t.Context(), cfg); err != nil {
			t.Fatal(err)
		}
		held := h.exts["fixture"].ext
		assertDeclarationGeneration(t, held, "gen 1", false)
		crash, ok := held.Command("crash")
		if !ok {
			t.Fatal("crash command missing")
		}
		_ = crash.Handler(t.Context(), "")
		waitDeclarationGeneration(t, h, "fixture", held, "gen 2")
		assertDeclarationGeneration(t, held, "gen 2", false)
	})
	t.Run("packed-node", func(t *testing.T) {
		nodeCellRequireNode(t)
		root := t.TempDir()
		var configs []ExtConfig
		for _, name := range []string{"member0", "member1"} {
			counter := filepath.Join(root, name+".generation")
			source := fmt.Sprintf(`import {existsSync, readFileSync, writeFileSync} from "node:fs";
export default function(pi) {
 const generation = existsSync(%[1]q) ? Number(readFileSync(%[1]q, "utf8")) + 1 : 1;
 writeFileSync(%[1]q, String(generation));
 const label = "gen " + generation;
 pi.registerCommand("probe", {description: label, handler: async () => {}});
 pi.registerShortcut("ctrl+alt+g", {description: label, handler: async () => {}});
 pi.registerFlag("probe-flag", {description: label, type: "string", default: label});
 pi.registerTool({name: %[2]q, label: "probe", description: label, parameters: {type: "object", properties: {}}, execute: async () => { process.exit(23); }});
}`, counter, name+"-crash")
			entry := filepath.Join(root, name+".mjs")
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
		held := h.exts["member0"].ext
		assertDeclarationGeneration(t, held, "gen 1", true)
		oldPID := pidOf(h.exts["member0"])
		crash, ok := held.RegisteredTool("member0-crash")
		if !ok {
			t.Fatal("crash tool missing")
		}
		_, _ = crash.Definition.Execute(t.Context(), "crash", []byte(`{}`), nil)
		waitRecovered(t, h, []string{"member0", "member1"}, oldPID)
		waitDeclarationGeneration(t, h, "member0", held, "gen 2")
		assertDeclarationGeneration(t, held, "gen 2", true)
	})
}

// waitDeclarationGeneration waits until the recovered member holds the Extension the runner held and its command reports want.
func waitDeclarationGeneration(t *testing.T, h *Host, name string, held *extension.Extension, want string) {
	t.Helper()
	deadline := time.Now().Add(testbudget.Wait(t))
	for sleep := 10 * time.Millisecond; ; sleep = min(sleep*2, 200*time.Millisecond) {
		h.mu.Lock()
		me := h.exts[name]
		h.mu.Unlock()
		if me != nil && me.ext == held {
			if command, ok := held.Command("probe"); ok && command.Description == want {
				return
			}
		}
		if time.Now().After(deadline) {
			command, _ := held.Command("probe")
			var notices []string
			if log, found := crashNoticeLogs.Load(h); found {
				log := log.(*crashNotices)
				log.mu.Lock()
				notices = append(notices, log.notices...)
				log.mu.Unlock()
			}
			t.Fatalf("%s: restarted command description = %q, want %q (same Extension: %t; crash notices: %q)", name, command.Description, want, me != nil && me.ext == held, notices)
		}
		time.Sleep(sleep)
	}
}

func assertDeclarationGeneration(t *testing.T, ext *extension.Extension, want string, tool bool) {
	t.Helper()
	var got []string
	for _, command := range ext.RegisteredCommands() {
		if command.Name == "probe" {
			got = append(got, "command "+command.Description)
		}
	}
	for _, flag := range ext.RegisteredFlags() {
		got = append(got, fmt.Sprintf("flag %s %v", flag.Description, flag.Default))
	}
	for _, shortcut := range ext.RegisteredShortcuts() {
		got = append(got, "shortcut "+shortcut.Description)
	}
	expected := []string{"command " + want, "flag " + want + " " + want, "shortcut " + want}
	if tool {
		for _, registered := range ext.RegisteredTools() {
			got = append(got, "tool "+registered.Definition.Description)
		}
		expected = append(expected, "tool "+want)
	}
	if strings.Join(got, "\n") != strings.Join(expected, "\n") {
		t.Fatalf("declarations:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(expected, "\n"))
	}
}
