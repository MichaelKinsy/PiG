package extensionconformance

import (
	"encoding/json"
	"net"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

// The host-surface rows (SdkCore3Plus): OnThemeChange, OnToolsExpandedChange, SetWidgetRenderer, Bell, ToolCallIndex, ToolInput and ToolParams, ExtensionSourceInfo, OnLoaded and a factory-time events.emit, in every SDK against the real subprocess Host. The Go fixture (testfixture/modeltypes/host_surface.go), the Rust fixture (testdata/modeltypes-rust), the Python fixture (model_types_test.go) and the Node fixture (testdata/model-types.mjs) carry the same tools, so one table of rows proves each SDK. Every value a row asserts is one the host or the fixture's own logic produces and no SDK fallback can: the theme the host switched to, the state the host toggled, the width it announced, the bell count its UI recorded, the batch position and argument order the agent loop produced.

// hostSurfaceUI is the interactive UI of the rows: a tool-expansion state the test toggles and a count of the bells it was asked to ring.
type hostSurfaceUI struct {
	extension.UIContext
	expanded atomic.Bool
	bells    atomic.Int32
}

func newHostSurfaceUI() *hostSurfaceUI { return &hostSurfaceUI{UIContext: extension.NoopUIContext} }

func (u *hostSurfaceUI) GetToolsExpanded() bool { return u.expanded.Load() }
func (u *hostSurfaceUI) Bell()                  { u.bells.Add(1) }

// hostSurfaceSource is the SourceInfo the rows load every fixture with.
var hostSurfaceSource = extension.SourceInfo{Path: "/c3/source/entry", Source: "local", Scope: "project", Origin: "package", BaseDir: "/c3/source"}

// eventually polls check until it holds or the deadline passes.
func eventually(t *testing.T, what string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func logOf(report map[string]any) []any {
	entries, _ := report["log"].([]any)
	return entries
}

// ctx.ui.onThemeChange: the handler receives the theme the host switched to, once per change, until it unsubscribes. The expected entry is the new theme's name and the host's own foreground for it, so an SDK that never subscribes, hands the old theme or repeats an unchanged palette fails.
func TestOnThemeChangeAcrossSDKs(t *testing.T) {
	eachModelTypesPlacement(t, func(t *testing.T, r *modelTypesRig) {
		registry := tui.NewThemeRegistry()
		dark, light := registry.Get("dark"), registry.Get("light")
		entry := func(theme *tui.Theme) string { return theme.Name + "|" + theme.Fg("accent", "x") }
		if entry(dark) == entry(light) {
			t.Fatal("the two themes are indistinguishable")
		}

		r.setTheme(codingagent.ExtensionThemePalette(dark))
		r.tool("c3_theme_log") // the first state carries the dark palette: not a change
		r.tool("c3_theme_arm")
		r.setTheme(codingagent.ExtensionThemePalette(light))
		eventually(t, "the light theme", func() bool { return len(logOf(r.tool("c3_theme_log"))) == 1 })
		if got := logOf(r.tool("c3_theme_log")); !reflect.DeepEqual(got, []any{entry(light)}) {
			t.Fatalf("log = %q, want %q", got, entry(light))
		}

		r.setTheme(codingagent.ExtensionThemePalette(light)) // an equal palette is not a change
		r.tool("c3_theme_log")
		time.Sleep(300 * time.Millisecond)
		r.setTheme(codingagent.ExtensionThemePalette(dark))
		eventually(t, "the dark theme", func() bool { return len(logOf(r.tool("c3_theme_log"))) == 2 })
		if got := logOf(r.tool("c3_theme_log")); !reflect.DeepEqual(got, []any{entry(light), entry(dark)}) {
			t.Fatalf("log = %q, want light then dark", got)
		}

		r.tool("c3_theme_disarm")
		r.setTheme(codingagent.ExtensionThemePalette(light))
		r.tool("c3_theme_log")
		time.Sleep(300 * time.Millisecond)
		if got := logOf(r.tool("c3_theme_log")); len(got) != 2 {
			t.Fatalf("a handler that unsubscribed was called again: %q", got)
		}
	})
}

// ctx.ui.onToolsExpandedChange: the handler receives the state the host toggled, pushed by UIBridge.HostStateChanged (the interactive mode calls it on the expand key and on ui.setToolsExpanded).
func TestOnToolsExpandedChangeAcrossSDKs(t *testing.T) {
	eachModelTypesPlacement(t, func(t *testing.T, r *modelTypesRig) {
		ui := newHostSurfaceUI()
		r.bridge.SetUIContext(ui)
		r.tool("c3_expanded_log") // the first state carries expanded=false: not a change
		r.tool("c3_expanded_arm")

		ui.expanded.Store(true)
		r.bridge.HostStateChanged()
		eventually(t, "the expansion", func() bool { return len(logOf(r.tool("c3_expanded_log"))) == 1 })
		ui.expanded.Store(false)
		r.bridge.HostStateChanged()
		eventually(t, "the collapse", func() bool { return len(logOf(r.tool("c3_expanded_log"))) == 2 })
		if got := logOf(r.tool("c3_expanded_log")); !reflect.DeepEqual(got, []any{true, false}) {
			t.Fatalf("log = %v, want [true false]", got)
		}

		r.tool("c3_expanded_disarm")
		ui.expanded.Store(true)
		r.bridge.HostStateChanged()
		r.tool("c3_expanded_log")
		time.Sleep(300 * time.Millisecond)
		if got := logOf(r.tool("c3_expanded_log")); len(got) != 2 {
			t.Fatalf("a handler that unsubscribed was called again: %v", got)
		}
	})
}

// ctx.ui.setWidget(key, factory, {placement}): the widget lays its rows out at the width the host announced, and again at each new width with no call from the extension. The rows name the width they were rendered for and the theme the host published, so a widget pushed once as static rows fails the second width.
func TestSetWidgetRendererAcrossSDKs(t *testing.T) {
	eachModelTypesPlacement(t, func(t *testing.T, r *modelTypesRig) {
		ui := newHostSurfaceUI()
		var mu sync.Mutex
		var widgets map[string]*subprocess.PushProxy
		r.bridge.SetWidgetSyncFunc(func(current map[string]*subprocess.PushProxy) {
			mu.Lock()
			widgets = current
			mu.Unlock()
		})
		r.bridge.SetUIContext(ui)
		dark := tui.NewThemeRegistry().Get("dark")
		r.setTheme(codingagent.ExtensionThemePalette(dark))
		r.host.NotifyWidth(40)
		proxy := func() *subprocess.PushProxy {
			mu.Lock()
			defer mu.Unlock()
			return widgets["c3w"]
		}

		r.toolWith("c3_widget_arm", map[string]any{"placement": "belowEditor"})
		eventually(t, "the widget", func() bool { return proxy() != nil })
		if placement, _ := proxy().WidgetLayout(); placement != "belowEditor" {
			t.Fatalf("placement = %q, want belowEditor", placement)
		}
		eventually(t, "rows for width 40", func() bool { return reflect.DeepEqual(proxy().Render(40), []string{"w=40|dark"}) })

		r.host.NotifyWidth(60)
		eventually(t, "rows for width 60", func() bool { return reflect.DeepEqual(proxy().Render(60), []string{"w=60|dark"}) })

		r.tool("c3_widget_clear")
		eventually(t, "the widget to go", func() bool { return proxy() == nil })
	})
}

// ctx.Bell: the interactive UI is asked to ring once per call; with no terminal UI the call does nothing and reports no error.
func TestBellAcrossSDKs(t *testing.T) {
	eachModelTypesPlacement(t, func(t *testing.T, r *modelTypesRig) {
		if report := r.tool("c3_bell"); report["error"] != "<nil>" {
			t.Fatalf("bell without a UI = %v", report)
		}
		ui := newHostSurfaceUI()
		r.bridge.SetUIContext(ui)
		for want := int32(1); want <= 2; want++ {
			if report := r.tool("c3_bell"); report["error"] != "<nil>" {
				t.Fatalf("bell = %v", report)
			}
			eventually(t, "the bell", func() bool { return ui.bells.Load() == want })
		}
		time.Sleep(200 * time.Millisecond)
		if got := ui.bells.Load(); got != 2 {
			t.Fatalf("bells = %d, want 2", got)
		}
	})
}

// ctx.toolCallIndex, ctx.toolInput and the ordered params of a tool handler. The arguments carry keys in an order no sort produces, a nested object with array-index keys, and a float that prints differently in JavaScript; the expected texts are what JSON.stringify gives for the object Pi hands execute. The batch position is the call's place among the tool calls of its assistant message.
func TestToolCallInputAcrossSDKs(t *testing.T) {
	const raw = `{"zeta":1,"alpha":"a","meta":{"y":1.5,"b":{"z":2,"10":3,"2":4},"x":[{"q":1,"p":2}]},"7":"s","mid":1.0}`
	const meta = `{"y":1.5,"b":{"2":4,"10":3,"z":2},"x":[{"q":1,"p":2}]}`
	const input = `{"7":"s","zeta":1,"alpha":"a","meta":` + meta + `,"mid":1}`
	eachModelTypesPlacement(t, func(t *testing.T, r *modelTypesRig) {
		def, ok := r.ext.Tools["c3_tool_call"]
		if !ok {
			t.Fatalf("the fixture has no tool c3_tool_call (tools: %v)", r.ext.ToolOrder)
		}
		message := &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{
			ai.TextContent{Text: "calling"},
			ai.ToolCall{ID: "first", Name: "c3_tool_call"},
			ai.ToolCall{ID: "call-c3_tool_call", Name: "c3_tool_call"},
			ai.ToolCall{ID: "third", Name: "c3_tool_call"},
		}}
		ctx := agent.WithToolCallHookContext(t.Context(), agent.ToolCallHookContext{AssistantMessage: message})
		result, err := def.Definition.Execute(ctx, "call-c3_tool_call", json.RawMessage(raw), nil)
		if err != nil {
			t.Fatal(err)
		}
		var report struct {
			Index *int     `json:"index"`
			Input *string  `json:"input"`
			Keys  []string `json:"keys"`
			Meta  *string  `json:"meta"`
		}
		if err := json.Unmarshal([]byte(result.Text()), &report); err != nil {
			t.Fatalf("%q: %v", result.Text(), err)
		}
		if report.Index == nil || *report.Index != 1 {
			t.Errorf("index = %v, want 1 (the second tool call of its message)", report.Index)
		}
		if report.Input == nil || *report.Input != input {
			t.Errorf("input = %v, want %s", report.Input, input)
		}
		if want := []string{"7", "zeta", "alpha", "meta", "mid"}; !reflect.DeepEqual(report.Keys, want) {
			t.Errorf("keys = %v, want %v", report.Keys, want)
		}
		if report.Meta == nil || *report.Meta != meta {
			t.Errorf("meta = %v, want %s", report.Meta, meta)
		}

		// A call no assistant message issued has no batch position.
		result, err = def.Definition.Execute(t.Context(), "call-c3_tool_call", json.RawMessage(raw), nil)
		if err != nil {
			t.Fatal(err)
		}
		report.Index = nil
		if err := json.Unmarshal([]byte(result.Text()), &report); err != nil {
			t.Fatal(err)
		}
		if report.Index != nil {
			t.Errorf("index without an assistant message = %d, want none", *report.Index)
		}
	})
}

// ctx.extensionSourceInfo: the provenance the loader stamped on the extension, the value ExtensionRunner.getExtensionPaths reads.
func TestExtensionSourceInfoAcrossSDKs(t *testing.T) {
	eachModelTypesPlacement(t, func(t *testing.T, r *modelTypesRig) {
		report := r.tool("c3_source")
		want := map[string]any{"path": hostSurfaceSource.Path, "source": hostSurfaceSource.Source, "scope": hostSurfaceSource.Scope, "origin": hostSurfaceSource.Origin, "baseDir": hostSurfaceSource.BaseDir}
		if got, _ := report["info"].(map[string]any); !reflect.DeepEqual(got, want) {
			t.Fatalf("info = %v, want %v", report["info"], want)
		}
	})
}

// onLoaded and a factory-time events.emit. The loaded handler sleeps before it records, so a request served before it finished reads nothing; its context answers cwd and a host call. The factory subscribed to a channel and emitted on it before the extension connected: its own listener hears the payload once.
func TestOnLoadedAndFactoryEmitAcrossSDKs(t *testing.T) {
	eachModelTypesPlacement(t, func(t *testing.T, r *modelTypesRig) {
		report := r.tool("c3_loaded")
		loaded, _ := report["loaded"].(map[string]any)
		if loaded == nil {
			t.Fatalf("the first request ran before the loaded handler finished: %v", report)
		}
		if loaded["cwd"] != r.dir || loaded["hostCall"] != true {
			t.Errorf("loaded = %v, want cwd %q and a host call that returned", loaded, r.dir)
		}
		want := []any{map[string]any{"n": float64(1), "from": "factory"}}
		if got, _ := report["factoryHeard"].([]any); !reflect.DeepEqual(got, want) {
			t.Errorf("factory emit heard %v, want %v", got, want)
		}
		// Pi's emit is synchronous (event-bus.ts:13-34): the factory's own listener had run when emit returned, and an emit on "error" that nobody hears threw in the factory. A queued emit would have read 0 and false.
		if report["heardAtEmit"] != float64(1) {
			t.Errorf("the factory's listener had heard %v emits when emit returned, want 1", report["heardAtEmit"])
		}
		if report["errorEmitFailed"] != true {
			t.Errorf("an emit on \"error\" that nobody hears did not fail in the factory: errorEmitFailed = %v", report["errorEmitFailed"])
		}
	})
}

// earlierListenerDelay is how long the earlier extension's c3:cross listener takes before it answers and stamps the moment. An emit that does not wait for the listener, or a listener of the factory that runs before it finished, reads a time this much before the stamp.
const earlierListenerDelay = 250 * time.Millisecond

// crossProcessClockSlack is how far two processes' Unix-millisecond stamps may disagree about one instant, which the order of stamps taken by the test's process and the extension's must allow for. A Windows clock advances in ticks of up to 15.6 ms, and V8's Date.now() is the system time of its last resample plus the ticks the performance counter counted since (v8 src/base/platform/time.cc, Clock::Now), so a Node extension's stamp and the test's can differ by a tick while the events they stamp are a few milliseconds apart. A fifth of the delay leaves every violation, which reads the whole delay early, outside the slack.
const crossProcessClockSlack = earlierListenerDelay / 5

// earlierListener is an extension loaded before the fixture, the host's listener a factory-time emit reaches first. Its c3:cross listener takes its time, answers with a c3:ack emit, which Pi's shared emitter delivers to the listener the factory subscribed before it emitted, and then stamps the moment, so an emit that returned before the listener finished reads an earlier stamp than the listener's; its "error" listener handles the factory's emit on "error".
type earlierListener struct {
	mu         sync.Mutex
	cross      []any
	crossAt    int64
	ackErr     error
	errorsSeen []any
}

func loadEarlierListener(t *testing.T, r *modelTypesRig) *earlierListener {
	t.Helper()
	earlier := &earlierListener{}
	// A fused extension serves its factory over the host's pipe, and the pipe is the factory's own connection while it runs.
	serve := func(conn net.Conn) error {
		return sdk.ServeFactory(conn, func() *sdk.Extension {
			e := sdk.New("c3-earlier")
			if _, err := e.Events().On("c3:cross", func(ctx sdk.Context, data any) error {
				time.Sleep(earlierListenerDelay)
				ackErr := ctx.Events().Emit("c3:ack", map[string]any{"from": "c3-earlier"})
				earlier.mu.Lock()
				earlier.ackErr = ackErr
				earlier.cross = append(earlier.cross, data)
				earlier.crossAt = time.Now().UnixMilli()
				earlier.mu.Unlock()
				return nil
			}); err != nil {
				panic(err)
			}
			if _, err := e.Events().On("error", func(_ sdk.Context, data any) error {
				earlier.mu.Lock()
				earlier.errorsSeen = append(earlier.errorsSeen, data)
				earlier.mu.Unlock()
				return nil
			}); err != nil {
				panic(err)
			}
			return e
		})
	}
	if _, err := r.host.LoadInProcess(t.Context(), subprocess.ExtConfig{Name: "c3-earlier", Path: "/ext/c3-earlier.go", Enabled: true}, serve); err != nil {
		t.Fatal(err)
	}
	return earlier
}

// A factory's emit reaches the listeners of the extensions loaded before it, at once and in order, and the nested reply (the earlier listener's c3:ack emit, made while the factory's emit runs) reaches the listener the factory subscribed before it emitted, which Pi's shared emitter holds from the moment the factory subscribes it: the earlier extension's listener ran and finished before the factory's own listener ran, which ran before emit returned, and the earlier extension's "error" listener handled the factory's emit on "error", which therefore did not fail. The earlier listener sleeps before it stamps the time, so an SDK whose emit is queued or does not wait for the host's listeners reads a return time before the earlier stamp.
func TestFactoryEmitReachesAnEarlierLoadedExtensionAcrossSDKs(t *testing.T) {
	var earlier *earlierListener
	eachModelTypesPlacementAfter(t, nil, func(t *testing.T, r *modelTypesRig) {
		r.keepLoaded = true
		earlier = loadEarlierListener(t, r)
	}, func(t *testing.T, r *modelTypesRig) {
		report := r.tool("c3_loaded")
		earlier.mu.Lock()
		defer earlier.mu.Unlock()
		wantData := []any{map[string]any{"from": "factory"}}
		if !reflect.DeepEqual(earlier.cross, wantData) {
			t.Fatalf("the earlier extension heard %v on c3:cross, want %v", earlier.cross, wantData)
		}
		if !reflect.DeepEqual(earlier.errorsSeen, wantData) {
			t.Errorf("the earlier extension heard %v on error, want %v", earlier.errorsSeen, wantData)
		}
		if report["errorEmitFailed"] != false {
			t.Errorf("the factory's emit on \"error\" failed although an earlier extension listens: errorEmitFailed = %v", report["errorEmitFailed"])
		}
		if earlier.ackErr != nil {
			t.Errorf("the earlier extension's c3:ack emit failed: %v", earlier.ackErr)
		}
		wantAck := []any{map[string]any{"from": "c3-earlier"}}
		if got, _ := report["ackHeard"].([]any); !reflect.DeepEqual(got, wantAck) {
			t.Errorf("the factory's listener heard %v on c3:ack, want %v", report["ackHeard"], wantAck)
		}
		if report["ackAtCross"] != float64(1) {
			t.Errorf("the factory's listener had heard %v c3:ack emits when its c3:cross emit returned, want 1", report["ackAtCross"])
		}
		own, _ := report["crossOwnAt"].(float64)
		returned, _ := report["crossReturnedAt"].(float64)
		if own == 0 || returned == 0 {
			t.Fatalf("the factory recorded no c3:cross times: %v", report)
		}
		if !(float64(earlier.crossAt-crossProcessClockSlack.Milliseconds()) <= own && own <= returned) {
			t.Errorf("order: earlier listener finished at %d, factory's own listener ran at %v, emit returned at %v; want that order, to within %v of clock difference between the processes", earlier.crossAt, own, returned, crossProcessClockSlack)
		}
	})
}
