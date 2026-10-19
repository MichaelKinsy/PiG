package subprocess

import (
	"encoding/json"
	"maps"
	"net"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// pushRig is a Host with one managed extension on a pipe connection, a UI bridge whose catalogs the test controls, and the state_update frames the extension side received.
type pushRig struct {
	h        *Host
	me       *managedExt
	conn     *Conn
	bridge   *UIBridge
	frames   chan *Envelope
	prompt   atomic.Value // string
	theme    atomic.Value // string, the active theme's name
	commands atomic.Value // []CommandInfo
	keys     atomic.Value // string, the interrupt key the keybinding table binds
	caps     atomic.Value // TerminalCapabilitiesPayload
}

func newPushRig(t *testing.T) *pushRig {
	t.Helper()
	rig := &pushRig{frames: make(chan *Envelope, 16)}
	rig.h = NewHost(t.TempDir())
	t.Cleanup(func() { rig.h.Shutdown("test done") })
	var side net.Conn
	rig.conn, side = startedPipeConn(t, "push")
	rig.me = withConn(&managedExt{config: ExtConfig{Name: "push"}, host: rig.h}, rig.conn)
	rig.h.exts = map[string]*managedExt{"push": rig.me}
	rig.bridge = NewUIBridge(func() {})
	rig.commands.Store([]CommandInfo{{Name: "cmdA"}})
	rig.prompt.Store("system prompt")
	rig.theme.Store("dark")
	rig.bridge.SetHostAction("getSystemPrompt", func() string { return rig.prompt.Load().(string) })
	rig.bridge.SetThemeFunc(func() any {
		return map[string]any{"name": rig.theme.Load().(string), "foregrounds": map[string]string{"accent": "\x1b[1m"}}
	})
	rig.keys.Store("escape")
	rig.caps.Store(TerminalCapabilitiesPayload{Images: "kitty", TrueColor: true})
	rig.bridge.SetKeybindingsFunc(func() any {
		return map[string]any{"definitions": map[string]any{"app.interrupt": rig.keys.Load().(string)}}
	})
	rig.bridge.SetTerminalCapabilitiesFunc(func() TerminalCapabilitiesPayload { return rig.caps.Load().(TerminalCapabilitiesPayload) })
	rig.bridge.SetHostAction("getCommands", func() []CommandInfo { return rig.commands.Load().([]CommandInfo) })
	rig.h.SetUIBridge(rig.bridge)
	go func() {
		for {
			env, err := readEnvelopeFrom(side)
			if err != nil {
				return
			}
			switch env.Type {
			case MsgNotify:
				if env.Notify != nil && env.Notify.Method == "state_update" {
					rig.frames <- env
				}
			case MsgRequest:
				_ = writeEnvelopeTo(side, &Envelope{Type: MsgResponse, ID: env.ID, Response: &ResponsePayload{}})
			case MsgPing:
				_ = writeEnvelopeTo(side, &Envelope{Type: MsgPong, Pong: &PongPayload{Nonce: env.Ping.Nonce}})
			}
		}
	}()
	return rig
}

func (r *pushRig) push(t *testing.T) {
	t.Helper()
	if err := r.h.pushStateTo(t.Context(), r.me, r.conn); err != nil {
		t.Fatal(err)
	}
}

func (r *pushRig) expectNoFrame(t *testing.T, why string) {
	t.Helper()
	select {
	case env := <-r.frames:
		t.Fatalf("%s: a state_update of %d bytes was sent", why, len(env.Notify.Args))
	case <-time.After(150 * time.Millisecond):
	}
}

func (r *pushRig) nextState(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	select {
	case env := <-r.frames:
		var payload struct {
			State map[string]json.RawMessage `json:"state"`
		}
		if err := json.Unmarshal(env.Notify.Args, &payload); err != nil {
			t.Fatal(err)
		}
		return payload.State
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a state_update")
		return nil
	}
}

// A push the recipient already holds is not sent; a catalog change goes out in full with the unchanged catalogs omitted; and the runtime-side merge of every lean push equals the full snapshot (the runtime's applyState keeps keys a state_update leaves out).
func TestPushStateToSkipsIdenticalAndOmitsUnchangedCatalogs(t *testing.T) {
	rig := newPushRig(t)
	rig.push(t)
	first := rig.nextState(t)
	if len(first["commands"]) == 0 {
		t.Fatal("the first push to a connection must carry commands")
	}
	rig.push(t)
	rig.expectNoFrame(t, "an identical push")

	rig.commands.Store([]CommandInfo{{Name: "cmdA"}, {Name: "cmdB"}})
	rig.push(t)
	second := rig.nextState(t)
	if len(second["commands"]) == 0 {
		t.Fatal("a catalog change must deliver commands")
	}
	if _, present := second["systemPromptOptions"]; present {
		t.Fatal("an unchanged catalog must stay out of a catalog-change push")
	}
	rig.push(t)
	rig.expectNoFrame(t, "an identical push after a catalog change")

	// Presentation state travels only when it changes: a theme change sends the theme alone, and a system prompt emptied to "" is still sent.
	rig.theme.Store("light")
	rig.push(t)
	third := rig.nextState(t)
	for _, key := range []string{"commands", "systemPrompt", "keybindings", "terminalCapabilities"} {
		if _, present := third[key]; present {
			t.Fatalf("a theme-only push carried %s", key)
		}
	}
	if !strings.Contains(string(third["theme"]), `"light"`) {
		t.Fatalf("theme push = %s", third["theme"])
	}
	rig.prompt.Store("")
	rig.push(t)
	fourth := rig.nextState(t)
	if got, present := fourth["systemPrompt"]; !present || string(got) != `""` {
		t.Fatalf("an emptied system prompt must still be sent, got %q present=%v", got, present)
	}
	if _, present := fourth["theme"]; present {
		t.Fatal("an unchanged theme was sent again")
	}
	rig.push(t)
	rig.expectNoFrame(t, "an identical push after presentation changes")

	merged := map[string]json.RawMessage{}
	for _, state := range []map[string]json.RawMessage{first, second, third, fourth} {
		maps.Copy(merged, state)
	}
	full, err := json.Marshal(map[string]any{"state": rig.bridge.Snapshot(rig.me.flagNames, 0, false)})
	if err != nil {
		t.Fatal(err)
	}
	var want struct {
		State map[string]json.RawMessage `json:"state"`
	}
	if err := json.Unmarshal(full, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(merged, want.State) {
		t.Fatalf("merged lean pushes differ from a full snapshot\nmerged: %v\nfull:   %v", merged, want.State)
	}
}

// The keybinding table and the terminal capabilities travel only when they change, and a change to either reaches the next push alone: the omission is by comparison with what the recipient holds, so a table the user rebinds or a terminal that reports other capabilities is never kept stale.
func TestPushStateToSendsChangedKeybindingsAndTerminalCapabilities(t *testing.T) {
	rig := newPushRig(t)
	rig.push(t)
	first := rig.nextState(t)
	for _, key := range []string{"keybindings", "terminalCapabilities", "theme"} {
		if len(first[key]) == 0 {
			t.Fatalf("the first push to a connection must carry %s", key)
		}
	}
	rig.push(t)
	rig.expectNoFrame(t, "unchanged presentation state")

	rig.keys.Store("ctrl+c")
	rig.push(t)
	second := rig.nextState(t)
	if !strings.Contains(string(second["keybindings"]), "ctrl+c") {
		t.Fatalf("a rebound key did not reach the next push: %s", second["keybindings"])
	}
	for _, key := range []string{"terminalCapabilities", "theme", "commands", "systemPrompt"} {
		if _, present := second[key]; present {
			t.Fatalf("a keybinding-only push carried %s", key)
		}
	}
	rig.push(t)
	rig.expectNoFrame(t, "an identical push after a rebinding")

	rig.caps.Store(TerminalCapabilitiesPayload{Images: "iterm2", Hyperlinks: true})
	rig.push(t)
	third := rig.nextState(t)
	var caps TerminalCapabilitiesPayload
	if err := json.Unmarshal(third["terminalCapabilities"], &caps); err != nil || caps != (TerminalCapabilitiesPayload{Images: "iterm2", Hyperlinks: true}) {
		t.Fatalf("changed terminal capabilities did not reach the next push: %s (%v)", third["terminalCapabilities"], err)
	}
	for _, key := range []string{"keybindings", "theme", "commands", "systemPrompt"} {
		if _, present := third[key]; present {
			t.Fatalf("a capabilities-only push carried %s", key)
		}
	}

	// A change back is a change: the recipient holds the other values now.
	rig.keys.Store("escape")
	rig.caps.Store(TerminalCapabilitiesPayload{Images: "kitty", TrueColor: true})
	rig.push(t)
	fourth := rig.nextState(t)
	if !strings.Contains(string(fourth["keybindings"]), "escape") || !strings.Contains(string(fourth["terminalCapabilities"]), "kitty") {
		t.Fatalf("a change back did not reach the next push: %v", fourth)
	}
	rig.push(t)
	rig.expectNoFrame(t, "an identical push after changing back")
}

// A state field the runtime changes before the host does (thinking level, editor text) is not in the host's cache, so the next push is a full snapshot even when the host's state is unchanged: a host that clamps the value must still correct the runtime.
func TestPushStateToResendsAfterTheRuntimeMutatesItsOwnState(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T, rig *pushRig)
	}{
		{"setThinkingLevel", func(t *testing.T, rig *pushRig) {
			rig.bridge.SetHostAction("setThinkingLevel", func(string) {})
			if _, err := rig.bridge.handleCall(t.Context(), "push", rig.conn, &CallPayload{Method: "setThinkingLevel", Args: json.RawMessage(`{"level":"high"}`)}); err != nil {
				t.Fatal(err)
			}
		}},
		{"terminal_input", func(t *testing.T, rig *pushRig) {
			ui := &terminalInputUI{UIContext: extension.NoopUIContext}
			rig.bridge.SetUIContext(ui)
			rig.bridge.RegisterExtConn("push", rig.conn, false)
			if _, err := rig.bridge.handleOnTerminalInput("push", nil); err != nil {
				t.Fatal(err)
			}
			ui.handler("x")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rig := newPushRig(t)
			rig.push(t)
			rig.nextState(t)
			rig.push(t)
			rig.expectNoFrame(t, "an identical push before the runtime mutation")
			tc.mutate(t, rig)
			rig.push(t)
			state := rig.nextState(t)
			if len(state["commands"]) == 0 {
				t.Fatalf("the push after %s must be a full snapshot, got keys %v", tc.name, keysOf(state))
			}
			rig.push(t)
			rig.expectNoFrame(t, "an identical push after the full snapshot")
		})
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// With getSettingsJSON bound, a state push carries the host's settings encoding as it is and does not encode getSettings again (EX-F4).
func TestSnapshotTakesTheHostSettingsEncoding(t *testing.T) {
	bridge := NewUIBridge(func() {})
	var reads atomic.Int32
	bridge.SetHostAction("getSettings", func() extension.Settings {
		reads.Add(1)
		return extension.Settings{"theme": "dark"}
	})
	if got := string(bridge.Snapshot(nil, 0, false).Settings); got != `{"theme":"dark"}` {
		t.Fatalf("settings without getSettingsJSON = %s", got)
	}
	encoded := json.RawMessage(`{"theme":"dark"}`)
	bridge.SetHostAction("getSettingsJSON", func() json.RawMessage { return encoded })
	reads.Store(0)
	for range 3 {
		if got := bridge.Snapshot(nil, 0, false).Settings; unsafe.SliceData(got) != unsafe.SliceData(encoded) { //nolint:gosec // G103: compares backing-array addresses to prove the encoding is reused; the pointers are never dereferenced
			t.Fatalf("settings = %s, want the host's encoding unchanged", got)
		}
	}
	if got := reads.Load(); got != 0 {
		t.Fatalf("getSettings ran %d times with getSettingsJSON bound, want 0", got)
	}
}
