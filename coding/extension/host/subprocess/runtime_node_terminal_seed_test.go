package subprocess

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"
)

// Pi resolves terminal capabilities once per process, and the extensions it loads share that cache (packages/tui/src/terminal-image.ts:34,53-67,160-169; packages/coding-agent/src/main.ts:853,887). PiG's host owns the terminal, so a Node extension process starts with the host's resolved capabilities. It must not repeat the terminal probe: Pi's theme initialization reads the capabilities, and under tmux the repeated probe is a synchronous tmux subprocess on every Node extension's startup path.
func TestNodeRuntimeStartsWithHostTerminalCapabilities(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the recording tmux stand-in is a POSIX shell script")
	}
	shortSockDir(t)
	probeLog := installRecordingTmux(t)

	var mu sync.Mutex
	var notifications []string
	fakeUI := newTestUIContext()
	fakeUI.onNotify = func(msg, _ string) {
		mu.Lock()
		notifications = append(notifications, msg)
		mu.Unlock()
	}
	h := newTestHost(t)
	h.SetMode("tui")
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(fakeUI)
	// Hyperlinks and true color differ from what the environment probe would resolve: there is no COLORTERM hint and the stand-in reports no tmux features.
	bridge.SetTerminalCapabilitiesFunc(func() TerminalCapabilitiesPayload {
		return TerminalCapabilitiesPayload{TrueColor: true, Hyperlinks: true}
	})
	h.SetUIBridge(bridge)
	defer h.Shutdown("test done")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ext, err := h.Load(ctx, ExtConfig{
		Name:    "terminal-capabilities",
		Source:  filepath.Join("testdata", "terminal-capabilities.mjs"),
		Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ext.Commands["report_capabilities"].Handler(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	var report string
	pollUntil(t, 10*time.Second, "extension never reported its capabilities", func() bool {
		mu.Lock()
		defer mu.Unlock()
		if len(notifications) == 0 {
			return false
		}
		report = notifications[len(notifications)-1]
		return true
	})
	var got struct {
		AtFactory map[string]any `json:"atFactory"`
		Leaked    bool           `json:"leaked"`
	}
	if err := json.Unmarshal([]byte(report), &got); err != nil {
		t.Fatalf("report %q: %v", report, err)
	}
	if got.AtFactory["images"] != nil || got.AtFactory["trueColor"] != true || got.AtFactory["hyperlinks"] != true {
		t.Errorf("factory-time capabilities = %v, want the host's {images:null trueColor:true hyperlinks:true}", got.AtFactory)
	}
	if got.Leaked {
		t.Error("the host's capability seed leaked into the extension's environment")
	}
	assertNoTmuxProbe(t, probeLog)
}

// Packed Node members share one process, as Pi's extensions share its process, and start with the host's capabilities exactly as an isolated member does.
func TestPackedNodeCellStartsWithHostTerminalCapabilities(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the recording tmux stand-in is a POSIX shell script")
	}
	nodeCellRequireNode(t)
	shortSockDir(t)
	probeLog := installRecordingTmux(t)
	root := t.TempDir()
	var configs []ExtConfig
	for _, name := range []string{"caps-one", "caps-two"} {
		entry := filepath.Join(root, name+".mjs")
		src := `import { getCapabilities } from "@earendil-works/pi-tui";
export default function (pi) {
  const report = JSON.stringify({ atFactory: getCapabilities(), leaked: process.env.PIG_TERMINAL_CAPABILITIES !== undefined });
  pi.registerTool({
    name: ` + strconv.Quote(name) + `,
    label: ` + strconv.Quote(name) + `,
    description: "report factory-time terminal capabilities",
    parameters: { type: "object", properties: {} },
    async execute() { return { content: [{ type: "text", text: report }] }; },
  });
}
`
		if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		configs = append(configs, ExtConfig{Name: name, Source: entry, Enabled: true})
	}
	h := NewHostWithConfigRoot(root, filepath.Join(root, "config"))
	bridge := NewUIBridge(func() {})
	bridge.SetTerminalCapabilitiesFunc(func() TerminalCapabilitiesPayload {
		return TerminalCapabilitiesPayload{Images: "kitty", TrueColor: true, Hyperlinks: true}
	})
	h.SetUIBridge(bridge)
	t.Cleanup(func() { h.Shutdown("test done") })
	// An inherited seed is replaced by the host's own capabilities.
	t.Setenv(terminalCapabilitiesEnv, `{"images":"iterm2","trueColor":false,"hyperlinks":false}`)

	loaded, errs := h.LoadAll(t.Context(), configs)
	if len(errs) != 0 || len(loaded) != len(configs) {
		t.Fatalf("LoadAll = %d extensions, %v; want %d", len(loaded), errs, len(configs))
	}
	var pid int
	for _, ext := range h.Extensions() {
		h.mu.Lock()
		memberPID := h.exts[ext.Name].proc.Pid
		h.mu.Unlock()
		if pid != 0 && memberPID != pid {
			t.Fatalf("members use different processes: %d and %d; want one packed cell", pid, memberPID)
		}
		pid = memberPID
		result, err := ext.Tools[ext.Name].Definition.Execute(t.Context(), "caps-"+ext.Name, json.RawMessage(`{}`), nil)
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		var toolResult struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(data, &toolResult); err != nil || len(toolResult.Content) != 1 {
			t.Fatalf("tool %s result = %s, %v", ext.Name, data, err)
		}
		content := toolResult.Content
		var got struct {
			AtFactory map[string]any `json:"atFactory"`
			Leaked    bool           `json:"leaked"`
		}
		if err := json.Unmarshal([]byte(content[0].Text), &got); err != nil {
			t.Fatalf("report %q: %v", content[0].Text, err)
		}
		if got.AtFactory["images"] != "kitty" || got.AtFactory["trueColor"] != true || got.AtFactory["hyperlinks"] != true {
			t.Errorf("%s factory-time capabilities = %v, want the host's {images:kitty trueColor:true hyperlinks:true}", ext.Name, got.AtFactory)
		}
		if got.Leaked {
			t.Errorf("%s: the host's capability seed leaked into the extension's environment", ext.Name)
		}
	}
	assertNoTmuxProbe(t, probeLog)
}

// The runtime seeds pi-tui's capability cache from the host before Pi's theme initialization reads it, and removes the seed from its environment.
func TestNodeRuntimeSeedsTerminalCapabilitiesBeforeTheme(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the recording tmux stand-in is a POSIX shell script")
	}
	probeLog := installRecordingTmux(t)
	t.Setenv(terminalCapabilitiesEnv, `{"images":"kitty","trueColor":true,"hyperlinks":true}`)
	base, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	script := "const base = " + strconv.Quote((&url.URL{Scheme: "file", Path: filepath.ToSlash(base) + "/"}).String()) + ";\n" + `
import assert from "node:assert/strict";
const { Runtime } = await import(new URL("runtime-node/runtime.mjs", base));
const { getCapabilities } = await import(new URL("runtime-node/shims/pi-dist/pi-tui/terminal-image.js", base));
const { theme } = await import(new URL("runtime-node/shims/pi-dist/pi-coding-agent/modes/interactive/theme/theme.js", base));
new Runtime("/ext/seed.mjs");
assert.deepEqual(getCapabilities(), { images: "kitty", trueColor: true, hyperlinks: true });
assert.equal(theme.getColorMode(), "truecolor");
assert.equal(process.env.PIG_TERMINAL_CAPABILITIES, undefined);
new Runtime("/ext/packed-sibling.mjs");
assert.deepEqual(getCapabilities(), { images: "kitty", trueColor: true, hyperlinks: true });
`
	if output, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("seed terminal capabilities: %v\n%s", err, output)
	}
	assertNoTmuxProbe(t, probeLog)
}

// installRecordingTmux puts a tmux stand-in first on PATH, runs every process as though inside tmux without an explicit capability override, and returns the log the stand-in appends each invocation to.
func installRecordingTmux(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	probeLog := filepath.Join(dir, "tmux.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + probeLog + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX", filepath.Join(dir, "server")+",1,0")
	for _, key := range []string{"COLORTERM", "PI_HYPERLINKS", "PI_TRUE_COLOR", "PI_IMAGE_PROTOCOL"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	return probeLog
}

func assertNoTmuxProbe(t *testing.T, probeLog string) {
	t.Helper()
	data, err := os.ReadFile(probeLog)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Errorf("the Node runtime probed the terminal again instead of using the host's capabilities: tmux %s", data)
}
