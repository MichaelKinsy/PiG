package subprocess

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// waitReaped blocks until managed's underlying OS process has exited, or
// fails the test after 5s. managed.exitedCh is populated only for an
// isolated extension's own reaper goroutine; a packed member (every
// conventional Node factory packs by default since N8) has no exitedCh of
// its own - it shares packedProcess with its cell siblings, so waiting on a
// nil exitedCh here would block forever regardless of whether the process
// actually exited. managed.packedProcess.startWait(), unlike exitedCh, is
// the packed side's own completion boundary (the same channel
// watchPackedProcess and every packed accept path already wait on): calling
// it here only observes that existing reaper, idempotently (sync.Once), so
// this needs no OS-specific PID liveness check and compiles and reaps
// correctly on every platform, including Windows, where a Unix "kill -0"
// style poll is unavailable.
func waitReaped(t *testing.T, managed *managedExt) {
	t.Helper()
	if managed.exitedCh != nil {
		select {
		case <-managed.exitedCh:
		case <-time.After(5 * time.Second):
			t.Fatal("retained extension was not reaped")
		}
		return
	}
	if managed.packedProcess == nil {
		t.Fatal("managed extension has neither exitedCh nor a packedProcess to reap")
	}
	select {
	case <-managed.packedProcess.startWait():
	case <-time.After(5 * time.Second):
		t.Fatal("retained extension was not reaped")
	}
}

func startupNodeFixture(t *testing.T, directory, name, body string) ExtConfig {
	t.Helper()
	path := filepath.Join(directory, name+".mjs")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return ExtConfig{Name: name, Source: path, Enabled: true}
}

// Source: resource-loader.test.ts, user extensions load before trust and are
// reused after trust resolves. Also exercise the denied final selection.
func TestHostFinalExtensionSetPreservesPreTrustState(t *testing.T) {
	for _, trusted := range []bool{true, false} {
		t.Run(fmt.Sprint(trusted), func(t *testing.T) {
			dir := t.TempDir()
			marker := filepath.Join(dir, "events")
			user := startupNodeFixture(t, dir, "user", fmt.Sprintf(`
import { appendFileSync } from "node:fs";
export default function(pi) {
 appendFileSync(%q, "factory\n");
 let trustCalls = 0;
 pi.on("project_trust", () => { trustCalls++; return {trusted: %q}; });
 pi.on("session_start", () => appendFileSync(%q, "state:"+trustCalls+"\n"));
 pi.registerCommand("user-trust", {description:"user trust",handler:async()=>{}});
}`, marker, map[bool]string{true: "yes", false: "no"}[trusted], marker))
			project := startupNodeFixture(t, dir, "project", `export default function(pi) { pi.registerCommand("project-trusted", {description:"project trusted",handler:async()=>{}}); }`)
			host := NewHostWithConfigRoot(dir, filepath.Join(dir, "home"))
			t.Cleanup(func() { host.Shutdown("test") })
			preload, errs := host.LoadAll(t.Context(), []ExtConfig{user})
			if len(errs) != 0 || len(preload) != 1 {
				t.Fatalf("preload=%v errors=%v", preload, errs)
			}
			managed := host.exts[user.Name]
			if _, err := preload[0].Handlers["project_trust"][0](map[string]any{"type": "project_trust", "cwd": dir}); err != nil {
				t.Fatal(err)
			}
			selected := []ExtConfig{user}
			wantNames := []string{"user"}
			if trusted {
				selected = []ExtConfig{project, user}
				wantNames = []string{"project", "user"}
			}
			loaded, errs := host.LoadFinalExtensionSet(t.Context(), selected, []ExtConfig{user})
			var names []string
			for _, ext := range loaded {
				names = append(names, ext.Name)
			}
			if len(errs) != 0 || !reflect.DeepEqual(names, wantNames) || host.exts[user.Name] != managed {
				t.Fatalf("final names=%v errors=%v retained=%v", names, errs, host.exts[user.Name] == managed)
			}
			if _, err := loaded[len(loaded)-1].Handlers["session_start"][0](map[string]any{"type": "session_start"}); err != nil {
				t.Fatal(err)
			}
			observed, err := os.ReadFile(marker)
			if err != nil || string(observed) != "factory\nstate:1\n" {
				t.Fatalf("factory/state=%q error=%v", observed, err)
			}
			host.Shutdown("test")
			waitReaped(t, managed)
			if _, err := os.Stat(host.sockRuntimeDir); !os.IsNotExist(err) {
				t.Fatalf("socket directory survived shutdown: %v", err)
			}
		})
	}
}

func TestHostFinalExtensionSetDoesNotRetryFailedPreload(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "attempts")
	failed := startupNodeFixture(t, dir, "failed", fmt.Sprintf(`import {appendFileSync} from "node:fs";
export default function() { appendFileSync(%q,"attempt\n"); throw new Error("preload failed"); }`, marker))
	host := NewHostWithConfigRoot(dir, filepath.Join(dir, "home"))
	defer host.Shutdown("test")
	_, errs := host.LoadAll(t.Context(), []ExtConfig{failed})
	if len(errs) != 1 {
		t.Fatalf("preload errors=%v", errs)
	}
	priorErrors := host.LoadErrors()
	loaded, errs := host.LoadFinalExtensionSet(t.Context(), []ExtConfig{failed}, []ExtConfig{failed})
	observed, err := os.ReadFile(marker)
	if len(loaded) != 0 || len(errs) != 0 || err != nil || string(observed) != "attempt\n" || !reflect.DeepEqual(priorErrors, host.LoadErrors()) {
		t.Fatalf("loaded=%v errors=%v attempts=%q read=%v retained errors=%v", loaded, errs, observed, err, host.LoadErrors())
	}
}

func TestHostFinalExtensionSetOwnsUnselectedPreloadUntilShutdown(t *testing.T) {
	dir := t.TempDir()
	config := startupNodeFixture(t, dir, "removed", `export default function() {}`)
	host := NewHostWithConfigRoot(dir, filepath.Join(dir, "home"))
	defer host.Shutdown("test")
	_, errs := host.LoadAll(t.Context(), []ExtConfig{config})
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	managed := host.exts[config.Name]
	loaded, errs := host.LoadFinalExtensionSet(t.Context(), nil, []ExtConfig{config})
	if len(errs) != 0 || len(loaded) != 0 || host.ExtensionCount() != 1 {
		t.Fatalf("loaded=%v errors=%v count=%d", loaded, errs, host.ExtensionCount())
	}
	host.Shutdown("test")
	waitReaped(t, managed)
	if _, err := os.Stat(managed.sockPath); !os.IsNotExist(err) {
		t.Fatalf("removed socket survived: %v", err)
	}
	if strings.TrimSpace(managed.stderrLogPath) == "" {
		t.Fatal("fixture did not exercise a subprocess")
	}
}

// Upstream loader.ts awaits extension factories in path order, and runner.ts
// dispatches tool_call in that same order. A blocking first handler must
// therefore prevent every later handler at startup and after repeated reloads.
func TestHostLoadAndReloadPreserveConfiguredGuardOrder(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("PIG_HOME", filepath.Join(root, "pig-home"))
	marker := filepath.Join(root, "guard-order")
	commandMarker := filepath.Join(root, "command-order")

	configs := make([]ExtConfig, 0, 3)
	for _, name := range []string{"c", "b", "a"} {
		result := `{ block: false }`
		if name == "c" {
			result = `{ block: true, reason: "first guard" }`
		}
		configs = append(configs, startupNodeFixture(t, root, name, fmt.Sprintf(`
import { appendFileSync } from "node:fs";
export default function(pi) {
  pi.on("tool_call", () => {
    appendFileSync(%q, %q);
    return %s;
  });
  pi.registerCommand("dup", {
    description: "duplicate command",
    handler: async () => appendFileSync(%q, %q),
  });
}`, marker, name, result, commandMarker, name)))
	}

	host := NewHostWithConfigRoot(root, filepath.Join(root, "config"))
	host.SetConfigLoader(func() ([]ExtConfig, error) { return append([]ExtConfig(nil), configs...), nil })
	t.Cleanup(func() { host.Shutdown("test") })

	assertGuardOrder := func(label string, loaded []extension.Extension) {
		t.Helper()
		if err := os.WriteFile(marker, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(commandMarker, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		names := make([]string, len(loaded))
		for i := range loaded {
			names[i] = loaded[i].Name
		}
		if !reflect.DeepEqual(names, []string{"c", "b", "a"}) {
			t.Fatalf("%s extension order = %v, want [c b a]", label, names)
		}
		runner := inproc.NewRunner(loaded, root)
		result, err := runner.EmitToolCall(t.Context(), extension.CustomToolCallEvent{
			ToolCallEventBase: extension.ToolCallEventBase{Type: "tool_call", ToolCallID: label},
			ToolName:          "fixture",
			Input:             map[string]any{},
		})
		if err != nil {
			t.Fatalf("%s tool_call: %v", label, err)
		}
		if result == nil || !result.Block {
			t.Fatalf("%s tool_call result = %#v, want block", label, result)
		}
		calls, err := os.ReadFile(marker)
		if err != nil {
			t.Fatal(err)
		}
		if string(calls) != "c" {
			t.Fatalf("%s handler calls = %q, want only first configured guard", label, calls)
		}
		if !runner.ExecuteCommand(t.Context(), "dup:1", "") {
			t.Fatalf("%s did not resolve /dup:1", label)
		}
		commandCalls, err := os.ReadFile(commandMarker)
		if err != nil {
			t.Fatal(err)
		}
		if string(commandCalls) != "c" {
			t.Fatalf("%s /dup:1 owner = %q, want c", label, commandCalls)
		}
	}

	loaded, errs := host.LoadAll(t.Context(), configs)
	if len(errs) != 0 {
		t.Fatalf("startup errors = %v", errs)
	}
	assertGuardOrder("startup", loaded)
	for i := range 20 {
		loaded, err := host.Reload(t.Context())
		if err != nil {
			t.Fatalf("reload %d: %v", i+1, err)
		}
		assertGuardOrder(fmt.Sprintf("reload-%d", i+1), loaded)
	}
}

// Upstream resource-loader.ts clears the module cache before every reload.
// The fresh factory has fresh module state even when the source did not change.
func TestHostReloadRestartsUnchangedNodeExtension(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("PIG_HOME", filepath.Join(root, "pig-home"))
	marker := filepath.Join(root, "counter")
	config := startupNodeFixture(t, root, "counter", fmt.Sprintf(`
import { appendFileSync } from "node:fs";
import { randomUUID } from "node:crypto";
const factoryLoadedAt = randomUUID();
let count = 0;
export default function(pi) {
  pi.on("tool_call", () => {
    count++;
    appendFileSync(%q, factoryLoadedAt + ":" + count + "\n");
    return { block: false };
  });
}`, marker))

	host := NewHostWithConfigRoot(root, filepath.Join(root, "config"))
	host.SetConfigLoader(func() ([]ExtConfig, error) { return []ExtConfig{config}, nil })
	t.Cleanup(func() { host.Shutdown("test") })
	loaded, errs := host.LoadAll(t.Context(), []ExtConfig{config})
	if len(errs) != 0 || len(loaded) != 1 {
		t.Fatalf("startup loaded=%v errors=%v", loaded, errs)
	}
	emit := func(exts []extension.Extension, id string) {
		t.Helper()
		runner := inproc.NewRunner(exts, root)
		if _, err := runner.EmitToolCall(t.Context(), extension.CustomToolCallEvent{
			ToolCallEventBase: extension.ToolCallEventBase{Type: "tool_call", ToolCallID: id},
			ToolName:          "fixture",
			Input:             map[string]any{},
		}); err != nil {
			t.Fatal(err)
		}
	}
	emit(loaded, "before-1")
	emit(loaded, "before-2")

	reloaded, err := host.Reload(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded) != 1 {
		t.Fatalf("reloaded extensions = %v", reloaded)
	}
	emit(reloaded, "after")
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("counter output = %q", data)
	}
	firstID, firstCount, ok := strings.Cut(lines[0], ":")
	if !ok || firstCount != "1" || lines[1] != firstID+":2" {
		t.Fatalf("before reload output = %q, want one factory counts 1,2", lines[:2])
	}
	afterID, afterCount, ok := strings.Cut(lines[2], ":")
	if !ok || afterCount != "1" || afterID == firstID {
		t.Fatalf("after reload output = %q, want fresh factory id at count 1", lines[2])
	}
}
