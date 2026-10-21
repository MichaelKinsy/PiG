package modeltypes

import (
	"fmt"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// registerHostSurface registers the tools of the host-surface rows (host_surface_test.go): OnThemeChange, OnToolsExpandedChange, SetWidgetRenderer, Bell, ToolCallIndex, ToolInput and ToolParams, ExtensionSourceInfo, OnLoaded and a factory-time events.emit. testdata/modeltypes-rust, the inline Python fixture and testdata/model-types.mjs carry the same tools, so one table of rows proves every SDK.
func registerHostSurface(e *sdk.Extension) {
	object := sdk.Schema{"type": "object"}
	var (
		mu           sync.Mutex
		themeLog     = []string{}
		expandedLog  = []bool{}
		heardFactory = []any{}
		// heardAtEmit is how many emits the factory's own listener had heard when the factory's emit returned; errorEmitFailed whether the factory's emit on "error", which nobody hears, failed.
		heardAtEmit     int
		errorEmitFailed bool
		// crossOwnAt is when the factory's own c3:cross listener ran and crossReturnedAt when the factory's emit on c3:cross returned, in Unix milliseconds: an extension loaded before this one that listens on c3:cross runs first and finishes before emit returns.
		crossOwnAt, crossReturnedAt int64
		// ackHeard are the c3:ack emits the factory's listener heard: an extension loaded before this one answers the factory's c3:cross emit, from inside its listener, with a c3:ack emit, which Pi's shared emitter delivers to the listener this factory subscribed before it emitted. ackAtCross is how many it had heard when the c3:cross emit returned.
		ackHeard    = []any{}
		ackAtCross  int
		loaded      any
		themeOff    func()
		expandedOff func()
	)
	tool := func(name string, execute func(ctx sdk.Context, args map[string]any) (any, error)) {
		e.RegisterTool(sdk.ToolDefinition{Name: name, Label: name, Description: name, Parameters: object, Execute: execute})
	}

	// A factory's pi.events.emit reaches the listeners subscribed before it, its own included.
	if _, err := e.Events().On("c3:factory", func(_ sdk.Context, data any) error {
		mu.Lock()
		heardFactory = append(heardFactory, data)
		mu.Unlock()
		return nil
	}); err != nil {
		panic(err)
	}
	if err := e.Events().Emit("c3:factory", map[string]any{"n": 1, "from": "factory"}); err != nil {
		panic(err)
	}
	// Pi's emit is synchronous: the listener has run when emit returns, and an emit on "error" that nobody hears throws in the factory.
	mu.Lock()
	heardAtEmit = len(heardFactory)
	mu.Unlock()
	errorEmitFailed = e.Events().Emit("error", map[string]any{"from": "factory"}) != nil
	if _, err := e.Events().On("c3:ack", func(_ sdk.Context, data any) error {
		mu.Lock()
		ackHeard = append(ackHeard, data)
		mu.Unlock()
		return nil
	}); err != nil {
		panic(err)
	}
	if _, err := e.Events().On("c3:cross", func(sdk.Context, any) error {
		mu.Lock()
		crossOwnAt = time.Now().UnixMilli()
		mu.Unlock()
		return nil
	}); err != nil {
		panic(err)
	}
	if err := e.Events().Emit("c3:cross", map[string]any{"from": "factory"}); err != nil {
		panic(err)
	}
	crossReturnedAt = time.Now().UnixMilli()
	mu.Lock()
	ackAtCross = len(ackHeard)
	mu.Unlock()
	// The handler takes its time; a request that ran before it finished would read nil.
	e.OnLoaded(func(ctx sdk.Context) {
		time.Sleep(300 * time.Millisecond)
		expanded, err := ctx.GetToolsExpanded()
		mu.Lock()
		loaded = map[string]any{"cwd": ctx.Cwd(), "mode": ctx.Mode(), "hostCall": err == nil, "expanded": expanded}
		mu.Unlock()
	})

	tool("c3_theme_arm", func(ctx sdk.Context, _ map[string]any) (any, error) {
		off, err := ctx.OnThemeChange(func(_ sdk.Context, theme sdk.UITheme) {
			mu.Lock()
			themeLog = append(themeLog, theme.Name+"|"+theme.Fg("accent", "x"))
			mu.Unlock()
		})
		mu.Lock()
		themeOff = off
		mu.Unlock()
		if err != nil {
			return nil, err
		}
		return text(map[string]any{"ok": true})
	})
	tool("c3_theme_disarm", func(sdk.Context, map[string]any) (any, error) {
		mu.Lock()
		off := themeOff
		mu.Unlock()
		off()
		return text(map[string]any{"ok": true})
	})
	tool("c3_theme_log", func(sdk.Context, map[string]any) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		return text(map[string]any{"log": themeLog})
	})
	tool("c3_expanded_arm", func(ctx sdk.Context, _ map[string]any) (any, error) {
		off, err := ctx.OnToolsExpandedChange(func(_ sdk.Context, expanded bool) {
			mu.Lock()
			expandedLog = append(expandedLog, expanded)
			mu.Unlock()
		})
		mu.Lock()
		expandedOff = off
		mu.Unlock()
		if err != nil {
			return nil, err
		}
		return text(map[string]any{"ok": true})
	})
	tool("c3_expanded_disarm", func(sdk.Context, map[string]any) (any, error) {
		mu.Lock()
		off := expandedOff
		mu.Unlock()
		off()
		return text(map[string]any{"ok": true})
	})
	tool("c3_expanded_log", func(sdk.Context, map[string]any) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		return text(map[string]any{"log": expandedLog})
	})
	tool("c3_widget_arm", func(ctx sdk.Context, args map[string]any) (any, error) {
		placement, _ := args["placement"].(string)
		err := ctx.SetWidgetRenderer("c3w", func(width int) []string {
			return []string{fmt.Sprintf("w=%d|%s", width, ctx.UITheme().Name)}
		}, sdk.WidgetOptions{"placement": placement})
		if err != nil {
			return nil, err
		}
		return text(map[string]any{"ok": true})
	})
	tool("c3_widget_clear", func(ctx sdk.Context, _ map[string]any) (any, error) {
		if err := ctx.SetWidgetRenderer("c3w", nil); err != nil {
			return nil, err
		}
		return text(map[string]any{"ok": true})
	})
	tool("c3_bell", func(ctx sdk.Context, _ map[string]any) (any, error) {
		return text(map[string]any{"error": fmt.Sprint(ctx.Bell())})
	})
	tool("c3_tool_call", func(ctx sdk.Context, _ map[string]any) (any, error) {
		var index any
		if i, ok := ctx.ToolCallIndex(); ok {
			index = i
		}
		var input any
		if raw := ctx.ToolInput(); raw != nil {
			input = string(raw)
		}
		keys := []string{}
		var meta any
		if params := ctx.ToolParams(); params != nil {
			keys = params.Keys()
			if value, ok := params.Get("meta"); ok {
				encoded, err := text(value)
				if err != nil {
					return nil, err
				}
				meta = encoded
			}
		}
		return text(map[string]any{"index": index, "input": input, "keys": keys, "meta": meta})
	})
	tool("c3_source", func(ctx sdk.Context, _ map[string]any) (any, error) {
		info, ok := ctx.ExtensionSourceInfo()
		if !ok {
			return text(map[string]any{"info": nil})
		}
		return text(map[string]any{"info": info})
	})
	tool("c3_loaded", func(sdk.Context, map[string]any) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		return text(map[string]any{"loaded": loaded, "factoryHeard": heardFactory, "heardAtEmit": heardAtEmit, "errorEmitFailed": errorEmitFailed, "crossOwnAt": crossOwnAt, "crossReturnedAt": crossReturnedAt, "ackHeard": ackHeard, "ackAtCross": ackAtCross})
	})
}
