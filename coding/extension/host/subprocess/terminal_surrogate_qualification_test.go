package subprocess

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/tui"
)

type surrogateInputUI struct {
	extension.UIContext
	registered chan extension.RemoteTerminalInputHandler
}

func (u *surrogateInputUI) OnRemoteTerminalInput(_ string, handler extension.RemoteTerminalInputHandler) func() {
	u.registered <- handler
	return func() {}
}

// upstream: packages/tui/src/stdin-buffer.ts:202-253
// upstream: packages/tui/src/tui.ts:138-139
func TestTerminalInputNodeSurrogateQualification(t *testing.T) {
	shortSockDir(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PIG_HOME", filepath.Join(home, "pig"))
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(home, "pig", "agent"))
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "pi", "agent"))
	source := filepath.Join(t.TempDir(), "surrogate-probe.mjs")
	const factory = `export default function(pi) {
 pi.registerCommand("subscribe", {handler: (_args, ctx) => {
 ctx.ui.onTerminalInput(data => {
 if(data === "rewrite-high") return {data: "\ud83d"};
 if(data === "rewrite-low") return {data: "\ude00"};
 const units=s=>Array.from({length:s.length},(_,i)=>s.charCodeAt(i));
 return {data:JSON.stringify({units:units(data),editor:units(ctx.ui.getEditorText())})};
 });
 }});
 }`
	if err := os.WriteFile(source, []byte(factory), 0600); err != nil {
		t.Fatal(err)
	}
	h := newTestHost(t)
	bridge := NewUIBridge(func() {})
	ui := &surrogateInputUI{UIContext: extension.NoopUIContext, registered: make(chan extension.RemoteTerminalInputHandler, 1)}
	bridge.SetUIContext(ui)
	h.SetUIBridge(bridge)
	defer h.Shutdown("qualification complete")
	ext, err := h.Load(t.Context(), ExtConfig{Name: "surrogate-probe", Source: source, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := ext.Commands["subscribe"].Handler(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	handler := awaitTerminalInputSignal(t, ui.registered, "terminal listener not registered")
	e := tui.NewEditor()
	parser := tui.NewStdinBuffer(tui.StdinBufferOptions{})
	for _, event := range parser.ProcessTerminalBytes([]byte("A😀B")) {
		got := handler(WithTerminalInputState(t.Context(), e.Text(), false), event)
		if got.Data == nil {
			t.Fatal("missing observed units")
		}
		var observed struct {
			Units  []uint16 `json:"units"`
			Editor []uint16 `json:"editor"`
		}
		if err := json.Unmarshal([]byte(*got.Data), &observed); err != nil {
			t.Fatal(err)
		}
		want := jsstring.ToUTF16(event)
		if !slices.Equal(observed.Units, want) {
			t.Errorf("terminal event at native=%04x: Node observed=%04x, want=%04x", jsstring.ToUTF16(e.Text()), observed.Units, want)
		}
		if want := jsstring.ToUTF16(e.Text()); !slices.Equal(observed.Editor, want) {
			t.Errorf("Node editor=%04x, want=%04x", observed.Editor, want)
		}
		e.HandleInput(event)
	}
	for _, tc := range []struct {
		key  string
		unit uint16
	}{{"rewrite-high", 0xd83d}, {"rewrite-low", 0xde00}} {
		got := handler(WithTerminalInputState(t.Context(), e.Text(), false), tc.key)
		if got.Data == nil {
			t.Fatal("missing rewritten data")
		}
		if units := jsstring.ToUTF16(*got.Data); !slices.Equal(units, []uint16{tc.unit}) {
			t.Errorf("%s host units=%04x, want=%04x", tc.key, units, tc.unit)
		}
	}
}
