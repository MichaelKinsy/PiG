// Command sdk-fixture is a minimal SDK-based extension for integration
// testing. Unlike testdata/fixture-ext/ (raw protocol), this uses the Go
// SDK to verify the SDK→Host wire format round-trip end-to-end.
//
// The wire format mismatch bug (schema vs parameters, []string vs
// []HandlerDecl) was only caught in production because no integration
// test exercised the real SDK path. This fixture exists to close that gap.
package testfixture

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"maps"
	"math"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf16"

	jsjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

// kitProbe is the component kit's conformance view (D107,
// docs/plan/extension-component-kit.md §10). It logs every HandleInput and
// HandleViewEvent call and closes on select with the log joined by ",".
type kitProbe struct {
	mu  sync.Mutex
	log []string
}

func (p *kitProbe) View(int) kit.View {
	text := kit.NewText("Kit probe", 2, 0)
	text.Bg = "customMessageBg"
	stack := kit.NewHStack(
		kit.Entry(kit.NewTruncatedText("left side", 0, 0), kit.StackEntryOptions{Grow: new(1)}),
		kit.Entry(kit.NewTruncatedText("right", 0, 0), kit.StackEntryOptions{Grow: new(1)}),
	)
	stack.Gap = 1
	items := make([]kit.SelectItem, 5)
	for i := range items {
		items[i] = kit.SelectItem{Value: fmt.Sprintf("k%d", i), Label: fmt.Sprintf("Track %d", i), Description: fmt.Sprintf("Artist %d", i)}
	}
	tracks := kit.NewSelectList("kit-tracks", items, 3)
	tracks.SetSelectedIndex(2)
	return kit.View{
		Root: kit.NewContainer(
			kit.NewDynamicBorder("accent"),
			text,
			kit.NewMarkdown("- one\n- **two**", 1, 0),
			stack,
			kit.NewSpacer(1),
			tracks,
		),
		Focus: "kit-tracks",
		Theme: map[string]string{"accent": "#d75f00"},
	}
}

func (p *kitProbe) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.log = append(p.log, "input:"+data)
	return sdk.RemoteComponentResult{}, nil
}

func (p *kitProbe) HandleViewEvent(event kit.Event) (sdk.RemoteComponentResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	value := ""
	if event.Item != nil {
		value = event.Item.Value
	}
	p.log = append(p.log, fmt.Sprintf("%s:%d:%s", event.Type, event.Index, value))
	if event.Type == kit.EventSelect {
		return sdk.RemoteComponentResult{Done: true, Value: strings.Join(p.log, ",")}, nil
	}
	return sdk.RemoteComponentResult{}, nil
}

// mouseProbe is the extension mouse row's component: it logs every
// mouse event it receives, all of its fields, and closes on a click with the
// log joined by ",".
type mouseProbe struct {
	log []string
}

func (*mouseProbe) Render(int) []string {
	return []string{"mouse probe", "row 1", "row 2", "row 3"}
}

func (*mouseProbe) HandleInput(string) (sdk.RemoteComponentResult, error) {
	return sdk.RemoteComponentResult{}, nil
}

func (p *mouseProbe) HandleMouse(event sdk.MouseEvent) (sdk.RemoteComponentResult, error) {
	var mods strings.Builder
	for _, mod := range []struct {
		on   bool
		name string
	}{{event.Shift, "S"}, {event.Alt, "A"}, {event.Ctrl, "C"}} {
		if mod.on {
			mods.WriteString(mod.name)
		}
	}
	p.log = append(p.log, fmt.Sprintf("%s/%s/%d,%d/%d,%d/%dx%d/w%d/c%d/%s", event.Type, event.Button, event.X, event.Y,
		event.ScreenX, event.ScreenY, event.Width, event.Height, event.WheelDelta, event.ClickCount, mods.String()))
	if event.Type == sdk.MouseClick {
		return sdk.RemoteComponentResult{Done: true, Value: strings.Join(p.log, ",")}, nil
	}
	return sdk.RemoteComponentResult{}, nil
}

// kitImagePNG is image n of kit-images: a 1×1 PNG whose pixel encodes n, so
// each n has its own bytes and ref.
func kitImagePNG(n int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.NRGBA{R: uint8(n), G: uint8(n >> 8), B: 0x5f, A: 0xff})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// kitKindsPNG is kit-kinds' image, a 1×1 PNG every SDK fixture embeds byte
// for byte.
const kitKindsPNG = "89504e470d0a1a0a0000000d49484452000000010000000108060000001f15c48900000010494441547801010500faff002a005fff026a01892888e8cd0000000049454e44ae426082"

type focusedList struct {
	items    []string
	selected int
	disposed bool
}

func (l *focusedList) Render(width int) []string {
	lines := []string{fmt.Sprintf("focused width=%d", width)}
	for i, item := range l.items {
		prefix := "  "
		if i == l.selected {
			prefix = "> "
		}
		lines = append(lines, prefix+item)
	}
	return lines
}

func (l *focusedList) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	if data == "j" {
		return sdk.RemoteComponentResult{Done: true, Value: make(chan int)}, nil
	}
	if data == "e" {
		return sdk.RemoteComponentResult{}, errors.New("focused input failed")
	}
	switch data {
	case "\x1b[A":
		l.selected = (l.selected - 1 + len(l.items)) % len(l.items)
	case "\x1b[B":
		l.selected = (l.selected + 1) % len(l.items)
	case "\x1b[6~":
		l.selected = min(l.selected+2, len(l.items)-1)
	case "\r", "\n":
		return sdk.RemoteComponentResult{Done: true, Value: l.items[l.selected]}, nil
	case "\x1b":
		return sdk.RemoteComponentResult{Done: true}, nil
	}
	return sdk.RemoteComponentResult{}, nil
}

func (l *focusedList) Dispose() { l.disposed = true }

// overlayWidthProbe renders the width the SDK draws it at and returns that width on Enter.
type overlayWidthProbe struct{ width int }

func (p *overlayWidthProbe) Render(width int) []string {
	p.width = width
	return []string{fmt.Sprintf("overlay width=%d", width)}
}

func (p *overlayWidthProbe) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	if data == "\r" {
		return sdk.RemoteComponentResult{Done: true, Value: p.width}, nil
	}
	return sdk.RemoteComponentResult{}, nil
}

type timerFocused struct {
	mu         sync.Mutex
	frame      int
	invalidate func()
	stop       chan struct{}
	done       chan struct{}
	stopOnce   sync.Once
	disposed   bool
}

func newTimerFocused() *timerFocused {
	component := &timerFocused{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(component.done)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				component.mu.Lock()
				component.frame++
				invalidate := component.invalidate
				component.mu.Unlock()
				if invalidate != nil {
					invalidate()
				}
			case <-component.stop:
				return
			}
		}
	}()
	return component
}

func (c *timerFocused) Render(width int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return []string{fmt.Sprintf("timer frame=%d width=%d", c.frame, width)}
}

func (c *timerFocused) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if data == "\r" {
		return sdk.RemoteComponentResult{Done: true, Value: c.frame}, nil
	}
	return sdk.RemoteComponentResult{}, nil
}

func (c *timerFocused) SetInvalidate(invalidate func()) {
	c.mu.Lock()
	c.invalidate = invalidate
	c.mu.Unlock()
}

func (c *timerFocused) Dispose() {
	c.stopOnce.Do(func() { close(c.stop) })
	<-c.done
	c.mu.Lock()
	c.disposed = true
	c.mu.Unlock()
}

func (c *timerFocused) Disposed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.disposed
}

func (c *timerFocused) Detached() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.invalidate == nil
}

func modelConformanceContext() map[string]any {
	return map[string]any{
		"systemPrompt": "conformance-system",
		"messages": []any{
			map[string]any{
				"role":      "system",
				"content":   []any{map[string]any{"type": "text", "text": "signed system", "textSignature": "system-signature"}},
				"sections":  json.RawMessage(`{"zeta":"last-first","alpha":null,"middle":"middle"}`),
				"timestamp": 41,
			},
			map[string]any{"role": "user", "content": "hello", "timestamp": 42},
			map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "prior", "textSignature": "signed"}}, "api": "openai-responses", "provider": "prior-provider", "model": "prior-model", "usage": map[string]any{"input": 1, "output": 2, "cacheRead": 3, "cacheWrite": 4, "totalTokens": 10, "cost": map[string]any{"input": 0.1, "output": 0.2, "cacheRead": 0.3, "cacheWrite": 0.4, "total": 1.0}}, "stopReason": "stop", "timestamp": 43},
		},
		"tools": []any{map[string]any{
			"name": "lookup", "description": "lookup", "parameters": map[string]any{"type": "object"},
			"constrainedSampling": map[string]any{"type": "grammar", "variants": map[string]any{"openai_lark": "start: NUMBER"}},
		}},
	}
}

func modelConformanceOptions() map[string]any {
	return map[string]any{
		"timeoutMs": 0, "websocketConnectTimeoutMs": 1234, "maxRetries": 2, "maxRetryDelayMs": 3000,
		"maxTokens": 321, "temperature": 0.65, "samplingParams": map[string]any{"topP": 0.8},
		"thinkingBudgets": map[string]any{"minimal": 11, "low": 22, "medium": 33, "high": 44}, "reasoning": "high", "isReasoning": true,
		"env": map[string]any{"WIRE_ENV": "request-value", "SECOND_ENV": "distinct-value"}, "headers": map[string]any{"X-Wire": "yes", "X-Remove": nil}, "sessionId": "conformance-session", "transport": "sse",
	}
}

// Extension returns the canonical Go SDK fixture shared by subprocess and
// fused conformance harnesses.
func Extension() *sdk.Extension {
	ext := sdk.New("sdk-fixture")
	// factoryAgentDir is getAgentDir() while the factory runs, where Pi's extensions call it (TestConformance_HostConfigurationAgreesAcrossSDKs).
	factoryAgentDir, factoryAgentDirErr := sdk.AgentDir()
	if factoryAgentDirErr != nil {
		factoryAgentDir = "error: " + factoryAgentDirErr.Error()
	}
	registerAutocompleteFixture(ext)
	registerPiAiHelpersFixture(ext)
	registerHostPathsFixture(ext)
	registerHostHelpersFixture(ext)
	schemaRejected := RejectInvalidToolSchema(ext)
	ext.Command("schema-probe", "Report schema rejection", func(ctx sdk.Context, _ string) error {
		ctx.Notify(fmt.Sprintf("schema-rejected:%t", schemaRejected), "info")
		return nil
	})
	ext.Flag("flag-true", sdk.FlagOptions{Type: sdk.FlagBoolean, Default: true})
	ext.Flag("flag-false", sdk.FlagOptions{Type: sdk.FlagBoolean, Default: false})
	ext.Flag("flag-string", sdk.FlagOptions{Type: sdk.FlagString, Default: "default"})
	ext.Flag("flag-empty", sdk.FlagOptions{Type: sdk.FlagString, Default: ""})
	ext.Flag("flag-unset", sdk.FlagOptions{Type: sdk.FlagString})
	ext.Command("flag-probe", "Report registered flag values", func(ctx sdk.Context, _ string) error {
		values := []any{}
		for _, name := range []string{"flag-true", "flag-false", "flag-string", "flag-empty", "flag-unset", "unregistered"} {
			value, err := ctx.GetFlag(name)
			if err != nil {
				return err
			}
			values = append(values, value)
		}
		wire, err := json.Marshal(values)
		if err != nil {
			return err
		}
		ctx.Notify(string(wire), "info")
		return nil
	})
	ext.Command("timeout-probe", "Send JavaScript-number timeouts", func(ctx sdk.Context, _ string) error {
		for _, ms := range []float64{0.5, 4294967296.5, 1e21} {
			if _, err := ctx.ExecWithOptions("timeout-command", nil, sdk.ExecOptions{Timeout: ms}); err != nil {
				return err
			}
		}
		_, _, err := ctx.SelectWithOptions("timeout", []string{"a"}, sdk.DialogOptions{Timeout: 1500.5})
		return err
	})
	ext.Command("exec-reject-probe", "Report exec outcomes", func(ctx sdk.Context, args string) error {
		var commands []string
		if err := json.Unmarshal([]byte(args), &commands); err != nil {
			return err
		}
		for _, command := range commands {
			result, err := ctx.Exec(command, nil)
			switch {
			case err != nil:
				ctx.Notify("rejected:"+err.Error(), "info")
			case result == nil:
				ctx.Notify("resolved:none", "info")
			default:
				ctx.Notify("resolved:"+strconv.Itoa(result.ExitCode), "info")
			}
		}
		return nil
	})
	ext.Command("exec-abort-probe", "Abort a running exec through its signal", func(ctx sdk.Context, args string) error {
		var probe struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
			PidFile string   `json:"pidFile"`
			Pre     bool     `json:"pre"`
		}
		if err := json.Unmarshal([]byte(args), &probe); err != nil {
			return err
		}
		signal, abort := context.WithCancel(context.Background())
		defer abort()
		if probe.Pre {
			abort()
		} else {
			go func() {
				for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
					if _, err := os.Stat(probe.PidFile); err == nil {
						break
					}
				}
				abort()
			}()
		}
		result, err := ctx.ExecWithOptions(probe.Command, probe.Args, sdk.ExecOptions{Signal: signal})
		if err != nil {
			ctx.Notify("rejected:"+err.Error(), "info")
			return nil
		}
		ctx.Notify(fmt.Sprintf("exec-abort:killed=%t;code=%d;stdout=%s", result.Killed, result.ExitCode, strings.TrimSpace(result.Stdout)), "info")
		return nil
	})
	ext.Command("thinking-model", "Read the thinking level and switch the model", func(ctx sdk.Context, _ string) error {
		level, err := ctx.GetThinkingLevel()
		if err != nil {
			return err
		}
		ctx.SetThinkingLevel("high")
		ok, err := ctx.SetModel("probe/model")
		if err != nil {
			return err
		}
		data, err := json.Marshal([]any{level, ok})
		if err != nil {
			return err
		}
		ctx.Notify(string(data), "info")
		return nil
	})
	ext.Command("replicated-accessors", "Read the replicated accessors after each write", replicatedAccessorsProbe)
	ext.Command("active_tools_set", "Set the active tools to the comma-separated names", func(ctx sdk.Context, args string) error {
		ctx.SetActiveTools(strings.Split(strings.TrimSpace(args), ","))
		return nil
	})
	// session_actions_unbound drives the session actions of a command context the host never bound; Pi answers { cancelled: false } and a no-op reload (runner.ts:383-387).
	ext.Command("session_actions_unbound", "Report what unbound session actions answer", func(ctx sdk.Context, _ string) error {
		var parts []string
		report := func(name string, result sdk.CancelledResult, err error) {
			if err != nil {
				parts = append(parts, name+"=error:"+err.Error())
				return
			}
			parts = append(parts, fmt.Sprintf("%s=%t", name, result.Cancelled))
		}
		result, err := ctx.NewSession(nil)
		report("new", result, err)
		result, err = ctx.Fork("entry", nil)
		report("fork", result, err)
		result, err = ctx.NavigateTree("entry", nil)
		report("navigate", result, err)
		result, err = ctx.SwitchSession("/s.jsonl", nil)
		report("switch", result, err)
		if err := ctx.Reload(); err != nil {
			parts = append(parts, "reload=error:"+err.Error())
		} else {
			parts = append(parts, "reload=ok")
		}
		ctx.Notify("unbound:"+strings.Join(parts, ","), "info")
		return nil
	})
	ext.Command("active_tools_get", "Report the active tools", func(ctx sdk.Context, _ string) error {
		names, err := ctx.GetActiveTools()
		if err != nil {
			return err
		}
		ctx.Notify("active_tools:"+strings.Join(names, ","), "info")
		return nil
	})
	// provider_probe_register and provider_probe_unregister register and unregister a plain provider after the extension connected.
	ext.Command("provider_probe_register", "Register a provider with one model", func(ctx sdk.Context, _ string) error {
		var config sdk.ProviderConfig
		if err := json.Unmarshal([]byte(`{"baseUrl":"https://probe.invalid/v1","api":"openai-completions","apiKey":"probe-key","models":[{"id":"probe-model","name":"Probe Model","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":1000,"maxTokens":100}]}`), &config); err != nil {
			return err
		}
		ext.RegisterProvider("conformance-probe", config)
		return nil
	})
	ext.Command("provider_probe_unregister", "Unregister the provider", func(ctx sdk.Context, _ string) error {
		ext.UnregisterProvider("conformance-probe")
		return nil
	})
	ext.Command("commands-probe", "Read the host's slash commands", func(ctx sdk.Context, _ string) error {
		commands, err := ctx.GetCommands()
		if err != nil {
			return err
		}
		listed := [][]string{}
		for _, c := range commands {
			if c.Name == "conformance-listed" {
				listed = append(listed, []string{c.Name, c.Source, c.Description})
			}
		}
		data, err := json.Marshal(listed)
		if err != nil {
			return err
		}
		ctx.Notify(string(data), "info")
		return nil
	})
	ext.Command("session-identity", "Read context identity accessors", func(ctx sdk.Context, _ string) error {
		id, err := ctx.GetSessionID()
		if err != nil {
			return err
		}
		file, err := ctx.GetSessionFile()
		if err != nil {
			return err
		}
		leaf, err := ctx.GetLeafID()
		if err != nil {
			return err
		}
		name, err := ctx.GetSessionName()
		if err != nil {
			return err
		}
		data, err := json.Marshal([]*string{&id, file, leaf, name})
		if err != nil {
			return err
		}
		ctx.Notify(string(data), "info")
		return nil
	})
	ext.Command("registry-session", "Read registry and session facades", registrySessionProbe)
	ext.Command("registry-edges", "Read the registry answers that depend on the provider state", registryEdgesProbe)
	ext.Command("registry-unbound-reads", "Report find and a session read against a host that cannot answer", registryUnboundReadsProbe)
	ext.MessageRenderer("conformance-message", func(_ sdk.Context, message map[string]any, options sdk.MessageRenderOptions, width int) ([]string, error) {
		if message["content"] == "padding-options" {
			wire, err := json.Marshal(options)
			return []string{string(wire)}, err
		}
		return []string{fmt.Sprintf("renderer:%v:expanded=%t:width=%d", message["content"], options.Expanded, width)}, nil
	})
	ext.MessageViewRenderer("kit-message", func(sdk.Context, map[string]any, sdk.MessageRenderOptions, int) (kit.View, error) {
		return (&kitProbe{}).View(0), nil
	})
	ext.EntryViewRenderer("kit-entry", func(sdk.Context, map[string]any, sdk.EntryRenderOptions, int) (kit.View, error) {
		return (&kitProbe{}).View(0), nil
	})
	ext.EntryRenderer("conformance-entry", func(_ sdk.Context, entry map[string]any, options sdk.EntryRenderOptions, width int) ([]string, error) {
		return []string{fmt.Sprintf("entryrenderer:%v:expanded=%t:width=%d", entry["data"], options.Expanded, width)}, nil
	})
	ext.MarkdownTransformer(func(markdown string, context sdk.MarkdownTransformContext) string {
		return fmt.Sprintf("md:%s:%s:streaming=%t:width=%d", markdown, context.MessageType, context.IsStreaming, context.AvailableWidth)
	})
	resolvedLine := func(prefix, tool string) sdk.ToolRenderCallFunc {
		return func(_ sdk.Context, args map[string]any, _ sdk.ToolRenderContext, _ int) ([]string, error) {
			return []string{fmt.Sprintf("%s:%s:%v", prefix, tool, args["q"])}, nil
		}
	}
	ext.ToolRenderer(func(tool string, next func() *sdk.ToolRenderers) *sdk.ToolRenderers {
		switch tool {
		case "conformance_tool_renderer":
			return &sdk.ToolRenderers{Call: resolvedLine("resolved", tool)}
		case "kit_resolved_tool":
			// The resolver draws a call and its result as the kit probe (D107).
			var renderers sdk.ToolRenderers
			renderers.CallView = func(sdk.Context, map[string]any, sdk.ToolRenderContext, int) (kit.View, error) {
				return (&kitProbe{}).View(0), nil
			}
			renderers.ResultView = func(sdk.Context, sdk.ToolRenderResult, sdk.ToolRenderResultOptions, sdk.ToolRenderContext, int) (kit.View, error) {
				return (&kitProbe{}).View(0), nil
			}
			return &renderers
		case "conformance_no_renderer":
			return nil
		case "conformance_fill":
			if renderers := next(); renderers != nil {
				return renderers
			}
			return &sdk.ToolRenderers{Call: resolvedLine("filled", tool)}
		case "conformance_wrap":
			wrapped := next()
			wrapped.Call = resolvedLine("wrapped", tool)
			return wrapped
		}
		return next()
	})
	ext.Command("late_tool_renderer", "Register a tool renderer resolver after loading", func(sdk.Context, string) error {
		ext.ToolRenderer(func(tool string, next func() *sdk.ToolRenderers) *sdk.ToolRenderers {
			if tool != "conformance_late" {
				return next()
			}
			return &sdk.ToolRenderers{Call: resolvedLine("late", tool)}
		})
		return nil
	})
	ext.Tool("render_probe", "Render its own tool card", sdk.Schema{"type": "object", "properties": map[string]any{}}, func(sdk.Context, map[string]any) (any, error) {
		return "render ok", nil
	})
	ext.SetToolRenderers("render_probe", sdk.ToolRenderers{
		Shell: sdk.ToolRenderShellSelf,
		Call: func(_ sdk.Context, args map[string]any, render sdk.ToolRenderContext, width int) ([]string, error) {
			calls, _ := render.State["calls"].(int)
			render.State["calls"] = calls + 1
			return []string{fmt.Sprintf("toolrender:call:%v:partial=%t:calls=%d:width=%d", args["topic"], render.IsPartial, calls+1, width)}, nil
		},
		Result: func(_ sdk.Context, result sdk.ToolRenderResult, options sdk.ToolRenderResultOptions, render sdk.ToolRenderContext, width int) ([]string, error) {
			details, _ := result.Details.(map[string]any)
			duration := "none"
			if render.DurationMs != nil {
				duration = fmt.Sprint(*render.DurationMs)
			}
			return []string{fmt.Sprintf("toolrender:result:%v:%v:expanded=%t:calls=%v:width=%d:duration=%s", result.Content[0]["text"], details["k"], options.Expanded, render.State["calls"], width, duration)}, nil
		},
	})

	ext.Tool("echo", "Echo back the input", sdk.Schema{
		"type":     "object",
		"required": []string{"text"},
		"properties": map[string]any{
			"offset": map[string]any{"type": "number"},
			"text": map[string]any{
				"type":        "string",
				"description": "Text to echo",
			},
		},
	}, func(ctx sdk.Context, params map[string]any) (any, error) {
		text, _ := params["text"].(string)
		return map[string]string{"content": "echo: " + text}, nil
	})

	ext.Tool("update_tool", "Stream two partial results", sdk.Schema{"type": "object", "properties": map[string]any{}}, func(ctx sdk.Context, _ map[string]any) (any, error) {
		if err := ctx.OnUpdate("step 1"); err != nil {
			return nil, err
		}
		if err := ctx.OnUpdate(sdk.ToolResult{Content: "step 2"}); err != nil {
			return nil, err
		}
		return "done", nil
	})

	ext.Tool("big_out", "Return text of the size in the argument", sdk.Schema{"type": "object", "properties": map[string]any{}}, func(ctx sdk.Context, params map[string]any) (any, error) {
		n, _ := params["n"].(float64)
		return sdk.ToolResult{Content: strings.Repeat("a", int(n))}, nil
	})
	// echo_params returns the arguments it received as its details (TestConformance_ToolArgumentNumbersAgreeAcrossSDKs).
	ext.Tool("echo_params", "Return the arguments as the result details", sdk.Schema{"type": "object", "properties": map[string]any{}}, func(ctx sdk.Context, params map[string]any) (any, error) {
		return sdk.ToolResult{Content: "echoed", Details: params}, nil
	})
	ext.Tool("nan_out", "Return details holding NaN and Infinity", sdk.Schema{"type": "object", "properties": map[string]any{}}, func(ctx sdk.Context, params map[string]any) (any, error) {
		return sdk.ToolResult{Content: "nan", Details: map[string]any{"n": math.NaN(), "i": math.Inf(-1), "l": []any{math.NaN(), 2, map[string]any{"d": math.Inf(1)}}, "ok": 1}}, nil
	})
	ext.Tool("ordered_details", "Return details whose members are not in alphabetical order", sdk.Schema{"type": "object", "properties": map[string]any{}}, func(ctx sdk.Context, _ map[string]any) (any, error) {
		details := json.RawMessage(`{"zeta":1,"alpha":{"yy":2,"bb":3},"mid":[{"qq":1,"aa":2}]}`)
		if err := ctx.OnUpdate(sdk.ToolResult{Content: "partial", Details: details}); err != nil {
			return nil, err
		}
		return sdk.ToolResult{Content: "done", Details: details}, nil
	})

	// The Go SDK writes a result from a struct, which has one member order (content, details, isError): not a row of the member-order test, but the same tool set as every other fixture.
	ext.Tool("ordered_result", "Return a result whose members are not in the declared order", sdk.Schema{"type": "object", "properties": map[string]any{}}, func(ctx sdk.Context, _ map[string]any) (any, error) {
		if err := ctx.OnUpdate(sdk.ToolResult{Content: "partial", Details: map[string]any{"k": 1}}); err != nil {
			return nil, err
		}
		return sdk.ToolResult{Content: "done", Details: map[string]any{"k": 1}, IsError: true}, nil
	})

	var abortObserved atomic.Bool
	var abortReason atomic.Value
	abortReason.Store("")
	ext.Tool("abort_tool", "Wait for the abort signal", sdk.Schema{"type": "object", "properties": map[string]any{}}, func(ctx sdk.Context, _ map[string]any) (any, error) {
		if err := ctx.OnUpdate("waiting"); err != nil {
			return nil, err
		}
		<-ctx.Done()
		abortReason.Store(ctx.CancellationReason())
		abortObserved.Store(true)
		return "aborted", nil
	})
	// hang_tool ignores its abort signal for longer than the host's abort grace period (D111).
	ext.Tool("hang_tool", "Ignore the abort signal", sdk.Schema{"type": "object", "properties": map[string]any{}}, func(ctx sdk.Context, _ map[string]any) (any, error) {
		if err := ctx.OnUpdate("waiting"); err != nil {
			return nil, err
		}
		time.Sleep(8 * time.Second)
		return "late", nil
	})
	ext.Command("abort_probe", "Report whether abort_tool saw its abort signal", func(ctx sdk.Context, args string) error {
		ctx.Notify(fmt.Sprintf("abort:%t", abortObserved.Load()), "info")
		return nil
	})
	ext.Command("abort_reason", "Report the cancellation reason abort_tool saw", func(ctx sdk.Context, args string) error {
		ctx.Notify("reason:"+abortReason.Load().(string), "info")
		return nil
	})

	ext.Tool("rich_tool", "Return text, image, and terminate", sdk.Schema{"type": "object", "properties": map[string]any{}}, func(sdk.Context, map[string]any) (any, error) {
		return map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "  padded  "},
				{"type": "image", "data": "aW1n", "mimeType": "image/png"},
				{"type": "text", "text": "tail\n"},
			},
			"terminate": true,
		}, nil
	})

	ext.ToolWithPrepareArguments("prepared_tool", "Transform legacy arguments before execution", sdk.Schema{
		"type": "object", "required": []string{"text"}, "properties": map[string]any{"text": map[string]any{"type": "string"}},
	}, func(params map[string]any) (map[string]any, error) {
		return map[string]any{"text": params["legacy"]}, nil
	}, func(_ sdk.Context, params map[string]any) (any, error) {
		text, _ := params["text"].(string)
		return map[string]string{"content": "prepared:" + text}, nil
	})

	// Reports the order in which calls start: the number of calls that started before it, plus its own argument.
	var startedCalls atomic.Int64
	ext.Tool("start_order", "Report the order in which calls start", sdk.Schema{"type": "object", "properties": map[string]any{"n": map[string]any{"type": "number"}}}, func(_ sdk.Context, params map[string]any) (any, error) {
		number := startedCalls.Add(1)
		return map[string]string{"content": fmt.Sprintf("start#%d n=%v", number, params["n"])}, nil
	})

	// Opens a select dialog as its first act, after an exec when the call asks for one, and returns the answer: a batch of these shows the order the SDK opens the dialogs in and resumes the calls in.
	ext.Tool("dialog_order", "Open a select dialog titled by the call and report the answer", sdk.Schema{"type": "object", "properties": map[string]any{"title": map[string]any{"type": "string"}, "exec": map[string]any{"type": "boolean"}}}, func(ctx sdk.Context, params map[string]any) (any, error) {
		if params["exec"] == true {
			_, _ = ctx.Exec("dialog-order-exec", nil)
		}
		selected, _, err := ctx.Select(fmt.Sprint(params["title"]), []string{"a", "b"})
		if err != nil {
			return nil, err
		}
		return map[string]string{"content": fmt.Sprintf("dialog:%v=%s", params["title"], selected)}, nil
	})

	// A tool that notifies inline and, from a goroutine timer_release releases, after its handler returned (D189).
	timerGate := make(chan struct{}, 16)
	var staleMu sync.Mutex
	var stale *sdk.Context
	ext.Tool("timer_defer", "Notify inline and from deferred work", sdk.Schema{"type": "object"}, func(ctx sdk.Context, _ map[string]any) (any, error) {
		staleMu.Lock()
		stale = &ctx
		staleMu.Unlock()
		ctx.Notify("inline:sync", "info")
		go func() {
			<-timerGate
			ctx.Notify("deferred:goroutine", "info")
		}()
		return map[string]string{"content": "queued"}, nil
	})
	// timer_stale awaits a host call it makes through the context of the earlier timer_defer request, which Pi-port extensions keep (an activeCtx), and returns once it was applied. The call is not detached work: the handler waits for it, so the host must not hold it for a loop turn that waits for the handler.
	ext.Tool("timer_stale", "Await a host call made through an earlier request's context", sdk.Schema{"type": "object"}, func(sdk.Context, map[string]any) (any, error) {
		staleMu.Lock()
		earlier := stale
		staleMu.Unlock()
		if earlier == nil {
			return nil, fmt.Errorf("timer_defer has not run")
		}
		earlier.Notify("stale:awaited", "info")
		return map[string]string{"content": "applied"}, nil
	})
	ext.Tool("timer_release", "Release the deferred work", sdk.Schema{"type": "object"}, func(sdk.Context, map[string]any) (any, error) {
		timerGate <- struct{}{}
		return map[string]string{"content": "released"}, nil
	})

	// order_probe makes the host calls and updates whose order the ordering rows pin (ordering_test.go): its first acts are two synchronous host calls, then one update, and the tool_result handler below reports each result as its first act.
	ext.Tool("order_probe", "Make a host call and an update in the first tick", sdk.Schema{"type": "object", "properties": map[string]any{"n": map[string]any{"type": "number"}}}, func(ctx sdk.Context, params map[string]any) (any, error) {
		ctx.Notify(fmt.Sprintf("order-call=%v", params["n"]), "info")
		ctx.Notify(fmt.Sprintf("order-then=%v", params["n"]), "info")
		if err := ctx.OnUpdate(sdk.ToolResult{Content: fmt.Sprintf("order-update=%v", params["n"])}); err != nil {
			return nil, err
		}
		return map[string]string{"content": fmt.Sprintf("order-done n=%v", params["n"])}, nil
	})

	// mutation_order_probe takes the file mutation queue in its first statement and appends its tag to the file (TestFileMutationOrderAcrossSDKs): Pi registers it in the queue
	// before the edit or write call that follows it in a batch.
	ext.Tool("mutation_order_probe", "Take the file mutation queue in the first statement and append a tag to the file", sdk.Schema{"type": "object", "properties": map[string]any{"file": map[string]any{"type": "string"}, "tag": map[string]any{"type": "string"}}}, func(ctx sdk.Context, params map[string]any) (any, error) {
		file, _ := params["file"].(string)
		tag, _ := params["tag"].(string)
		_, err := sdk.WithFileMutationQueue(file, func() (struct{}, error) {
			before, _ := os.ReadFile(file)
			return struct{}{}, os.WriteFile(file, append(before, tag+"\n"...), 0o600)
		})
		if err != nil {
			return nil, err
		}
		return map[string]string{"content": "mutated " + tag}, nil
	})

	ext.Tool("throw_probe", "Throw a value of the requested kind", sdk.Schema{"type": "object", "properties": map[string]any{"kind": map[string]any{"type": "string"}}}, func(ctx sdk.Context, params map[string]any) (any, error) {
		switch params["kind"] {
		case "unicode":
			return nil, fmt.Errorf("é😀 \n x")
		case "empty":
			return nil, fmt.Errorf("")
		case "long":
			return nil, fmt.Errorf("%s", strings.Repeat("x", 100000))
		}
		return map[string]string{"content": "ok"}, nil
	})

	ext.Tool("tool_error", "Return a thrown tool error", sdk.Schema{"type": "object"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		return nil, fmt.Errorf("tool exploded")
	})

	ext.Tool("tool_is_error", "Return a structured tool error result", sdk.Schema{"type": "object"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		return map[string]any{"content": "soft tool error", "is_error": true}, nil
	})

	ext.ToolWithGuidelines("guided_tool", "Tool with prompt guidelines", sdk.Schema{"type": "object"},
		[]string{"Use guided_tool when the user asks for guided behavior."},
		func(ctx sdk.Context, params map[string]any) (any, error) {
			return map[string]string{"content": "guided"}, nil
		})

	ext.ToolPromptSnippet("guided_tool", " \ufeffGuided\r\n tool\t summary ")

	ext.ToolWithSource("sourced_tool", "Tool with explicit source", sdk.Schema{"type": "object"},
		"mcp:test-server",
		[]string{"Use sourced_tool to test per-tool source attribution."},
		func(ctx sdk.Context, params map[string]any) (any, error) {
			return map[string]string{"content": "sourced"}, nil
		})

	ext.RegisterTool(sdk.ToolDefinition{Name: "sampling_disabled", Label: "sampling_disabled", Description: "Disable constrained sampling", Parameters: sdk.Schema{"type": "object"}, ConstrainedSampling: sdk.DisabledConstrainedSampling{}, Execute: func(sdk.Context, map[string]any) (any, error) { return "disabled", nil }})
	ext.ToolWithConstrainedSampling("grammar_tool", "Tool with a grammar constrained sampling request", sdk.Schema{"type": "object"},
		sdk.ConstrainedSampling{Type: "grammar", Variants: map[string]string{"openai_lark": "start: NUMBER"}},
		func(ctx sdk.Context, params map[string]any) (any, error) {
			return map[string]string{"content": "grammar"}, nil
		})

	ext.RegisterCommand("complete_probe", sdk.CommandOptions{
		Description: "Complete its arguments",
		GetArgumentCompletions: func(prefix string) ([]sdk.AutocompleteItem, error) {
			var items []sdk.AutocompleteItem
			for _, item := range []sdk.AutocompleteItem{{Value: "alpha", Label: "alpha — first"}, {Value: "apple", Description: "fruit"}, {Value: "beta"}} {
				if strings.HasPrefix(item.Value, strings.TrimSpace(prefix)) {
					items = append(items, item)
				}
			}
			return items, nil
		},
		Handler: func(sdk.Context, string) error { return nil },
	})
	ext.Command("ping", "Respond with pong", func(ctx sdk.Context, args string) error {
		ctx.Notify("pong", "info")
		return nil
	})

	ext.Command("liveness_host_call", "Exercise an awaited host call", func(ctx sdk.Context, _ string) error {
		return ctx.WaitForIdle()
	})
	ext.Command("liveness_user_call", "Exercise an interactive host call", func(ctx sdk.Context, _ string) error {
		_, _, err := ctx.Input("Question", "Answer")
		return err
	})
	ext.Command("liveness_fire_call", "Exercise a no-result UI host call", func(ctx sdk.Context, _ string) error {
		ctx.SetTitle("Conformance title")
		return nil
	})

	ext.Command("command_error", "Return a command error", func(ctx sdk.Context, args string) error {
		return fmt.Errorf("command exploded")
	})

	ext.Command("command_awaited_error", "Return an error after awaited work", func(ctx sdk.Context, args string) error {
		time.Sleep(150 * time.Millisecond)
		return fmt.Errorf("awaited command exploded")
	})

	var termUnsub func()
	ext.Command("term_subscribe", "Subscribe to raw terminal input", func(ctx sdk.Context, args string) error {
		unsub, err := ctx.OnTerminalInput(func(data string) sdk.TerminalInputResult {
			switch data {
			case "\xed\xa0\xbd", "\xed\xb8\x80", "😀":
				rewritten := "seen:" + data
				return sdk.TerminalInputResult{Data: &rewritten}
			case "\x1b[96~":
				text, textErr := ctx.GetEditorText()
				expanded, expandedErr := ctx.GetToolsExpanded()
				if textErr != nil || expandedErr != nil {
					text, expanded = "error", false
				}
				encoded, _ := jsjson.Marshal([]any{text, expanded})
				state := string(encoded)
				return sdk.TerminalInputResult{Data: &state}
			case "\x1b[98~":
				rewritten := "rewritten"
				return sdk.TerminalInputResult{Data: &rewritten}
			case "\x1b[97~":
				time.Sleep(200 * time.Millisecond)
				return sdk.TerminalInputResult{Consume: true}
			}
			return sdk.TerminalInputResult{Consume: data == "\x1b[99~"}
		})
		if err != nil {
			return err
		}
		termUnsub = unsub
		return nil
	})
	ext.Command("term_unsubscribe", "Release the raw input subscription", func(ctx sdk.Context, args string) error {
		if termUnsub != nil {
			termUnsub()
			termUnsub = nil
		}
		return nil
	})
	// pi.on called after the extension connected returns an unsubscribe (conformance TestConformance_EventUnsubscribe): the handler
	// reports the event's own message entry id, which no SDK fallback produces.
	var eventProbeOff func()
	ext.Command("event_probe_subscribe", "Subscribe to turn_end after connecting", func(ctx sdk.Context, args string) error {
		eventProbeOff = ext.OnEvent("turn_end", func(ctx sdk.Context, data map[string]any) (any, error) {
			ctx.Notify(fmt.Sprintf("event_probe:%v", data["messageEntryId"]), "info")
			return nil, nil
		})
		return nil
	})
	ext.Command("event_probe_unsubscribe", "Remove the turn_end handler registered after connecting", func(ctx sdk.Context, args string) error {
		if eventProbeOff != nil {
			eventProbeOff()
			eventProbeOff = nil
		}
		return nil
	})
	ext.Command("report_geometry", "Report observed terminal geometry", func(ctx sdk.Context, args string) error {
		ctx.Notify(fmt.Sprintf("geometry:%dx%d", ctx.Width(), ctx.Height()), "info")
		return nil
	})
	// A width handler makes a host call the way any other handler does; the host's reply must reach it (conformance TestConformance_WidthHandlerHostCall).
	var widthProbeUnsub func()
	ext.Command("arm_width_probe", "Notify from a width handler", func(ctx sdk.Context, args string) error {
		if widthProbeUnsub != nil {
			return nil
		}
		unsub, err := ctx.OnWidthChange(func(c sdk.Context, width int) {
			c.Notify(fmt.Sprintf("width-probe:%d", width), "info")
			c.Notify(fmt.Sprintf("width-probe-returned:%d", width), "info")
		})
		widthProbeUnsub = unsub
		return err
	})
	ext.Command("surface_footer", "Install a footer renderer", func(ctx sdk.Context, args string) error {
		return ctx.SetFooterRenderer(func(width int) []string { return []string{fmt.Sprintf("footer@%d", width)} })
	})
	ext.Command("surface_header", "Install a header renderer", func(ctx sdk.Context, args string) error {
		return ctx.SetHeaderRenderer(func(width int) []string { return []string{fmt.Sprintf("header@%d", width)} })
	})
	ext.Command("surface_static_footer", "Push static footer rows", func(ctx sdk.Context, args string) error {
		return ctx.SetFooter([]string{fmt.Sprintf("static@%d", ctx.Width())})
	})
	ext.Command("surface_static_header", "Push static header rows", func(ctx sdk.Context, args string) error {
		return ctx.SetHeader([]string{fmt.Sprintf("static@%d", ctx.Width())})
	})
	ext.Command("surface_widget", "Set a string list widget wider than the pane", func(ctx sdk.Context, args string) error {
		return ctx.SetWidget("wide", []string{strings.Repeat("A", 60) + " tail", "short"})
	})
	ext.Command("status", "Set a status entry", func(ctx sdk.Context, args string) error {
		ctx.SetStatus("conformance", "ok")
		return nil
	})

	ext.Command("status_burst", "Set one status repeatedly without awaiting", func(ctx sdk.Context, args string) error {
		for i := range 200 {
			ctx.SetStatus("burst", strconv.Itoa(i))
		}
		return nil
	})

	ext.Command("send_message", "Send a custom message", func(ctx sdk.Context, args string) error {
		trigger := true
		return ctx.SendMessage("notice", "hello-custom", true, sdk.SendMessageOptions{TriggerTurn: &trigger, DeliverAs: "steer"})
	})
	ext.Command("send_message_default", "Send a custom message with default options", func(ctx sdk.Context, args string) error {
		return ctx.SendMessage("notice", "default", true, sdk.SendMessageOptions{})
	})
	ext.Command("send_message_no_turn", "Send a custom message that never starts a turn", func(ctx sdk.Context, args string) error {
		trigger := false
		return ctx.SendMessage("notice", "no-turn", true, sdk.SendMessageOptions{TriggerTurn: &trigger})
	})

	ext.Command("send_user_message", "Send a user message", func(ctx sdk.Context, args string) error {
		var content any = "hello-user"
		if args != "" {
			if err := json.Unmarshal([]byte(args), &content); err != nil {
				return err
			}
		}
		return ctx.SendUserMessage(content, "followUp")
	})

	ext.Command("set_session_name", "Set the session name", func(ctx sdk.Context, args string) error {
		return ctx.SetSessionName("conformance-session")
	})

	ext.Command("event_field_probe", "Report a field of the events named by the argument `<event> <field>`", func(ctx sdk.Context, args string) error {
		name, field, _ := strings.Cut(strings.TrimSpace(args), " ")
		ext.OnEvent(name, func(hctx sdk.Context, data map[string]any) (any, error) {
			reported := "absent"
			if value, ok := data[field]; ok {
				encoded, err := json.Marshal(value)
				if err != nil {
					return nil, err
				}
				reported = string(encoded)
			}
			hctx.Notify("event_field:"+name+"."+field+"="+reported, "info")
			return nil, nil
		})
		return nil
	})
	ext.Command("host_state_probe", "Report the thinking level and the session commands", func(ctx sdk.Context, _ string) error {
		level, err := ctx.GetThinkingLevel()
		if err != nil {
			return err
		}
		commands, err := ctx.GetCommands()
		if err != nil {
			return err
		}
		parts := make([]string, 0, len(commands))
		for _, c := range commands {
			parts = append(parts, strings.Join([]string{c.Name, c.Description, c.Source, c.SourceInfo.Path, c.SourceInfo.Scope}, "|"))
		}
		ctx.Notify("getThinkingLevel="+level+";getCommands="+strings.Join(parts, ","), "info")
		return nil
	})

	// rw_prompt_state registers two tools and activates one, reading the system prompt state after each write. Pi's getters read the Session, so every read answers the writes before it.
	ext.Command("rw_prompt_state", "Read the system prompt state after registering and activating tools", func(ctx sdk.Context, _ string) error {
		tool := func(name string) sdk.ToolDefinition {
			return sdk.ToolDefinition{Name: name, Label: name, Description: name, PromptSnippet: name + "-snippet", Parameters: sdk.Schema{"type": "object"}, Execute: func(sdk.Context, map[string]any) (any, error) { return "ok", nil }}
		}
		ctx.RegisterTool(tool("rw-alpha"))
		ctx.RegisterTool(tool("rw-beta"))
		registered, err := ctx.GetSystemPromptOptions()
		if err != nil {
			return err
		}
		ctx.SetActiveTools([]string{"rw-beta"})
		options, err := ctx.GetSystemPromptOptions()
		if err != nil {
			return err
		}
		prompt, err := ctx.GetSystemPrompt()
		if err != nil {
			return err
		}
		ctx.Notify(fmt.Sprintf("rw_prompt_state=registered:%s;selected:%s;beta:%t;alpha:%t", registered.ToolSnippets["rw-alpha"], strings.Join(options.SelectedTools, ","), strings.Contains(prompt, "rw-beta-snippet"), strings.Contains(prompt, "rw-alpha-snippet")), "info")
		return nil
	})

	// rw_idle_after_trigger sends a message that starts a run and reads isIdle at once; rw_idle_in_settled does the same, once, from an agent_settled handler, where the Session holds the message back until the handlers return.
	ext.Command("rw_idle_after_trigger", "Read isIdle after a message that starts a run", func(ctx sdk.Context, _ string) error {
		trigger := true
		if err := ctx.SendMessage("rw-trigger", "rw-trigger", true, sdk.SendMessageOptions{TriggerTurn: &trigger}); err != nil {
			return err
		}
		idle, err := ctx.IsIdle()
		if err != nil {
			return err
		}
		ctx.Notify(fmt.Sprintf("rw_idle_after_trigger=%t", idle), "info")
		return nil
	})
	settledOnce := &atomic.Bool{}
	ext.Command("rw_idle_in_settled", "Read isIdle after a message that starts a run, sent from an agent_settled handler", func(sdk.Context, string) error {
		ext.OnEvent("agent_settled", func(hctx sdk.Context, _ map[string]any) (any, error) {
			if !settledOnce.CompareAndSwap(false, true) {
				return nil, nil
			}
			trigger := true
			if err := hctx.SendMessage("rw-trigger", "rw-trigger", true, sdk.SendMessageOptions{TriggerTurn: &trigger}); err != nil {
				return nil, err
			}
			idle, err := hctx.IsIdle()
			if err != nil {
				return nil, err
			}
			hctx.Notify(fmt.Sprintf("rw_idle_in_settled=%t", idle), "info")
			return nil, nil
		})
		return nil
	})

	ext.Command("set_model", "Switch to the model in the arguments", func(ctx sdk.Context, args string) error {
		ok, err := ctx.SetModel(args)
		if err != nil {
			return err
		}
		ctx.Notify(fmt.Sprintf("setModel=%t", ok), "info")
		return nil
	})
	// event_probe_off runs every unsubscribe event_probe_on returned for the event named by the argument (TestConformance_EventUnsubscribeEveryEvent).
	eventProbeOn := map[string][]func(){}
	// event_probe_result queues the JSON value ("<event> <json>") the next event_probe_on handler call of that event returns, so a test observes what the host does with a handler's result and which handlers it still calls.
	var eventProbeResultsMu sync.Mutex
	eventProbeResults := map[string][]any{}
	ext.Command("event_probe_result", "Queue the JSON result the next event_probe_on handler call of an event returns", func(ctx sdk.Context, args string) error {
		name, raw, _ := strings.Cut(strings.TrimSpace(args), " ")
		var value any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			return err
		}
		eventProbeResultsMu.Lock()
		eventProbeResults[name] = append(eventProbeResults[name], value)
		eventProbeResultsMu.Unlock()
		return nil
	})
	ext.Command("event_probe_on", "Subscribe to the event named by the argument", func(ctx sdk.Context, args string) error {
		name := strings.TrimSpace(args)
		off := ext.OnEvent(name, func(hctx sdk.Context, data map[string]any) (any, error) {
			hctx.Notify("event_probe_on:"+name, "info")
			// The event as the host sent it, for the tests that compare a payload field with the Go reference's.
			if encoded, err := json.Marshal(data); err == nil {
				hctx.Notify("event_probe_data:"+name+":"+string(encoded), "info")
			}
			// mcp_servers_change carries every registered server (types.ts:699-709): report their names, which only the host's registry knows.
			if servers, ok := data["servers"].([]any); ok {
				names := make([]string, 0, len(servers))
				for _, server := range servers {
					entry, _ := server.(map[string]any)
					names = append(names, fmt.Sprint(entry["name"]))
				}
				hctx.Notify("event_probe_servers:"+strings.Join(names, ","), "info")
			}
			payload, err := json.Marshal(data)
			if err != nil {
				return nil, err
			}
			hctx.Notify("event_payload:"+name+":"+string(payload), "info")
			eventProbeResultsMu.Lock()
			defer eventProbeResultsMu.Unlock()
			queued := eventProbeResults[name]
			if len(queued) == 0 {
				return nil, nil
			}
			eventProbeResults[name] = queued[1:]
			return queued[0], nil
		})
		eventProbeOn[name] = append(eventProbeOn[name], off)
		return nil
	})
	ext.Command("event_probe_off", "Unsubscribe the event_probe_on handlers of the event named by the argument", func(ctx sdk.Context, args string) error {
		name := strings.TrimSpace(args)
		for _, off := range eventProbeOn[name] {
			off()
		}
		delete(eventProbeOn, name)
		return nil
	})
	// event_result_probe subscribes to the event named first with a handler that returns the JSON that follows: the result an SDK extension gives the host.
	ext.Command("event_result_probe", "Subscribe to an event with a handler that returns the given JSON", func(ctx sdk.Context, args string) error {
		name, raw, _ := strings.Cut(strings.TrimSpace(args), " ")
		var result any
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			return err
		}
		ext.OnEvent(name, func(sdk.Context, map[string]any) (any, error) { return result, nil })
		return nil
	})
	// event_payload_probe reports the JSON of the event the named handler received, so a conformance test compares what every SDK was sent
	// (TestConformance_EventPayloadsReachEverySDKUnchanged).
	ext.Command("event_payload_probe", "Subscribe and report the payload of the event named by the argument", func(ctx sdk.Context, args string) error {
		name := strings.TrimSpace(args)
		ext.OnEvent(name, func(hctx sdk.Context, data map[string]any) (any, error) {
			wire, err := json.Marshal(data)
			if err != nil {
				return nil, err
			}
			hctx.Notify("event_payload:"+name+":"+string(wire), "info")
			return nil, nil
		})
		return nil
	})
	// event_isolation_probe subscribes two handlers to the event named by the argument (TestConformance_EventHandlersDoNotShareTheirPayload). The first
	// reports the marker it received, writes a sentinel into data["context"]["contextEntries"][0]["sourceEntry"]["id"], keeps that container and changes it again from
	// another goroutine after the handler returned; the second handler of the same dispatch reports what it received. Each handler owns its payload, so no report carries a sentinel.
	ext.Command("event_isolation_probe", "Subscribe two handlers that report the nested marker they received and then write sentinels into it", func(ctx sdk.Context, args string) error {
		name := strings.TrimSpace(args)
		source := func(data map[string]any) map[string]any {
			context, _ := data["context"].(map[string]any)
			entries, _ := context["contextEntries"].([]any)
			if len(entries) == 0 {
				return nil
			}
			entry, _ := entries[0].(map[string]any)
			container, _ := entry["sourceEntry"].(map[string]any)
			return container
		}
		ext.OnEvent(name, func(hctx sdk.Context, data map[string]any) (any, error) {
			container := source(data)
			hctx.Notify(fmt.Sprintf("event_isolation:A:%v:end", container["id"]), "info")
			container["id"] = "SENTINEL-HANDLER-A"
			go func() {
				time.Sleep(100 * time.Millisecond)
				container["id"] = "SENTINEL-LATE-WRITER"
			}()
			return nil, nil
		})
		ext.OnEvent(name, func(hctx sdk.Context, data map[string]any) (any, error) {
			hctx.Notify(fmt.Sprintf("event_isolation:B:%v:end", source(data)["id"]), "info")
			return nil, nil
		})
		return nil
	})
	// tool_result_guards_probe reports which of the Is<Tool>ToolResult guards accept each tool_result event it receives
	// (TestConformance_ToolResultGuardsAgreeAcrossSDKs).
	ext.Command("tool_result_guards_probe", "Report the tool_result guards that accept each tool_result event", func(ctx sdk.Context, args string) error {
		ext.OnEvent("tool_result", func(hctx sdk.Context, data map[string]any) (any, error) {
			var matched []string
			for name, guard := range map[string]func(map[string]any) bool{"bash": sdk.IsBashToolResult, "powershell": sdk.IsPowerShellToolResult, "read": sdk.IsReadToolResult, "edit": sdk.IsEditToolResult, "write": sdk.IsWriteToolResult, "grep": sdk.IsGrepToolResult, "find": sdk.IsFindToolResult, "ls": sdk.IsLsToolResult} {
				if guard(data) {
					matched = append(matched, name)
				}
			}
			slices.Sort(matched)
			hctx.Notify("tool_result_guards:"+strings.Join(matched, ",")+":end", "info")
			return nil, nil
		})
		return nil
	})
	ext.Command("settings_probe", "Report the effective settings", func(ctx sdk.Context, args string) error {
		settings, err := ctx.GetSettings()
		if err != nil {
			return err
		}
		wire, err := json.Marshal(settings)
		if err != nil {
			return err
		}
		ctx.Notify("settings_probe:"+string(wire), "info")
		return nil
	})
	ext.Command("model_set", "Switch the model", func(ctx sdk.Context, args string) error {
		ok, err := ctx.SetModel(strings.TrimSpace(args))
		if err != nil {
			return err
		}
		ctx.Notify("model_set:"+strconv.FormatBool(ok), "info")
		return nil
	})
	ext.Command("thinking_set", "Set the thinking level", func(ctx sdk.Context, args string) error {
		ctx.SetThinkingLevel(args)
		return nil
	})
	ext.Command("thinking_get", "Report the thinking level", func(ctx sdk.Context, args string) error {
		level, err := ctx.GetThinkingLevel()
		if err != nil {
			return err
		}
		ctx.Notify("thinking_get:"+level, "info")
		return nil
	})
	ext.Command("set_label", "Set an entry label", func(ctx sdk.Context, args string) error {
		return ctx.SetLabel("label-entry", "conformance-label")
	})
	// label_probe labels the entry named first with the rest of the arguments: an entry the host's Session holds.
	ext.Command("label_probe", "Label the entry named first", func(ctx sdk.Context, args string) error {
		id, label, _ := strings.Cut(strings.TrimSpace(args), " ")
		return ctx.SetLabel(id, label)
	})
	ext.Shortcut("ctrl+alt+y", "Conformance shortcut", func(ctx sdk.Context) error { return nil })

	ext.Command("append_entry", "Append a custom entry", func(ctx sdk.Context, args string) error {
		return ctx.AppendEntry("conformance-entry", "hello-entry")
	})
	// append-entries-probe appends one custom entry for each comma-separated type in order. Pi's appendEntry returns nothing and never fails, so a refusal the SDK reports is dropped here; the row reads the session afterwards.
	ext.Command("append-entries-probe", "Append a custom entry for each comma-separated type", func(ctx sdk.Context, args string) error {
		for customType := range strings.SplitSeq(args, ",") {
			_ = ctx.AppendEntry(customType, "data-of-"+customType)
		}
		return nil
	})

	ext.Command("sprite-probe", "Exercise sprite registration and host errors", func(ctx sdk.Context, args string) error {
		definition := conformanceSpriteDefinition()
		if err := ctx.RegisterSprite(definition); err != nil {
			return err
		}
		definition.Mascot[0] = definition.Mascot[0][:15]
		err := ctx.RegisterSprite(definition)
		if err == nil {
			return errors.New("invalid sprite definition was accepted")
		}
		ctx.Notify(err.Error(), "error")
		return nil
	})

	ext.Command("login-probe", "Exercise semantic login submission and host errors", func(ctx sdk.Context, args string) error {
		definition := conformanceLoginDefinition()
		if err := ctx.SetLogin(definition); err != nil {
			return err
		}
		definition.Brand[0] = definition.Brand[0][:40]
		err := ctx.SetLogin(definition)
		if err == nil {
			return errors.New("invalid login definition was accepted")
		}
		ctx.Notify(err.Error(), "error")
		return nil
	})

	ext.Command("scoped-models-probe", "Report the model scope", func(ctx sdk.Context, _ string) error {
		models, err := ctx.ScopedModels()
		if err != nil {
			return err
		}
		data, err := json.Marshal(models)
		if err != nil {
			return err
		}
		ctx.Notify(string(data), "info")
		return nil
	})
	ext.Command("ui-availability", "Probe bound UI and headless defaults", func(ctx sdk.Context, _ string) error {
		selected, _, err := ctx.Select("Pick", []string{"first", "second"})
		if err != nil {
			return err
		}
		ctx.Notify("availability-notify", "info")
		if !ctx.HasUI() {
			if err := probeNoUI(ctx); err != nil {
				return err
			}
		}
		return ctx.AppendEntry("ui-availability", fmt.Sprintf("hasUI=%t selected=%s", ctx.HasUI(), dialogText(selected)))
	})

	ext.Command("ui-state-barrier", "Read UI state after a dialog", func(ctx sdk.Context, _ string) error {
		selected, _, err := ctx.Select("expand", []string{"chosen"})
		if err != nil {
			return err
		}
		named, err := ctx.GetTheme("light")
		if err != nil {
			return err
		}
		missing, err := ctx.GetTheme("missing")
		if err != nil {
			return err
		}
		success, message := ctx.SetTheme("missing")
		expanded, err := ctx.GetToolsExpanded()
		if err != nil {
			return err
		}
		ctx.Notify(fmt.Sprintf("selected=%s expanded=%t named=%s missing=%t success=%t error=%s", dialogText(selected), expanded, named.(map[string]any)["name"], missing == nil, success, message), "info")
		return nil
	})

	// Upstream ctx.signal (runner.ts:917-920) is the active run's signal, undefined while no run is active.
	ext.Command("signal-probe", "Report ctx.signal", func(ctx sdk.Context, _ string) error {
		signal := ctx.Signal()
		state := "none"
		if signal != nil {
			state = "live"
			if signal.Err() != nil {
				state = "aborted"
			}
		}
		ctx.Notify("signal:"+state, "info")
		return nil
	})
	// Upstream ctx.cwd, ctx.mode, ctx.hasUI and ctx.model throw the stale message after invalidation (runner.ts:571-600).
	ext.Command("stale-probe", "Report cwd, mode, hasUI and model", func(ctx sdk.Context, _ string) error {
		ctx.Notify(fmt.Sprintf("stale:%s|%s|%t|%s", ctx.Cwd(), ctx.Mode(), ctx.HasUI(), ctx.Model()), "info")
		return nil
	})
	ext.Command("signal-wait", "Wait for ctx.signal to abort", func(ctx sdk.Context, _ string) error {
		signal := ctx.Signal()
		if signal == nil {
			ctx.Notify("wait:none", "info")
			return nil
		}
		ctx.Notify("wait:start", "info")
		select {
		case <-signal.Done():
			ctx.Notify("wait:aborted", "info")
		case <-time.After(10 * time.Second):
			ctx.Notify("wait:timeout", "info")
		}
		return nil
	})
	// signal-poll reads ctx.signal the way a timer that fires between requests does: no request reaches the runtime while it waits for the state in args ("live" or "none").
	ext.Command("signal-poll", "Poll ctx.signal until it is live or none", func(ctx sdk.Context, args string) error {
		ctx.Notify("poll:start", "info")
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if (ctx.Signal() != nil) == (args == "live") {
				ctx.Notify("poll:"+args, "info")
				return nil
			}
			time.Sleep(5 * time.Millisecond)
		}
		ctx.Notify("poll:timeout", "info")
		return nil
	})
	ext.Command("usage-probe", "Report context usage", func(ctx sdk.Context, _ string) error {
		usage, err := ctx.GetContextUsage()
		if err != nil {
			return err
		}
		data, err := json.Marshal(usage)
		if err != nil {
			return err
		}
		ctx.Notify(string(data), "info")
		return nil
	})

	ext.Command("context-probe", "Report ctx.mode + ctx.getSystemPromptOptions()", func(ctx sdk.Context, args string) error {
		opts, err := ctx.GetSystemPromptOptions()
		if err != nil {
			return err
		}
		trusted, err := ctx.IsProjectTrusted()
		if err != nil {
			return err
		}
		skillSource := ""
		if len(opts.Skills) > 0 {
			skillSource = opts.Skills[0].SourceInfo.Scope
		}
		// Upstream normalizes every collection to a present value; nil means the wire omitted it.
		count := func(present bool, kind string, n int) string {
			if !present {
				return "absent"
			}
			return fmt.Sprintf("%s:%d", kind, n)
		}
		shapes := strings.Join([]string{
			"selectedTools:" + count(opts.SelectedTools != nil, "array", len(opts.SelectedTools)),
			"toolSnippets:" + count(opts.ToolSnippets != nil, "object", len(opts.ToolSnippets)),
			"toolGuidelines:" + count(opts.ToolGuidelines != nil, "object", len(opts.ToolGuidelines)),
			"promptGuidelines:" + count(opts.PromptGuidelines != nil, "array", len(opts.PromptGuidelines)),
			fmt.Sprintf("appendSystemPrompt:string:%d", len(utf16.Encode([]rune(opts.AppendSystemPrompt)))),
			"sections:" + count(opts.Sections != nil, "object", func() int {
				if opts.Sections == nil {
					return 0
				}
				return len(*opts.Sections)
			}()),
			"contextFiles:" + count(opts.ContextFiles != nil, "array", len(opts.ContextFiles)),
			"skills:" + count(opts.Skills != nil, "array", len(opts.Skills)),
		}, ",")
		ctx.Notify(fmt.Sprintf("mode=%s trusted=%t spo_prompt=%s spo_cwd=%s spo_tools=%s spo_shape=%s spo_guidelines=%s spo_skill_scope=%s spo_force_empty=%t spo_custom_present=%t",
			ctx.Mode(), trusted, opts.CustomPrompt, opts.Cwd, strings.Join(opts.SelectedTools, ","), shapes, strings.Join(opts.ToolGuidelines["read"], ","), skillSource, opts.ForceSystemPrompt != nil && *opts.ForceSystemPrompt == "", opts.CustomPromptSet || opts.CustomPrompt != ""), "info")
		return nil
	})

	// model-info-probe reports the typed active-model view (TestConformance_TypedModelInfoAgreesBetweenGoAndRust).
	ext.Command("model-info-probe", "Report the typed active model", func(ctx sdk.Context, _ string) error {
		info, err := ctx.GetModelInfo()
		if err != nil {
			return err
		}
		if info == nil {
			ctx.Notify("modelinfo:none:end", "info")
			return nil
		}
		ctx.Notify(fmt.Sprintf("modelinfo:%s/%s/%s/%d/%d/%t/%g/%g/%g/%g:end", info.ID, info.Name, info.Provider, info.ContextWindow, info.MaxOutputTokens, info.Reasoning,
			info.InputCostPer1M, info.OutputCostPer1M, info.CacheReadCostPer1M, info.CacheWriteCostPer1M), "info")
		return nil
	})
	// branch-typed-probe reports the typed branch view, one entry per segment: id, role, content, thinking, provider, model, tool calls (name=id=args) and
	// usage (input/output/cacheRead/cacheWrite/total) (TestConformance_TypedBranchEntriesAgreeBetweenGoAndRust).
	ext.Command("branch-typed-probe", "Report the typed branch entries", func(ctx sdk.Context, _ string) error {
		branch, err := ctx.GetBranch()
		if err != nil {
			return err
		}
		var entries []string
		for _, entry := range branch {
			var calls []string
			for _, call := range entry.ToolCalls {
				calls = append(calls, call.Name+"="+call.ID+"="+call.Args)
			}
			usage := "-"
			if u := entry.Usage; u != nil {
				usage = fmt.Sprintf("%d/%d/%d/%d/%d", u.Input, u.Output, u.CacheRead, u.CacheWrite, u.TotalTokens)
			}
			entries = append(entries, strings.Join([]string{entry.ID, entry.Role, entry.Content, entry.Thinking, entry.Provider, entry.ModelID, strings.Join(calls, ","), usage}, "~"))
		}
		ctx.Notify("branchtyped:"+strings.Join(entries, " | ")+":end", "info")
		return nil
	})
	ext.Command("session-log-probe", "Read a paged session log", func(ctx sdk.Context, _ string) error {
		entries, err := ctx.GetEntries()
		if err != nil {
			return err
		}
		branch, err := ctx.GetBranch()
		if err != nil {
			return err
		}
		ctx.Notify(fmt.Sprintf("session entries=%d branch=%d", len(entries), len(branch)), "info")
		return nil
	})
	// session-branch-probe reports the branch, the leaf entry, the entry the argument names and that entry's label, as "branch=a>b leaf=b entry=a label=L" ("none" for an absent value).
	ext.Command("session-branch-probe", "Read the branch, an entry and its label", func(ctx sdk.Context, args string) error {
		branch, err := ctx.GetBranch()
		if err != nil {
			return err
		}
		ids := make([]string, len(branch))
		for i, entry := range branch {
			ids[i] = entry.ID
		}
		manager := ctx.SessionManager()
		leaf, err := manager.GetLeafEntry()
		if err != nil {
			return err
		}
		entry, err := manager.GetEntry(args)
		if err != nil {
			return err
		}
		label, err := manager.GetLabel(args)
		if err != nil {
			return err
		}
		id := func(entry map[string]any) string {
			if entry == nil {
				return "none"
			}
			return fmt.Sprint(entry["id"])
		}
		labelText := "none"
		if label != nil {
			labelText = *label
		}
		ctx.Notify(fmt.Sprintf("branch=%s leaf=%s entry=%s label=%s", strings.Join(ids, ">"), id(leaf), id(entry), labelText), "info")
		return nil
	})

	ext.Command("dialog-probe", "Exercise interactive dialog responses", func(ctx sdk.Context, args string) error {
		selected, _, selectErr := ctx.Select("Pick", []string{"first", "second"})
		input, _, inputErr := ctx.Input("Input", "placeholder")
		edited, _, editorErr := ctx.Editor("Editor", "prefill")
		confirmed, confirmErr := ctx.Confirm("Confirm", "message")
		if selectErr != nil || inputErr != nil || editorErr != nil || confirmErr != nil {
			return errors.Join(selectErr, inputErr, editorErr, confirmErr)
		}
		ctx.Notify(fmt.Sprintf("select=%s input=%s editor=%s confirm=%t",
			dialogText(selected), dialogText(input), dialogText(edited), confirmed == true), "info")
		return nil
	})

	ext.Command("focused-probe", "Exercise focused subprocess UI", func(ctx sdk.Context, args string) error {
		component := &focusedList{items: []string{"alpha", "beta", "gamma"}}
		selected, err := ctx.Custom(component, sdk.RemoteOverlayOptions{Title: "Focused", WidthFraction: 0.5, HeightFraction: 0.5})
		if err != nil {
			return err
		}
		selectedText := ""
		if selected != nil {
			selectedText = fmt.Sprint(selected)
		}
		ctx.Notify(fmt.Sprintf("focused=%s disposed=%t", selectedText, component.disposed), "info")
		return nil
	})

	// An overlay component renders at the overlay's resolved width (tui.ts:1212-1233), not the terminal width (conformance TestConformance_OverlayRenderWidth).
	ext.Command("overlay-width-probe", "Report the width overlay components render at", func(ctx sdk.Context, _ string) error {
		defaultWidth, err := ctx.Custom(&overlayWidthProbe{}, sdk.RemoteOverlayOptions{Overlay: true})
		if err != nil {
			return err
		}
		percentWidth, err := ctx.Custom(&overlayWidthProbe{}, sdk.RemoteOverlayOptions{Overlay: true, OverlayOptions: &sdk.OverlayOptions{Width: sdk.OverlayPercent(50)}})
		if err != nil {
			return err
		}
		ctx.Notify(fmt.Sprintf("overlay-width default=%v percent=%v", defaultWidth, percentWidth), "info")
		return nil
	})

	registerKitWidgets(ext)
	registerEditorHooks(ext)

	ext.Command("kit-probe", "Exercise the component kit (D107)", func(ctx sdk.Context, args string) error {
		result, err := ctx.Custom(&kitProbe{}, sdk.RemoteOverlayOptions{Title: "Kit"})
		if err != nil {
			return err
		}
		resultText := ""
		if result != nil {
			resultText = fmt.Sprint(result)
		}
		ctx.Notify("kit="+resultText, "info")
		return nil
	})

	// mouse-probe opens the mouse row's component: the host hands it
	// fullscreen mouse events, and it closes on a click with what it got.
	ext.Command("mouse-probe", "Exercise extension mouse input", func(ctx sdk.Context, args string) error {
		result, err := ctx.Custom(&mouseProbe{}, sdk.RemoteOverlayOptions{Overlay: true})
		if err != nil {
			return err
		}
		ctx.Notify(fmt.Sprintf("mouse=%v", result), "info")
		return nil
	})

	// kit-surfaces shows the kit probe's tree on every other view surface
	// (D107, spec §10): a pushed widget, the header, the footer and a tool
	// result. The message renderer "kit-message" draws it too.
	ext.Command("kit-surfaces", "Show the kit probe on every view surface (D107)", func(ctx sdk.Context, _ string) error {
		view := (&kitProbe{}).View(0)
		if err := ctx.SetWidget("kit-probe", view); err != nil {
			return err
		}
		if err := ctx.SetHeaderView(view); err != nil {
			return err
		}
		if err := ctx.SetFooterView(view); err != nil {
			return err
		}
		ctx.RegisterTool(sdk.ToolDefinition{Name: "kit_view_tool", Label: "kit_view_tool", Description: "Render its result as the kit probe", Parameters: sdk.Schema{"type": "object"}, Execute: func(sdk.Context, map[string]any) (any, error) { return "kit", nil }})
		ext.SetToolRenderers("kit_view_tool", sdk.ToolRenderers{ResultView: func(sdk.Context, sdk.ToolRenderResult, sdk.ToolRenderResultOptions, sdk.ToolRenderContext, int) (kit.View, error) {
			return (&kitProbe{}).View(0), nil
		}})
		return nil
	})

	// kit-images drives the image transport (D107, spec §7): step "a<k>"
	// shows image 0 and step "b<n>" image n (1 ≤ n ≤ 64), each with its step
	// as the text, as one ui.setWidget call on the "kit-img" widget.
	ext.Command("kit-images", "Exercise the component kit's image transport (D107)", func(ctx sdk.Context, args string) error {
		show := func(n int) error {
			view := kit.View{Root: kit.NewContainer(kit.NewImage(kitImagePNG(n), "image/png"), kit.NewText(args, 0, 0))}
			return ctx.SetWidget("kit-img", view, sdk.WidgetOptions{})
		}
		n, err := strconv.Atoi(args[min(len(args), 1):])
		switch {
		case err != nil || n < 1:
		case args[0] == 'a':
			return show(0)
		case args[0] == 'b' && n <= 64:
			return show(n)
		}
		return fmt.Errorf("kit-images: unknown step %q", args)
	})

	// kit-kinds sets the "kit-kinds" widget to the kinds kit-probe does not
	// draw (D107, spec §10): a box, a settings list, a loader, an image and
	// a lines node whose list annotation goes out only while a frontend
	// draws.
	ext.Command("kit-kinds", "Show every other component kit kind (D107)", func(ctx sdk.Context, _ string) error {
		data, err := hex.DecodeString(kitKindsPNG)
		if err != nil {
			return err
		}
		box := kit.NewBox(1, 0, kit.NewText("boxed", 0, 0))
		box.Bg = "customMessageBg"
		settings := kit.NewSettingsList("kit-settings", []kit.SettingItem{
			{ID: "theme", Label: "Theme", Description: "Color theme", CurrentValue: "dark", Values: []string{"dark", "light"}},
			{ID: "wrap", Label: "Wrap", Description: "Wrap lines", CurrentValue: "on", Values: []string{"on", "off"}},
		}, 3)
		loader := kit.NewLoader("Working")
		loader.SpinnerColor, loader.MessageColor = "accent", "muted"
		loader.Indicator = &kit.LoaderIndicator{Frames: []string{"*"}}
		lines := kit.NewLines([]string{"track one", "track two"})
		lines.List = &kit.List{Items: []kit.ListItem{{Label: "track one", Detail: "A"}, {Label: "track two", Detail: "B"}}, Selected: 1}
		stack := kit.NewVStack(box, settings, loader, kit.NewImage(data, "image/png"), lines)
		stack.Gap = 1
		view := kit.View{Root: stack, Theme: map[string]string{"accent": "#d75f00"}}
		return ctx.SetWidget("kit-kinds", view, sdk.WidgetOptions{})
	})

	// kit-conversation sets the "kit-conversation" widget to every
	// conversation kind (D107, spec §2.1, §10); "next" updates the nodes
	// with an id as a Pi author updates kept components.
	ext.Command("kit-conversation", "Show Pi's conversation components (D107)", func(ctx sdk.Context, args string) error {
		next := args == "next"
		user := kit.NewUserMessage("Fix **the** kit build\n\n- one\n- two")
		streaming := kit.NewAssistantMessage(nil)
		streaming.ID = "kit-a1"
		reply := "Done. **Bold** reply\n\n1. a\n2. b"
		if next {
			reply += "\n\nThen more kit."
		}
		streaming.UpdateContent(kit.Message{Content: []kit.ContentBlock{kit.ThinkingBlock("Reading the *kit* file"), kit.TextBlock(reply)}}, !next)
		failed := kit.NewAssistantMessage(&kit.Message{Content: []kit.ContentBlock{kit.ThinkingBlock("secret"), kit.TextBlock("Visible kit")}, StopReason: "error", ErrorMessage: "kit-boom-7"})
		failed.SetHideThinkingBlock(true)
		failed.SetHiddenThinkingLabel("Pondering kit...")
		failed.SetOutputPad(0)
		card := func(id, name, callID string, args map[string]any) *kit.ToolExecution {
			tool := kit.NewToolExecution(name, callID, args, "/work/kit")
			tool.ID = id
			tool.SetArgsComplete()
			tool.MarkExecutionStarted()
			return tool
		}
		ls := card("kit-t1", "ls", "call-1", map[string]any{"path": "src"})
		ls.UpdateResult(kit.ToolResult{Content: []kit.ToolResultContent{kit.TextContent("a.go\nb.go")}}, false)
		grep := card("kit-t2", "grep", "call-2", map[string]any{"pattern": "TODO"})
		if next {
			grep.UpdateResult(kit.ToolResult{Content: []kit.ToolResultContent{kit.TextContent("x.go:1: TODO kit\ny.go:2: TODO kit")}}, false)
		} else {
			grep.UpdateResult(kit.ToolResult{Content: []kit.ToolResultContent{kit.TextContent("x.go:1: TODO kit")}}, true)
		}
		read := card("", "read", "call-3", map[string]any{"path": "missing.txt"})
		read.UpdateResult(kit.ToolResult{Content: []kit.ToolResultContent{kit.TextContent("ENOENT: kit")}, IsError: true}, false)
		read.SetExpanded(true)
		custom := card("", "kit_tool", "call-4", map[string]any{"q": "x"})
		custom.ToolDefinition = kit.ToolDefinitionEmpty
		custom.UpdateResult(kit.ToolResult{Content: []kit.ToolResultContent{kit.TextContent("answer 42")}}, false)
		exited := kit.NewBashExecution("ls -la", false)
		exited.ID = "kit-b1"
		exited.AppendOutput("a.txt\n")
		exited.AppendOutput("b.txt")
		exited.SetComplete(new(2), false, false, "")
		exited.SetExpanded(next)
		numbers := make([]string, 25)
		for i := range numbers {
			numbers[i] = strconv.Itoa(i + 1)
		}
		seq := kit.NewBashExecution("seq 25", true)
		seq.AppendOutput(strings.Join(numbers, "\n"))
		seq.SetComplete(new(0), false, false, "")
		diff := kit.NewDiff(" 1 keep\n-2 old kit line\n+2 new kit line\n 3 tail")
		diff.FilePath = "kit.go"
		root := kit.NewContainer(user, streaming, failed, ls, grep, read, custom, exited, seq, diff)
		return ctx.SetWidget("kit-conversation", kit.View{Root: root}, sdk.WidgetOptions{})
	})

	// kit-output-pad sets the "kit-output-pad" widget to a bash tool card and
	// a bash command with Pi 1.1.0's outputPad 3 and a final result's
	// durationMs 1234 (F7); "next" calls setOutputPad(2) on both and gives
	// the result durationMs 61500.
	ext.Command("kit-output-pad", "Show Pi's outputPad and durationMs (D107)", func(ctx sdk.Context, args string) error {
		next := args == "next"
		duration := int64(1234)
		if next {
			duration = 61500
		}
		tool := kit.NewToolExecution("bash", "call-p1", map[string]any{"command": "echo kit"}, "/work/kit")
		tool.ID = "kit-p1"
		tool.OutputPad = 3
		tool.SetArgsComplete()
		tool.MarkExecutionStarted()
		tool.UpdateResult(kit.ToolResult{Content: []kit.ToolResultContent{kit.TextContent("kit out")}, DurationMs: &duration}, false)
		command := kit.NewBashExecution("echo pad", false)
		command.ID = "kit-p2"
		command.OutputPad = 3
		command.AppendOutput("padded")
		command.SetComplete(new(0), false, false, "")
		if next {
			tool.SetOutputPad(2)
			command.SetOutputPad(2)
		}
		return ctx.SetWidget("kit-output-pad", kit.View{Root: kit.NewContainer(tool, command)}, sdk.WidgetOptions{})
	})

	ext.Command("timer-focused-probe", "Exercise timer-driven focused UI", func(ctx sdk.Context, args string) error {
		component := newTimerFocused()
		frame, err := ctx.Custom(component, sdk.RemoteOverlayOptions{Title: "Timer"})
		if err != nil {
			return err
		}
		ctx.Notify(fmt.Sprintf("timer=%v disposed=%t detached=%t", frame, component.Disposed(), component.Detached()), "info")
		return nil
	})

	ext.Command("model-stream-probe", "Exercise model streaming", func(ctx sdk.Context, _ string) error {
		registry := ctx.ModelRegistry()
		current, err := registry.Find("conformance", "current")
		if err != nil {
			return err
		}
		if current == nil || fmt.Sprint(current["id"]) != "current" {
			return fmt.Errorf("find current = %#v", current)
		}
		found, err := registry.Find("conformance", "declared")
		if err != nil {
			return err
		}
		if found == nil || fmt.Sprint(found["id"]) != "declared" || fmt.Sprint(found["provider"]) != "conformance" {
			return fmt.Errorf("find declared = %#v", found)
		}
		slash, err := registry.Find("conformance", "org/model/name")
		if err != nil {
			return err
		}
		if slash == nil || fmt.Sprint(slash["id"]) != "org/model/name" {
			return fmt.Errorf("find slash = %#v", slash)
		}
		for _, field := range []string{"baseUrl", "input", "cost", "thinkingLevelMap", "promptCache", "contextWindow", "maxTokens", "samplingParams", "headers", "compat"} {
			if _, ok := slash[field]; !ok {
				return fmt.Errorf("find slash missing %s: %#v", field, slash)
			}
		}
		var expectedLimits map[string]any
		if err := json.Unmarshal([]byte(`{"maxRequestBytes":12345,"images":{"maxPerMessage":7,"maxPerRequest":11,"resize":{"maxWidth":321,"maxHeight":123,"maxBytes":45678,"jpegQuality":67}}}`), &expectedLimits); err != nil {
			return err
		}
		if !reflect.DeepEqual(slash["inputLimits"], expectedLimits) {
			return fmt.Errorf("find slash inputLimits = %#v", slash["inputLimits"])
		}
		active, err := ctx.GetModelInfo()
		if err != nil {
			return err
		}
		if active == nil || !reflect.DeepEqual(active.InputLimits, expectedLimits) {
			return fmt.Errorf("active inputLimits = %#v", active)
		}
		input, _ := slash["input"].([]any)
		cost, _ := slash["cost"].(map[string]any)
		tiers, _ := cost["tiers"].([]any)
		compat, _ := slash["compat"].(map[string]any)
		if len(input) != 0 || cost["input"] != float64(0) || len(tiers) != 1 || compat["supportsStrictMode"] != false {
			return fmt.Errorf("find slash shape = %#v", slash)
		}
		missing, err := registry.Find("conformance", "missing")
		if err != nil {
			return err
		}
		if missing != nil {
			return fmt.Errorf("find missing = %#v", missing)
		}
		overrideOnly, err := registry.Find("conformance", "override-only")
		if err != nil {
			return err
		}
		if overrideOnly != nil {
			return fmt.Errorf("find override-only = %#v", overrideOnly)
		}
		auth, err := registry.GetApiKeyAndHeaders(found)
		if err != nil {
			return fmt.Errorf("get auth: %w", err)
		}
		wantAuth := map[string]any{"ok": true, "apiKey": "conformance-key", "headers": map[string]any{"X-Conformance-Auth": "yes"}, "baseUrl": "https://models.invalid/v1", "env": map[string]any{"CONFORMANCE_AUTH": "yes"}}
		if !reflect.DeepEqual(auth, wantAuth) {
			return fmt.Errorf("auth = %#v, want %#v", auth, wantAuth)
		}
		model := map[string]any{"provider": "conformance", "modelId": "declared", "api": "openai-responses"}
		request := modelConformanceContext()
		options := modelConformanceOptions()
		stream := ctx.ModelRegistry().Stream(model, request, options)
		var types []string
		for event := range stream.Events(context.Background()) {
			types = append(types, fmt.Sprint(event["type"]))
		}
		if strings.Join(types, ",") != "start,text_start,text_delta,text_end,done" {
			return fmt.Errorf("stream events = %v", types)
		}
		result := stream.Result()
		content, _ := result["content"].([]any)
		block, _ := content[0].(map[string]any)
		if fmt.Sprint(block["text"]) != "streamed" {
			return fmt.Errorf("stream result = %#v", result)
		}
		simple := ctx.ModelRegistry().StreamSimple(model, request, options)
		types = nil
		for event := range simple.Events(context.Background()) {
			types = append(types, fmt.Sprint(event["type"]))
		}
		if strings.Join(types, ",") != "start,text_start,text_delta,text_end,done" {
			return fmt.Errorf("simple events = %v", types)
		}
		if fmt.Sprint(simple.Result()["stopReason"]) != "stop" {
			return fmt.Errorf("simple result = %#v", simple.Result())
		}
		if fmt.Sprint(ctx.ModelRegistry().Complete(model, request, options)["stopReason"]) != "stop" {
			return errors.New("complete did not stop")
		}
		if fmt.Sprint(ctx.ModelRegistry().Stream(model, request, options).Result()["stopReason"]) != "stop" {
			return errors.New("result without iteration did not stop")
		}
		unknown := ctx.ModelRegistry().Complete(map[string]any{"provider": "conformance", "modelId": "unknown", "api": "openai-responses"}, request, options)
		if fmt.Sprint(unknown["stopReason"]) != "error" || !strings.Contains(fmt.Sprint(unknown["errorMessage"]), "unknown model") {
			return fmt.Errorf("unknown result = %#v", unknown)
		}
		transportError := ctx.ModelRegistry().Complete(map[string]any{"provider": "conformance", "modelId": "protocol-error", "api": "openai-responses"}, request, options)
		timestamp, ok := transportError["timestamp"].(int64)
		if !ok || timestamp <= 0 {
			return fmt.Errorf("transport error timestamp = %#v", transportError["timestamp"])
		}
		delete(transportError, "timestamp")
		wantTransportError := map[string]any{
			"role": "assistant", "content": []any{}, "api": "openai-responses", "provider": "conformance", "model": "protocol-error",
			"usage":      map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}},
			"stopReason": "error", "errorMessage": "transport boom",
		}
		if !reflect.DeepEqual(transportError, wantTransportError) {
			return fmt.Errorf("transport error = %#v, want %#v", transportError, wantTransportError)
		}
		ctx.Notify("model-stream=ok", "info")
		return nil
	})

	ext.Command("overlay-handle-probe", "Exercise the overlay handle Custom hands to OnHandle", func(ctx sdk.Context, _ string) error {
		var failures []string
		expect := func(label string, actual, expected any) {
			if fmt.Sprint(actual) != fmt.Sprint(expected) {
				failures = append(failures, fmt.Sprintf("%s: %v != %v", label, actual, expected))
			}
		}
		onHandle := func(handle *sdk.OverlayHandle) {
			expect("initial focused", handle.IsFocused(), true)
			expect("initial hidden", handle.IsHidden(), false)
			expect("initial bounds", *handle.GetBounds(), sdk.OverlayBounds{Row: 3, Col: 4, Width: 20, Height: 5})
			_ = handle.Focus()
			expect("focus", handle.IsFocused(), true)
			_ = handle.SetHidden(true)
			expect("hidden", fmt.Sprint(handle.IsHidden(), handle.IsFocused(), handle.GetBounds() == nil), "true false true")
			_ = handle.SetHidden(false)
			expect("shown", fmt.Sprint(handle.IsHidden(), handle.IsFocused()), "false false")
			_ = handle.Focus()
			expect("refocus", handle.IsFocused(), true)
			_ = handle.Unfocus()
			expect("unfocus", handle.IsFocused(), false)
			_ = handle.Unfocus(sdk.UnfocusOptions{})
		}
		if _, err := ctx.Custom(&handleProbeComponent{}, sdk.RemoteOverlayOptions{Overlay: true, OnHandle: onHandle}); err != nil {
			return err
		}
		if len(failures) > 0 {
			return fmt.Errorf("%s", strings.Join(failures, "; "))
		}
		ctx.Notify("overlay-handle=ok", "info")
		return nil
	})

	ext.Command("overlay-focus-probe", "Render the focus the host publishes to a mounted overlay", func(ctx sdk.Context, _ string) error {
		component := &focusProbeComponent{}
		onHandle := func(handle *sdk.OverlayHandle) { component.handle.Store(handle) }
		if _, err := ctx.Custom(component, sdk.RemoteOverlayOptions{Overlay: true, OnHandle: onHandle}); err != nil {
			return err
		}
		ctx.Notify("overlay-focus=ok", "info")
		return nil
	})

	ext.Command("model-stream-fetch-probe", "Exercise the fetch option of ModelRegistry.Stream", func(ctx sdk.Context, _ string) error {
		registry := ctx.ModelRegistry()
		model := map[string]any{"provider": "conformance", "id": "declared", "modelId": "declared", "api": "openai-responses"}
		request := map[string]any{"systemPrompt": "fetch", "messages": []any{map[string]any{"role": "user", "content": "hello", "timestamp": 1}}}
		var seen struct{ url, method, host, body string }
		fetch := func(call *http.Request) (*http.Response, error) {
			data, _ := io.ReadAll(call.Body)
			seen.url, seen.method, seen.host, seen.body = call.URL.String(), call.Method, call.Header.Get("X-Host"), string(data)
			body := make([]byte, 70000)
			for i := range body {
				body[i] = byte(i % 251)
			}
			return &http.Response{StatusCode: 207, Status: "207 Answered", Header: http.Header{"X-Sdk-Fetch": {"answered"}}, Body: io.NopCloser(bytes.NewReader(body))}, nil
		}
		if result := registry.Stream(model, request, map[string]any{"fetch": fetch}).Result(); result["stopReason"] != "stop" {
			return fmt.Errorf("fetch stream = %#v", result)
		}
		if seen.url != "https://fetch.invalid/v1/chat?x=1" || seen.method != "POST" || seen.host != "1" || seen.body != "ping-body" {
			return fmt.Errorf("fetch saw %+v", seen)
		}
		if result := registry.Stream(model, request, map[string]any{}).Result(); result["stopReason"] != "stop" {
			return fmt.Errorf("plain stream = %#v", result)
		}
		ctx.Notify("model-fetch=ok", "info")
		return nil
	})

	ext.Command("model-stream-callback-probe", "Exercise the provider request callbacks of ModelRegistry.Stream", func(ctx sdk.Context, _ string) error {
		registry := ctx.ModelRegistry()
		model := map[string]any{"provider": "conformance", "id": "declared", "modelId": "declared", "api": "openai-responses"}
		request := map[string]any{"systemPrompt": "callbacks", "messages": []any{map[string]any{"role": "user", "content": "hello", "timestamp": 1}}}
		var seenPayload, seenResponse map[string]any
		var seenModel string
		options := map[string]any{
			"onPayload": func(payload any, callbackModel map[string]any) (any, error) {
				seenPayload, _ = payload.(map[string]any)
				seenModel, _ = callbackModel["id"].(string)
				marked := map[string]any{"mark": "on-payload"}
				maps.Copy(marked, seenPayload)
				return marked, nil
			},
			"onResponse": func(response map[string]any, _ map[string]any) error {
				seenResponse = response
				return nil
			},
			"transformHeaders": func(headers map[string]any, _ map[string]any) (map[string]any, error) {
				headers["x-transformed"] = "yes"
				return headers, nil
			},
		}
		stream := registry.Stream(model, request, options)
		if result := stream.Result(); result["stopReason"] != "stop" {
			return fmt.Errorf("callback stream = %#v", result)
		}
		if seenPayload["original"] != true || seenModel != "declared" {
			return fmt.Errorf("onPayload saw %#v for %q", seenPayload, seenModel)
		}
		headers, _ := seenResponse["headers"].(map[string]any)
		if seenResponse["status"] != float64(201) || headers["x-upstream"] != "seen" {
			return fmt.Errorf("onResponse saw %#v", seenResponse)
		}
		if result := registry.Stream(model, request, map[string]any{}).Result(); result["stopReason"] != "stop" {
			return fmt.Errorf("plain stream = %#v", result)
		}
		ctx.Notify("model-callbacks=ok", "info")
		return nil
	})

	var installedEditorFactory sdk.EditorFactory
	ext.Command("editor-install", "Install a custom editor component", func(ctx sdk.Context, args string) error {
		installedEditorFactory = func(base *sdk.Editor) sdk.EditorComponent { return &conformanceEditor{Editor: base} }
		return ctx.SetEditorComponent(installedEditorFactory)
	})
	// editor-get-probe reports what getEditorComponent returns: the factory this extension installed, or nothing
	// (TestConformance_EditorComponentGetterAgreesAcrossSDKs).
	ext.Command("editor-get-probe", "Report the factory getEditorComponent returns", func(ctx sdk.Context, args string) error {
		got := ctx.GetEditorComponent()
		state := "absent"
		if factory, ok := got.(sdk.EditorFactory); ok && factory != nil {
			state = "other"
			if installedEditorFactory != nil && reflect.ValueOf(factory).Pointer() == reflect.ValueOf(installedEditorFactory).Pointer() {
				state = "installed"
			}
		}
		ctx.Notify("editor_get:"+state+":end", "info")
		return nil
	})
	// truncate_probe runs one SDK truncation helper on the JSON case in its argument and reports the result in Pi's shape
	// (TestConformance_TruncationHelpersAgreeAcrossSDKs).
	// language_probe reports Pi's getLanguageFromPath for a path (TestConformance_LanguageFromPathAgreesAcrossSDKs).
	ext.Command("language_probe", "Report the highlight language of a path", func(ctx sdk.Context, args string) error {
		var c struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal([]byte(args), &c); err != nil {
			return err
		}
		var language any
		if name, ok := sdk.GetLanguageFromPath(c.Path); ok {
			language = name
		}
		wire, err := json.Marshal(map[string]any{"lang": language})
		if err != nil {
			return err
		}
		ctx.Notify("language:"+string(wire)+":end", "info")
		return nil
	})
	// convert_probe runs convertToLlm or formatDimensionNote on a JSON case (TestConformance_MessageConversionAgreesAcrossSDKs).
	ext.Command("convert_probe", "Run convertToLlm or formatDimensionNote on a JSON case and report the result", func(ctx sdk.Context, args string) error {
		var c struct {
			Fn       string `json:"fn"`
			Messages []any  `json:"messages"`
			Image    struct {
				OriginalWidth  float64 `json:"originalWidth"`
				OriginalHeight float64 `json:"originalHeight"`
				Width          float64 `json:"width"`
				Height         float64 `json:"height"`
				WasResized     bool    `json:"wasResized"`
			} `json:"image"`
		}
		if err := json.Unmarshal([]byte(args), &c); err != nil {
			return err
		}
		var result any
		switch c.Fn {
		case "convert":
			// A message that is not an object has no role; the typed slice carries it as a nil map, and Pi drops it too.
			messages := make([]map[string]any, len(c.Messages))
			for i, message := range c.Messages {
				messages[i], _ = message.(map[string]any)
			}
			result = sdk.ConvertToLlm(messages)
		case "note":
			if note, ok := sdk.FormatDimensionNote(sdk.ResizedImage{OriginalWidth: c.Image.OriginalWidth, OriginalHeight: c.Image.OriginalHeight, Width: c.Image.Width, Height: c.Image.Height, WasResized: c.Image.WasResized}); ok {
				result = note
			}
		}
		wire, err := json.Marshal(map[string]any{"result": result})
		if err != nil {
			return err
		}
		ctx.Notify("convert:"+string(wire)+":end", "info")
		return nil
	})
	// serialize_probe runs serializeConversation on the raw JSON messages in its argument (TestConformance_SerializeConversationAgreesAcrossSDKs).
	ext.Command("serialize_probe", "Run serializeConversation on JSON messages and report the text", func(ctx sdk.Context, args string) error {
		var c struct {
			Messages []json.RawMessage `json:"messages"`
		}
		if err := json.Unmarshal([]byte(args), &c); err != nil {
			return err
		}
		out := map[string]any{}
		if text, err := sdk.SerializeConversation(c.Messages); err != nil {
			out["error"] = true
		} else {
			out["result"] = text
		}
		// The SDK's codec writes an unpaired surrogate as its \uXXXX escape, as JSON.stringify does; encoding/json would write U+FFFD.
		wire, err := jsjson.MarshalJS(out)
		if err != nil {
			return err
		}
		ctx.Notify("serialize:"+string(wire)+":end", "info")
		return nil
	})
	// branch_probe runs prepareBranchEntries or collectEntriesForBranchSummary (TestConformance_BranchSummaryHelpersAgreeAcrossSDKs); collect reads a session built from the entries.
	ext.Command("branch_probe", "Run a branch summarization helper on a JSON case and report the result", func(ctx sdk.Context, args string) error {
		var c struct {
			Fn      string            `json:"fn"`
			Entries []json.RawMessage `json:"entries"`
			Lone    bool              `json:"lone"`
			Budget  int               `json:"budget"`
			Old     *string           `json:"old"`
			Target  string            `json:"target"`
		}
		if err := jsjson.Unmarshal([]byte(args), &c); err != nil {
			return err
		}
		// The entries are raw JSON: a JavaScript string may hold an unpaired surrogate, which a decoded Go string cannot, and only the raw path keeps it.
		var wire []byte
		var decoded []map[string]any
		for _, raw := range c.Entries {
			var entry map[string]any
			if err := jsjson.Unmarshal(raw, &entry); err != nil {
				return err
			}
			decoded = append(decoded, entry)
		}
		var out map[string]any
		if c.Fn == "prepare" {
			text, err := sdk.PrepareBranchEntriesJSON(c.Entries, c.Budget)
			switch {
			case err != nil:
				out = map[string]any{"error": true}
			case c.Lone:
				wire = text
			default:
				// Where a decoded value can hold the input, the map path must answer what the raw path answers.
				r, err := sdk.PrepareBranchEntries(decoded, c.Budget)
				plain, merr := jsjson.MarshalJS(map[string]any{"messages": nonNilMaps(r.Messages), "fileOps": map[string]any{"read": nonNil(r.FileOps.Read), "written": nonNil(r.FileOps.Written), "edited": nonNil(r.FileOps.Edited)}, "totalTokens": r.TotalTokens})
				var a, b any
				if err != nil || merr != nil || jsjson.Unmarshal(plain, &a) != nil || jsjson.Unmarshal(text, &b) != nil || !reflect.DeepEqual(a, b) {
					out = map[string]any{"error": "the map path and the raw path differ"}
				} else {
					wire = text
				}
			}
		} else {
			r, err := sdk.CollectEntriesForBranchSummary(entrySession(decoded), c.Old, c.Target)
			if err != nil {
				out = map[string]any{"error": true}
			} else {
				out = map[string]any{"entries": r.Entries, "commonAncestorId": r.CommonAncestorID}
			}
		}
		if wire == nil {
			var err error
			if wire, err = jsjson.MarshalJS(out); err != nil {
				return err
			}
		}
		ctx.Notify("branch:"+string(wire)+":end", "info")
		return nil
	})
	// helper_probe and helper_factory_probe run the host-parser helpers (TestConformance_HostParserHelpersAgreeAcrossSDKs): the first from a handler over
	// its own connection, the second reports what the same helpers answered while this factory ran, before the connection to the host was ready.
	helperReport := func(call func(fn, input string) (map[string]any, error), fn, input string) map[string]any {
		out, err := call(fn, input)
		if err != nil {
			return map[string]any{"error": true}
		}
		return out
	}
	goHelperResult := func(parsed sdk.ParsedFrontmatter, keys func() ([]string, error)) (map[string]any, error) {
		lists, err := keys()
		if err != nil {
			return nil, err
		}
		return map[string]any{"frontmatter": parsed.Frontmatter, "body": parsed.Body, "keys": lists}, nil
	}
	factoryCall := func(fn, input string) (map[string]any, error) {
		switch fn {
		case "date":
			ms, ok, err := sdk.ParseDate(input)
			if err != nil {
				return nil, err
			}
			if !ok {
				return map[string]any{"ms": nil}, nil
			}
			return map[string]any{"ms": ms}, nil
		case "strip":
			body, err := sdk.StripFrontmatter(input)
			return map[string]any{"body": body}, err
		}
		parsed, err := sdk.ParseFrontmatter(input)
		if err != nil {
			return nil, err
		}
		return goHelperResult(parsed, func() ([]string, error) { return objectKeyLists(parsed.Frontmatter) })
	}
	factoryHelpers := map[string]any{
		"frontmatter": helperReport(factoryCall, "frontmatter", "---\nz: 1\n10: a\n2: b\nnested:\n  y: 1\n  3: x\n  \"q r\": [1, 2]\n---\nbody text\n"),
		"error":       helperReport(factoryCall, "frontmatter", "---\na: [\n---\nx"),
		"date":        helperReport(factoryCall, "date", "2020-01-02T03:04:05.678Z"),
		"strip":       helperReport(factoryCall, "strip", "---\nz: 1\n---\nbody text\n"),
	}
	ext.Command("helper_probe", "Run parseFrontmatter, stripFrontmatter or Date.parse on its argument", func(ctx sdk.Context, args string) error {
		var c struct {
			Fn    string `json:"fn"`
			Input string `json:"input"`
		}
		if err := json.Unmarshal([]byte(args), &c); err != nil {
			return err
		}
		call := func(fn, input string) (map[string]any, error) {
			switch fn {
			case "date":
				ms, ok, err := ctx.ParseDate(input)
				if err != nil {
					return nil, err
				}
				if !ok {
					return map[string]any{"ms": nil}, nil
				}
				return map[string]any{"ms": ms}, nil
			case "strip":
				parsed, err := ctx.ParseFrontmatter(input)
				return map[string]any{"body": parsed.Body}, err
			}
			parsed, err := ctx.ParseFrontmatter(input)
			if err != nil {
				return nil, err
			}
			return goHelperResult(parsed, func() ([]string, error) { return objectKeyLists(parsed.Frontmatter) })
		}
		wire, err := json.Marshal(helperReport(call, c.Fn, c.Input))
		if err != nil {
			return err
		}
		ctx.Notify("helper:"+string(wire)+":end", "info")
		return nil
	})
	ext.Command("helper_factory_probe", "Report the helpers' results from the factory", func(ctx sdk.Context, args string) error {
		wire, err := json.Marshal(factoryHelpers)
		if err != nil {
			return err
		}
		ctx.Notify("helper-factory:"+string(wire)+":end", "info")
		return nil
	})
	// compact_usage_probe runs a compaction usage helper on a JSON case (TestConformance_CompactionUsageHelpersAgreeAcrossSDKs).
	ext.Command("compact_usage_probe", "Run a compaction usage helper on a JSON case and report the result", func(ctx sdk.Context, args string) error {
		var c struct {
			Fn       string                 `json:"fn"`
			Usage    sdk.UsageInfo          `json:"usage"`
			Tokens   int                    `json:"tokens"`
			Window   int                    `json:"window"`
			Settings sdk.CompactionSettings `json:"settings"`
			Entries  []map[string]any       `json:"entries"`
			Message  map[string]any         `json:"message"`
			Start    int                    `json:"start"`
			End      int                    `json:"end"`
			Keep     int                    `json:"keep"`
			Index    int                    `json:"index"`
		}
		if err := json.Unmarshal([]byte(args), &c); err != nil {
			return err
		}
		var result any
		switch c.Fn {
		case "calc":
			result = map[string]any{"tokens": sdk.CalculateContextTokens(c.Usage)}
		case "should":
			result = map[string]any{"result": sdk.ShouldCompact(c.Tokens, c.Window, c.Settings)}
		case "last":
			if usage := sdk.GetLastAssistantUsage(c.Entries); usage != nil {
				result = map[string]any{"usage": usage}
			} else {
				result = map[string]any{"usage": nil}
			}
		case "cut":
			result = sdk.FindCutPoint(c.Entries, c.Start, c.End, c.Keep)
		case "turn":
			result = map[string]any{"index": sdk.FindTurnStartIndex(c.Entries, c.Index, c.Start)}
		case "estimate":
			result = map[string]any{"tokens": sdk.EstimateTokens(c.Message)}
		case "defaults":
			result = sdk.DefaultCompactionSettings
		default:
			return fmt.Errorf("unknown compaction helper %q", c.Fn)
		}
		wire, err := json.Marshal(result)
		if err != nil {
			return err
		}
		ctx.Notify("compact:"+string(wire)+":end", "info")
		return nil
	})
	// skills_probe runs a session or skill helper on a JSON case (TestConformance_SessionAndSkillHelpersAgreeAcrossSDKs).
	ext.Command("skills_probe", "Run a session or skill helper on a JSON case and report the result", func(ctx sdk.Context, args string) error {
		var c struct {
			Fn      string                  `json:"fn"`
			Entries []map[string]any        `json:"entries"`
			Text    string                  `json:"text"`
			Skills  []sdk.SystemPromptSkill `json:"skills"`
			Tool    *string                 `json:"tool"`
		}
		if err := json.Unmarshal([]byte(args), &c); err != nil {
			return err
		}
		var result any
		switch c.Fn {
		case "latest":
			result = map[string]any{"entry": sdk.GetLatestCompactionEntry(c.Entries)}
		case "parse":
			var block any
			if parsed := sdk.ParseSkillBlock(c.Text); parsed != nil {
				block = map[string]any{"name": parsed.Name, "location": parsed.Location, "content": parsed.Content, "userMessage": parsed.UserMessage}
			}
			result = map[string]any{"block": block}
		case "format":
			tool := "read"
			if c.Tool != nil {
				tool = *c.Tool
			}
			result = map[string]any{"text": sdk.FormatSkillsForPrompt(c.Skills, tool)}
		default:
			result = map[string]any{"version": sdk.CurrentSessionVersion, "virtual": sdk.VirtualModelStateEntry}
		}
		wire, err := json.Marshal(result)
		if err != nil {
			return err
		}
		ctx.Notify("skills:"+string(wire)+":end", "info")
		return nil
	})
	ext.Command("truncate_probe", "Run a truncation helper on a JSON case and report the result", func(ctx sdk.Context, args string) error {
		var c struct {
			Fn       string `json:"fn"`
			Content  string `json:"content"`
			MaxLines *int   `json:"maxLines"`
			MaxBytes *int   `json:"maxBytes"`
			MaxChars *int   `json:"maxChars"`
			Bytes    int    `json:"bytes"`
		}
		if err := json.Unmarshal([]byte(args), &c); err != nil {
			return err
		}
		options := sdk.TruncationOptions{MaxLines: c.MaxLines, MaxBytes: c.MaxBytes}
		var result any
		switch c.Fn {
		case "head", "tail":
			r := sdk.TruncateHead(c.Content, options)
			if c.Fn == "tail" {
				r = sdk.TruncateTail(c.Content, options)
			}
			var by any
			if r.TruncatedBy != "" {
				by = r.TruncatedBy
			}
			result = map[string]any{"content": r.Content, "truncated": r.Truncated, "truncatedBy": by, "totalLines": r.TotalLines, "totalBytes": r.TotalBytes,
				"outputLines": r.OutputLines, "outputBytes": r.OutputBytes, "lastLinePartial": r.LastLinePartial, "firstLineExceedsLimit": r.FirstLineExceedsLimit,
				"maxLines": r.MaxLines, "maxBytes": r.MaxBytes}
		case "line":
			maxChars := sdk.GrepMaxLineLength
			if c.MaxChars != nil {
				maxChars = *c.MaxChars
			}
			text, was := sdk.TruncateLine(c.Content, maxChars)
			result = map[string]any{"text": text, "wasTruncated": was}
		case "size":
			result = map[string]any{"text": sdk.FormatSize(c.Bytes)}
		case "limits":
			result = map[string]any{"maxLines": sdk.DefaultMaxLines, "maxBytes": sdk.DefaultMaxBytes}
		default:
			return fmt.Errorf("unknown truncation helper %q", c.Fn)
		}
		wire, err := json.Marshal(result)
		if err != nil {
			return err
		}
		ctx.Notify("truncate:"+string(wire)+":end", "info")
		return nil
	})
	registerBuiltinToolProbe(ext)
	// message_probe reads the event payload in its JSON argument with the SDK's messageRole and messageText helpers
	// (TestConformance_MessageHelpersAgreeAcrossSDKs).
	ext.Command("message_probe", "Report messageRole and messageText of a JSON event payload", func(ctx sdk.Context, args string) error {
		var data map[string]any
		if err := json.Unmarshal([]byte(args), &data); err != nil {
			return err
		}
		wire, err := json.Marshal(map[string]any{"role": sdk.MessageRole(data), "text": sdk.MessageText(data)})
		if err != nil {
			return err
		}
		ctx.Notify("message:"+string(wire)+":end", "info")
		return nil
	})
	// file_queue_probe runs the tasks in its JSON argument concurrently through the SDK's file mutation queue and reports the start/end log and the tasks
	// that failed (TestConformance_FileMutationQueueAgreesAcrossSDKs). Tasks are ordered by waiting on each other's progress, never by sleeping through a
	// race: waitStarted/waitCalled delay the call into the queue, holdEnded/holdCalled keep a task inside it. After a "called" signal a fixed settle time lets a
	// task that wrongly skipped the queue show itself.
	ext.Command("file_queue_probe", "Run tasks through the file mutation queue and report the log", func(ctx sdk.Context, args string) error {
		type spec struct {
			ID          int    `json:"id"`
			Path        string `json:"path"`
			Fail        bool   `json:"fail"`
			WaitStarted []int  `json:"waitStarted"`
			WaitCalled  []int  `json:"waitCalled"`
			HoldEnded   []int  `json:"holdEnded"`
			HoldCalled  []int  `json:"holdCalled"`
		}
		var c struct {
			SettleMs int    `json:"settleMs"`
			Tasks    []spec `json:"tasks"`
		}
		if err := json.Unmarshal([]byte(args), &c); err != nil {
			return err
		}
		var mu sync.Mutex
		log := []string{}
		failed := []int{}
		progress := map[string]bool{}
		mark := func(kind string, id int) {
			mu.Lock()
			progress[fmt.Sprintf("%s:%d", kind, id)] = true
			mu.Unlock()
		}
		// await polls until every id reached the stage, up to 20s; a timeout is logged so the test fails with a readable log instead of hanging.
		await := func(kind string, ids []int, who int) {
			for _, id := range ids {
				deadline := time.Now().Add(20 * time.Second)
				for {
					mu.Lock()
					ok := progress[fmt.Sprintf("%s:%d", kind, id)]
					mu.Unlock()
					if ok {
						break
					}
					if time.Now().After(deadline) {
						mu.Lock()
						log = append(log, fmt.Sprintf("timeout:%d:%s:%d", who, kind, id))
						mu.Unlock()
						return
					}
					time.Sleep(time.Millisecond)
				}
			}
		}
		settle := func(ids []int) {
			if len(ids) > 0 {
				time.Sleep(time.Duration(c.SettleMs) * time.Millisecond)
			}
		}
		var wg sync.WaitGroup
		for _, task := range c.Tasks {
			wg.Go(func() {
				await("started", task.WaitStarted, task.ID)
				await("called", task.WaitCalled, task.ID)
				settle(task.WaitCalled)
				mark("called", task.ID)
				_, err := sdk.WithFileMutationQueue(task.Path, func() (struct{}, error) {
					mu.Lock()
					log = append(log, fmt.Sprintf("start:%d", task.ID))
					mu.Unlock()
					mark("started", task.ID)
					await("ended", task.HoldEnded, task.ID)
					await("called", task.HoldCalled, task.ID)
					settle(task.HoldCalled)
					mu.Lock()
					log = append(log, fmt.Sprintf("end:%d", task.ID))
					mu.Unlock()
					mark("ended", task.ID)
					if task.Fail {
						return struct{}{}, errors.New("task failed")
					}
					return struct{}{}, nil
				})
				if err != nil {
					mark("ended", task.ID)
					mu.Lock()
					failed = append(failed, task.ID)
					mu.Unlock()
				}
			})
		}
		wg.Wait()
		slices.Sort(failed)
		wire, err := json.Marshal(map[string]any{"log": log, "errors": failed})
		if err != nil {
			return err
		}
		ctx.Notify("file_queue:"+string(wire)+":end", "info")
		return nil
	})
	// host_info_probe reports Pi's getAgentDir(), CONFIG_DIR_NAME and VERSION as this SDK answers them (TestConformance_HostConfigurationAgreesAcrossSDKs).
	ext.Command("host_info_probe", "Report getAgentDir, CONFIG_DIR_NAME and VERSION", func(ctx sdk.Context, args string) error {
		dir, err := sdk.AgentDir()
		if err != nil {
			return err
		}
		name, err := sdk.ConfigDirName()
		if err != nil {
			return err
		}
		version, err := sdk.Version()
		if err != nil {
			return err
		}
		wire, err := json.Marshal(map[string]string{"agentDir": dir, "configDirName": name, "version": version, "factoryAgentDir": factoryAgentDir})
		if err != nil {
			return err
		}
		ctx.Notify("host_info:"+string(wire)+":end", "info")
		return nil
	})
	// ui_dialog_values_probe reports the value each dialog resolves with (TestConformance_UIDialogValuesAgreeAcrossSDKs): `<undefined>` for Pi's undefined, else the JSON of the value.
	ext.Command("ui_dialog_values_probe", "Report the typed result of each dialog", func(ctx sdk.Context, args string) error {
		report := func(name string, value any, ok bool) {
			text := "<undefined>"
			if ok {
				encoded, _ := json.Marshal(value)
				text = string(encoded)
			}
			ctx.Notify(name+"="+text, "info")
		}
		for _, title := range []string{"S1", "S2", "S3", "S4"} {
			value, ok, err := ctx.Select(title, []string{"a", "b"})
			if err != nil {
				return err
			}
			report(title, value, ok)
		}
		for _, title := range []string{"C1", "C2", "C3", "C4"} {
			value, err := ctx.Confirm(title, "m")
			if err != nil {
				return err
			}
			report(title, value, true)
		}
		for _, title := range []string{"I1", "I2"} {
			value, ok, err := ctx.Input(title, "p")
			if err != nil {
				return err
			}
			report(title, value, ok)
		}
		for _, title := range []string{"E1", "E2"} {
			value, ok, err := ctx.Editor(title, "p")
			if err != nil {
				return err
			}
			report(title, value, ok)
		}
		return nil
	})
	// ui_forward_probe calls the UI members whose host effect a conformance row records (TestConformance_UIForwardingAgreesAcrossSDKs).
	ext.Command("ui_forward_probe", "Call the working, paste, editor-text and tools-expanded UI members", func(ctx sdk.Context, args string) error {
		ctx.SetWorkingMessage("Thinking hard")
		ctx.SetWorkingMessage("")
		ctx.ClearWorkingMessage()
		_ = ctx.SetHiddenThinkingLabel("Pondering")
		_ = ctx.SetHiddenThinkingLabel("")
		_ = ctx.ClearHiddenThinkingLabel()
		ctx.SetWorkingVisible(false)
		frames := []string{"-", "+"}
		if err := ctx.SetWorkingIndicator(sdk.WorkingIndicatorOptions{"frames": frames, "intervalMs": 120.0}); err != nil {
			return err
		}
		ctx.PasteToEditor("pasted \U0001F600")
		ctx.SetEditorText("draft")
		ctx.SetToolsExpanded(true)
		return nil
	})
	// dialog-stack-probe opens its dialogs at the same time, each from its own goroutine, and reports each one's answer as it settles
	// (TestConformance_ConcurrentDialogsAgreeAcrossSDKs). "pair" is a select A and a confirm B; "mix" is a select S, a confirm C, an input I and an editor E.
	ext.Command("dialog-stack-probe", "Open concurrent dialogs from one command", func(ctx sdk.Context, scenario string) error {
		answer := func(value any, ok bool) string {
			if !ok {
				return "cancelled"
			}
			return fmt.Sprint(value)
		}
		dialogs := map[string]func() (string, error){
			"A": func() (string, error) {
				value, ok, err := ctx.Select("A", []string{"a1", "a2"})
				return answer(value, ok), err
			},
			"B": func() (string, error) {
				confirmed, err := ctx.Confirm("B", "message")
				return fmt.Sprint(confirmed), err
			},
			"S": func() (string, error) {
				value, ok, err := ctx.Select("S", []string{"s1", "s2"})
				return answer(value, ok), err
			},
			"C": func() (string, error) {
				confirmed, err := ctx.Confirm("C", "message")
				return fmt.Sprint(confirmed), err
			},
			"I": func() (string, error) { value, ok, err := ctx.Input("I", "placeholder"); return answer(value, ok), err },
			"E": func() (string, error) { value, ok, err := ctx.Editor("E", "prefill"); return answer(value, ok), err },
		}
		names := []string{"A", "B"}
		if scenario == "mix" {
			names = []string{"S", "C", "I", "E"}
		}
		errs := make([]error, len(names))
		var wg sync.WaitGroup
		for index, name := range names {
			wg.Go(func() {
				value, err := dialogs[name]()
				if err != nil {
					errs[index] = err
					return
				}
				ctx.Notify("stack settled "+name+"="+value, "info")
			})
		}
		wg.Wait()
		if err := errors.Join(errs...); err != nil {
			return err
		}
		ctx.Notify("stack done", "info")
		return nil
	})
	// ui_wire_empties_probe calls the UI members Pi's RPC mode writes with empty strings and lists, and the optional members it omits, in this SDK's own spelling of
	// each (TestConformance_UIEmptyMembersAgreeAcrossSDKs).
	ext.Command("ui_wire_empties_probe", "Call the UI members with empty and unset arguments", func(ctx sdk.Context, args string) error {
		ctx.SetStatus("k", "")
		ctx.ClearStatus("k")
		ctx.Notify("", "")
		ctx.Notify("typed", "warning")
		ctx.SetTitle("")
		ctx.SetEditorText("")
		_, _, _ = ctx.Select("T", []string{})
		_, _ = ctx.Confirm("", "")
		_, _, _ = ctx.Input("T1", "")
		_, _, _ = ctx.Input("T3", "ph")
		_, _, _ = ctx.Editor("E1", "")
		_, _, _ = ctx.Editor("E3", "pre")
		return nil
	})
	// tool_call_guard_probe reports which tool names isToolCallEventType accepts for each tool_call event it receives (TestConformance_ToolCallGuardAgreesAcrossSDKs).
	ext.Command("tool_call_guard_probe", "Report the tool names the tool_call guard accepts", func(ctx sdk.Context, args string) error {
		ext.OnEvent("tool_call", func(hctx sdk.Context, data map[string]any) (any, error) {
			var accepted []string
			for _, name := range []string{"bash", "read", "probe", "other"} {
				if sdk.IsToolCallEventType(name, data) {
					accepted = append(accepted, name)
				}
			}
			hctx.Notify("tool_call_guard:"+strings.Join(accepted, ",")+":end", "info")
			return nil, nil
		})
		return nil
	})
	ext.Command("editor-clear", "Restore the host's editor", func(ctx sdk.Context, args string) error {
		installedEditorFactory = nil
		return ctx.SetEditorComponent(nil)
	})

	// The factory already calls super: the host answers once its editor is ready.
	ext.Command("editor-install-eager", "Install a custom editor that calls super in its factory", func(ctx sdk.Context, args string) error {
		return ctx.SetEditorComponent(func(base *sdk.Editor) sdk.EditorComponent {
			if err := base.SetText("eager"); err != nil {
				ctx.Notify("eager-error:"+err.Error(), "info")
			} else if text, err := base.GetText(); err != nil {
				ctx.Notify("eager-error:"+err.Error(), "info")
			} else {
				ctx.Notify("eager:"+text, "info")
			}
			return &conformanceEditor{Editor: base}
		})
	})
	ext.Command("editor-install-embed", "Install a custom editor that embeds the working status", func(ctx sdk.Context, args string) error {
		return ctx.SetEditorComponent(func(base *sdk.Editor) sdk.EditorComponent { return &embeddingEditor{conformanceEditor{Editor: base}} })
	})
	registerHeadlessUIProbe(ext)

	ext.Command("ui-probe", "Exercise SDK UI wrappers", func(ctx sdk.Context, args string) error {
		_ = ctx.SetWorkingIndicator(sdk.WorkingIndicatorOptions{"frames": []string{"*"}})
		_ = ctx.SetHiddenThinkingLabel("hidden-thoughts")
		_ = ctx.SetFooter(nil)
		_ = ctx.SetHeader(nil)
		_ = ctx.SetEditorComponent(nil)
		_ = ctx.SetWidget("status", []string{"sdk-fixture: ui-probe"})
		_ = ctx.SetWidget("status-call", []string{"sdk-fixture: ui-probe call"}, sdk.WidgetOptions{"position": "above"})

		themes, themesErr := ctx.GetAllThemes()
		if themesErr != nil {
			return themesErr
		}
		theme, themeErr := ctx.GetTheme("dark")
		_, customErr := ctx.Custom(nil, nil)
		autoErr := ctx.AddAutocompleteProvider(nil)
		// Consume the sentinel only, so the host test can prove both that a
		// subscribed extension suppresses a chunk and that it lets others by.
		_, termErr := ctx.OnTerminalInput(func(data string) sdk.TerminalInputResult {
			return sdk.TerminalInputResult{Consume: data == "\x1b[99~"}
		})

		summary, _ := json.Marshal(map[string]any{
			"themesCount": len(themes),
			"theme":       theme,
			"themeErr":    errString(themeErr),
			"customErr":   errString(customErr),
			"autoErr":     errString(autoErr),
			"termErr":     errString(termErr),
		})
		ctx.Notify(string(summary), "info")
		return nil
	})

	ext.Command("agent-probe", "Exercise SDK agent-control wrappers", func(ctx sdk.Context, args string) error {
		idle, err := ctx.IsIdle()
		if err != nil {
			return err
		}
		pending, err := ctx.HasPendingMessages()
		if err != nil {
			return err
		}
		ctx.Compact(nil)

		summary, _ := json.Marshal(map[string]any{
			"idle":    idle,
			"pending": pending,
		})
		ctx.Notify(string(summary), "info")
		return nil
	})

	ext.Command("session-probe", "Exercise SDK session-control wrappers", func(ctx sdk.Context, args string) error {
		waitErr := ctx.WaitForIdle()
		reloadErr := ctx.Reload()

		summary, _ := json.Marshal(map[string]any{
			"waitErr":   errString(waitErr),
			"reloadErr": errString(reloadErr),
		})
		ctx.Notify(string(summary), "info")
		return nil
	})

	ext.OnProjectTrust(func(sdk.Context, map[string]any) (sdk.ProjectTrustResult, error) {
		return sdk.ProjectTrustResult{}, errors.New("trust-boom")
	})
	ext.OnProjectTrust(func(sdk.Context, map[string]any) (sdk.ProjectTrustResult, error) {
		return sdk.ProjectTrustResult{Trusted: sdk.ProjectTrustUndecided}, nil
	})
	// The decisive handler leaves the "/probe" cwd undecided, so TestConformance_EventUnsubscribeEveryEvent reaches the handlers registered after connecting.
	ext.OnProjectTrust(func(_ sdk.Context, data map[string]any) (sdk.ProjectTrustResult, error) {
		if data["cwd"] == "/probe" {
			return sdk.ProjectTrustResult{Trusted: sdk.ProjectTrustUndecided}, nil
		}
		return sdk.ProjectTrustResult{Trusted: sdk.ProjectTrustYes, Remember: true}, nil
	})
	ext.OnEvent("cache_warming_decision", func(_ sdk.Context, data map[string]any) (any, error) {
		if data["warmCost"] != 0.05 || data["missCost"] != 0.5 || data["continuationProbability"] != 0.15 || data["action"] != "warm" {
			return nil, fmt.Errorf("unexpected cache decision: %v", data)
		}
		return map[string]any{"action": "stop"}, nil
	})
	ext.OnEvent("agent_before_settle", func(ctx sdk.Context, data map[string]any) (any, error) {
		entries, _ := data["entries"].([]any)
		preview, _ := data["context"].(map[string]any)
		contextEntries, _ := preview["contextEntries"].([]any)
		ctx.Notify(fmt.Sprintf("agent_before_settle:%v:%d:%v:%d:%v", data["outcome"], len(entries), data["continue"], len(contextEntries), preview["canContinue"]), "info")
		data["entries"] = append(entries, map[string]any{"type": "custom", "customType": "kept"})
		return nil, nil
	})
	ext.OnEvent("agent_before_settle", func(_ sdk.Context, data map[string]any) (any, error) {
		entries := data["entries"].([]any)
		data["entries"] = append(entries, map[string]any{"type": "custom", "customType": "before-error"})
		return nil, errors.New("boundary failed")
	})
	ext.OnEvent("agent_before_settle", func(_ sdk.Context, data map[string]any) (any, error) {
		entries := data["entries"].([]any)
		preview := data["context"].(map[string]any)
		if len(entries) != 2 || len(preview["contextEntries"].([]any)) != 2 || entries[0].(map[string]any)["customType"] != "kept" || entries[1].(map[string]any)["customType"] != "before-error" {
			return nil, errors.New("lost boundary mutation or preview")
		}
		return map[string]any{
			"entries":  []any{map[string]any{"type": "custom", "customType": "conformance-boundary"}},
			"continue": true,
		}, nil
	})
	ext.OnSessionStart(func(ctx sdk.Context, data map[string]any) (any, error) {
		ctx.Notify("session_start:"+fmt.Sprint(data["reason"]), "info")
		if path, ok := data["previousSessionFile"].(string); ok && path != "" {
			ctx.Notify("previous:"+path, "info")
		}
		return nil, nil
	})
	ext.OnSessionShutdown(func(ctx sdk.Context, data map[string]any) (any, error) {
		ctx.Notify("session_shutdown:"+fmt.Sprint(data["reason"]), "info")
		if path, ok := data["targetSessionFile"].(string); ok && path != "" {
			ctx.Notify("target:"+path, "info")
		}
		return nil, nil
	})
	ext.OnEvent("session_info_changed", func(ctx sdk.Context, data map[string]any) (any, error) {
		ctx.Notify("session_info_changed:"+fmt.Sprint(data["name"]), "info")
		return nil, nil
	})
	ext.OnEvent("session_before_compact", func(ctx sdk.Context, data map[string]any) (any, error) {
		ctx.Notify(fmt.Sprintf("session_before_compact:%v:%v", data["reason"], data["willRetry"]), "info")
		return nil, nil
	})
	ext.OnEvent("session_compact", func(ctx sdk.Context, data map[string]any) (any, error) {
		ctx.Notify(fmt.Sprintf("session_compact:%v:%v:%v", data["reason"], data["willRetry"], data["fromExtension"]), "info")
		return nil, nil
	})

	ext.OnEvent("session_compact_failed", func(ctx sdk.Context, data map[string]any) (any, error) {
		ctx.Notify(fmt.Sprintf("session_compact_failed:%v:%v:%v:%v:%v", data["reason"], data["errorMessage"], data["aborted"], data["willRetry"], data["fromExtension"]), "info")
		return nil, nil
	})
	ext.OnEvent("turn_end", func(_ sdk.Context, data map[string]any) (any, error) {
		if data["messageEntryId"] != "boundary-assistant" {
			return nil, nil
		}
		data["entries"] = append(data["entries"].([]any), map[string]any{"type": "custom", "customType": "mutated"})
		return nil, errors.New("turn-boundary-failure")
	})
	ext.OnEvent("turn_end", func(_ sdk.Context, data map[string]any) (any, error) {
		if data["messageEntryId"] != "boundary-assistant" {
			return nil, nil
		}
		return map[string]any{"entries": []any{map[string]any{"type": "custom", "customType": "turn-boundary", "data": data}}, "continue": true}, nil
	})
	ext.OnEvent("turn_end", func(ctx sdk.Context, data map[string]any) (any, error) {
		ids, _ := data["toolResultEntryIds"].([]any)
		var firstID any
		if len(ids) > 0 {
			firstID = ids[0]
		}
		ctx.Notify(fmt.Sprintf("turn_end:%v:%v", data["messageEntryId"], firstID), "info")
		return nil, nil
	})
	var promptMu sync.Mutex
	promptSequence := 0
	for _, event := range []string{"ui_prompt_start", "ui_prompt_end"} {
		ext.OnEvent(event, func(ctx sdk.Context, data map[string]any) (any, error) {
			promptMu.Lock()
			sequence := promptSequence
			promptSequence++
			promptMu.Unlock()
			if title, _ := data["title"].(string); strings.HasPrefix(title, "fifo:") {
				ctx.Notify(fmt.Sprintf("fifo:%d:%v:%s", sequence, data["type"], title), "info")
				return nil, nil
			}
			title := "(none)"
			if value, ok := data["title"]; ok {
				title = fmt.Sprint(value)
			}
			ctx.Notify(fmt.Sprintf("ui_prompt:%v:%v:%v:%s", data["type"], data["reason"], data["kind"], title), "info")
			return nil, nil
		})
	}

	ext.OnEvent("user_bash", func(_ sdk.Context, event map[string]any) (any, error) {
		result := map[string]any{"output": "handled", "exitCode": 7, "cancelled": false, "truncated": false}
		value := map[string]any{"result": result}
		switch event["command"] {
		case "valid":
		case "undefined":
			result["exitCode"] = nil
		case "undefined-path":
			result["fullOutputPath"] = nil
		case "missing":
			delete(result, "exitCode")
		case "invalid":
			result["exitCode"] = "invalid"
		case "null-operations":
			value["operations"] = nil
		case "operations":
			return map[string]any{"operations": conformanceBashOperations()}, nil
		default:
			return nil, nil
		}
		return value, nil
	})

	registerConformanceOAuth(ext)
	registerConformanceOAuthObject(ext)
	registerConformanceOAuthLarge(ext)

	// Typed tool events use one shared fixture for subprocess and fused Go.
	ext.OnEvent("tool_call", func(ctx sdk.Context, event map[string]any) (any, error) {
		name, _ := event["toolName"].(string)
		// Pi's handler mutates event.input in place and the runner reads it back (runner.ts emitToolCall), so the rewrite is the handler's own edit to the event.
		// Assigning a new map to the event's input leaves the object the tool runs with (agent-loop.ts prepareToolCall).
		if name == "rewrite_reassign_probe" {
			input, _ := event["input"].(map[string]any)
			event["input"] = map[string]any{"command": "git status --short", "timeout": input["timeout"]}
			return nil, nil
		}
		if name == "rewrite_probe" || name == "rewrite_block_probe" {
			input, _ := event["input"].(map[string]any)
			if input["command"] == "git status" || name == "rewrite_block_probe" {
				input["command"] = "git status --short"
				delete(input, "drop")
				input["added"] = true
				input["nested"] = map[string]any{"depth": 2.0}
			}
			if name == "rewrite_block_probe" {
				return map[string]any{"block": true, "reason": "blocked after rewrite"}, nil
			}
			return nil, nil
		}
		if name != "powershell" && name != "bash" {
			return nil, nil
		}
		input, _ := event["input"].(map[string]any)
		ctx.Notify(fmt.Sprintf("tool-call=%s:%v:%v", name, input["command"], input["timeout"]), "info")
		if input["command"] == "blocked-command" {
			return map[string]any{"block": true, "reason": "blocked " + name}, nil
		}
		return nil, nil
	})
	ext.OnEvent("tool_result", func(ctx sdk.Context, event map[string]any) (any, error) {
		if event["toolName"] == "order_probe" {
			ctx.Notify(fmt.Sprintf("order-result=%v", event["toolCallId"]), "info")
			return nil, nil
		}
		var held []string
		for _, guard := range []struct {
			name string
			is   func(map[string]any) bool
		}{{"bash", sdk.IsBashToolResult}, {"powershell", sdk.IsPowerShellToolResult}, {"read", sdk.IsReadToolResult}, {"edit", sdk.IsEditToolResult}, {"write", sdk.IsWriteToolResult}, {"grep", sdk.IsGrepToolResult}, {"find", sdk.IsFindToolResult}, {"ls", sdk.IsLsToolResult}} {
			if guard.is(event) {
				held = append(held, guard.name)
			}
		}
		ctx.Notify("tool-result-guards="+strings.Join(held, ","), "info")
		name, _ := event["toolName"].(string)
		if name != "powershell" && name != "bash" {
			return nil, nil
		}
		details, _ := event["details"].(map[string]any)
		truncation, _ := details["truncation"].(map[string]any)
		content, _ := event["content"].([]any)
		var text any
		if len(content) > 0 {
			first, _ := content[0].(map[string]any)
			text = first["text"]
		}
		ctx.Notify(fmt.Sprintf("tool-result=%s:%v:%v:%v", name, details["fullOutputPath"], truncation["totalLines"], text), "info")
		return map[string]any{"content": []any{map[string]any{"type": "text", "text": name + " redacted"}}}, nil
	})

	ext.OnEvent("after_provider_response", func(ctx sdk.Context, event map[string]any) (any, error) {
		headers, _ := event["headers"].(map[string]any)
		ctx.Notify(fmt.Sprintf("provider-response=%v:%v:%v", event["type"], event["status"], headers["x-probe"]), "info")
		return map[string]any{"cancel": true}, nil
	})
	ext.OnEvent("after_provider_response", func(ctx sdk.Context, _ map[string]any) (any, error) {
		ctx.Notify("provider-response=second", "info")
		return nil, nil
	})

	ext.OnEvent("message_update", func(ctx sdk.Context, event map[string]any) (any, error) {
		assistant, _ := event["assistantMessageEvent"].(map[string]any)
		_, nested := assistant["assistantMessageEvent"]
		ctx.Notify(fmt.Sprintf("message-update=%v:%v:%v:%t", assistant["type"], assistant["contentIndex"], assistant["delta"], nested), "info")
		return nil, nil
	})
	ext.OnEvent("tool_execution_update", func(ctx sdk.Context, event map[string]any) (any, error) {
		partial, _ := event["partialResult"].(map[string]any)
		details, _ := partial["details"].(map[string]any)
		if event["toolName"] == "production_tool" {
			args, _ := event["args"].(map[string]any)
			nested, _ := args["nested"].(map[string]any)
			ctx.Notify(fmt.Sprintf("tool-update=%v:%v:%v:%v:%v", event["toolName"], args["path"], nested["depth"], firstPartialText(partial), details["progress"]), "info")
			return nil, nil
		}
		args, _ := json.Marshal(event["args"])
		ctx.Notify(fmt.Sprintf("tool-update=%v:%s:%v:%v", event["toolName"], args, firstPartialText(partial), details["progress"]), "info")
		return nil, nil
	})
	ext.OnEvent("tool_execution_end", func(ctx sdk.Context, event map[string]any) (any, error) {
		result, _ := event["result"].(map[string]any)
		content, _ := result["content"].([]any)
		var image map[string]any
		if len(content) > 1 {
			image, _ = content[1].(map[string]any)
		}
		details, _ := result["details"].(map[string]any)
		nested, _ := details["nested"].(map[string]any)
		if event["toolName"] == "production_tool" {
			var text map[string]any
			if len(content) > 0 {
				text, _ = content[0].(map[string]any)
			}
			ctx.Notify(fmt.Sprintf("tool-end=%v:%v:%v:%v:%v:%v:%v", event["toolName"], text["text"], len(content), image["data"], image["mimeType"], nested["value"], event["isError"]), "info")
			return nil, nil
		}
		ctx.Notify(fmt.Sprintf("tool-end=%v:%v:%v:%v:%v", event["toolName"], len(content), image["data"], nested["value"], event["isError"]), "info")
		return nil, nil
	})

	registerStatePushProbe(ext)
	registerSignalAbort(ext)

	return ext
}

// registerConformanceOAuth contributes the canonical OAuth provider the
// cross-transport conformance suite drives. Its behavior must match the Rust
// and Python fixtures byte-for-byte so the OAuth recordings compare equal.
func conformanceLoginDefinition() sdk.LoginDefinition {
	return sdk.LoginDefinition{
		Brand:       repeatRow("A", 41, 5),
		Hero:        repeatRow("A", 32, 14),
		Mascot:      repeatRow("A", 16, 14),
		Palette:     map[string]string{"A": "#123ABC"},
		Name:        "Conformance Pig",
		Description: "Cross-language login fixture",
		Tagline:     "One canonical definition across every SDK",
	}
}

func conformanceSpriteDefinition() sdk.SpriteDefinition {
	return sdk.SpriteDefinition{
		ID:      "conformance-pig",
		Name:    "Conformance Pig",
		Tagline: "One canonical sprite across every SDK",
		Mascot:  repeatRow("A", 16, 14),
		Palette: map[string]string{"A": "#123ABC"},
	}
}

func repeatRow(symbol string, width, height int) []string {
	rows := make([]string, height)
	for i := range rows {
		rows[i] = strings.Repeat(symbol, width)
	}
	return rows
}

func registerConformanceOAuth(ext *sdk.Extension) {
	ext.RegisterProvider("conformance-oauth", sdk.ProviderConfig{
		"name": "Conformance OAuth",
		"oauth": &sdk.OAuthProvider{
			Name:           "Conformance OAuth",
			IsSubscription: true,
			Login: func(cb *sdk.OAuthLoginCallbacks) (sdk.OAuthCredentials, error) {
				cb.OnDeviceCode(sdk.OAuthDeviceCodeInfo{
					UserCode:        "CONF-USER-CODE",
					VerificationURI: "https://conf.example/verify",
				})
				cb.OnProgress("waiting")
				value, err := cb.OnPrompt(sdk.OAuthPrompt{Message: "paste the code"})
				if err != nil {
					return sdk.OAuthCredentials{}, err
				}
				return sdk.OAuthCredentials{Access: "access-" + value, Refresh: "refresh-tok", Expires: 4242, AccountID: "account-login", Scope: "scope-login"}, nil
			},
			RefreshToken: func(creds sdk.OAuthCredentials) (sdk.OAuthCredentials, error) {
				return sdk.OAuthCredentials{Access: "refreshed-" + creds.Refresh, Refresh: creds.Refresh, Expires: 9999, AccountID: creds.AccountID, Scope: creds.Scope}, nil
			},
			GetAPIKey: func(creds sdk.OAuthCredentials) string {
				if creds.Access == "boom" {
					panic("getApiKey exploded")
				}
				return "key:" + creds.Access
			},
			CredentialStore: conformanceStore{},
		},
	})
}

// registerConformanceOAuthObject contributes a provider whose callbacks treat credentials as Pi's complete token object: login returns a fractional expiry and provider-owned keys, and refresh spreads its input as `{ ...creds, access, expires }` does. Behavior must match the Rust, Python and Node fixtures.
func registerConformanceOAuthObject(ext *sdk.Extension) {
	ext.RegisterProvider("conformance-oauth-object", sdk.ProviderConfig{
		"name": "Conformance OAuth Object",
		"oauth": &sdk.OAuthProvider{
			Name: "Conformance OAuth Object",
			Login: func(*sdk.OAuthLoginCallbacks) (sdk.OAuthCredentials, error) {
				creds := sdk.OAuthCredentials{Access: "object-access", Refresh: "object-refresh", Extra: map[string]json.RawMessage{"meta": json.RawMessage(`{"k":[1,null,""]}`), "projectId": json.RawMessage(`""`)}}
				creds.SetExpiresMillis(1700000000000.25)
				return creds, nil
			},
			RefreshToken: func(creds sdk.OAuthCredentials) (sdk.OAuthCredentials, error) {
				next := creds
				next.Access = "refreshed-" + creds.Refresh
				expires, _ := creds.ExpiresMillis()
				next.SetExpiresMillis(expires + 0.5)
				return next, nil
			},
			GetAPIKey: func(creds sdk.OAuthCredentials) string {
				typed, meta := "untyped", "nometa"
				if string(creds.Extra["type"]) == `"oauth"` {
					typed = "typed"
				}
				if _, ok := creds.Extra["meta"]; ok {
					meta = "meta"
				}
				return "key:" + typed + ":" + meta
			},
		},
	})
}

// registerConformanceOAuthLarge contributes a provider whose expiry is the double 2**60. JSON.parse reads the wire digits 1152921504606847000 as that double, so every SDK reports getApiKey's distance from 2**60 as 0; an SDK that keeps the exact integer reports 24. Behavior must match the Rust, Python and Node fixtures.
func registerConformanceOAuthLarge(ext *sdk.Extension) {
	ext.RegisterProvider("conformance-oauth-large", sdk.ProviderConfig{
		"name": "Conformance OAuth Large",
		"oauth": &sdk.OAuthProvider{
			Name: "Conformance OAuth Large",
			Login: func(*sdk.OAuthLoginCallbacks) (sdk.OAuthCredentials, error) {
				creds := sdk.OAuthCredentials{Access: "large-access", Refresh: "large-refresh"}
				creds.SetExpiresMillis(1 << 60)
				return creds, nil
			},
			GetAPIKey: func(creds sdk.OAuthCredentials) string {
				expires, _ := creds.ExpiresMillis()
				return fmt.Sprintf("key:%.0f:%d", expires-(1<<60), creds.Expires-(1<<60))
			},
		},
	})
}

type conformanceStore struct{}

func (conformanceStore) CredentialStatus() sdk.OAuthCredentialStatus {
	return sdk.OAuthCredentialStatus{Present: true, AuthType: "oauth", Source: "conformance"}
}

func (conformanceStore) StoreCredentials(creds sdk.OAuthCredentials) (string, error) {
	if creds.AccountID != "account-store" || creds.Scope != "scope-store" {
		return "", fmt.Errorf("credential metadata lost: %#v", creds)
	}
	return "/conf/creds.json", nil
}

func (conformanceStore) DeleteCredentials() (bool, error) {
	return true, nil
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// firstPartialText is the text of the first content block of a tool_execution_update partialResult, the AgentToolResult the tool passed to onUpdate (agent-loop.ts:778-786); absent when `content` is not an array of blocks.
func firstPartialText(partial map[string]any) any {
	blocks, _ := partial["content"].([]any)
	if len(blocks) == 0 {
		return nil
	}
	block, _ := blocks[0].(map[string]any)
	return block["text"]
}

// conformanceEditor swallows "q", rewrites "a" to "A", upper-cases the host's setText, and frames super.render.
type conformanceEditor struct{ *sdk.Editor }

func (e *conformanceEditor) HandleInput(data string) error {
	switch data {
	case "q":
		return nil
	case "a":
		data = "A"
	case "L":
		lines, err := e.GetLines()
		if err != nil {
			return err
		}
		return e.Editor.SetText("raw:lines=" + strings.Join(lines, "|"))
	case "K":
		// The default editor's queries and setters (TestEditorComponentGoBaseQueriesReachTheHost): the answers come back as text.
		cursor, err := e.GetCursor()
		if err != nil {
			return err
		}
		expanded, err := e.GetExpandedText()
		if err != nil {
			return err
		}
		showing, err := e.IsShowingAutocomplete()
		if err != nil {
			return err
		}
		if err := e.SetPaddingX(3); err != nil {
			return err
		}
		if err := e.SetAutocompleteMaxVisible(7); err != nil {
			return err
		}
		return e.Editor.SetText(fmt.Sprintf("raw:cursor=%d,%d|expanded=%s|autocomplete=%t", cursor.Line, cursor.Col, expanded, showing))
	}
	return e.Editor.HandleInput(data)
}

// InsertTextAtCursor wraps the host's text in angle brackets and AddToHistory prefixes it, unless it starts with "raw:" (TestEditorComponentOverridesReachSuper).
func (e *conformanceEditor) InsertTextAtCursor(text string) error {
	if strings.HasPrefix(text, "raw:") {
		return e.Editor.InsertTextAtCursor(text)
	}
	return e.Editor.InsertTextAtCursor("<" + text + ">")
}

func (e *conformanceEditor) AddToHistory(text string) error {
	if strings.HasPrefix(text, "raw:") {
		return e.Editor.AddToHistory(text)
	}
	return e.Editor.AddToHistory("history:" + text)
}

func (e *conformanceEditor) SetText(text string) error {
	if strings.HasPrefix(text, "raw:") {
		return e.Editor.SetText(text)
	}
	return e.Editor.SetText(strings.ToUpper(text))
}

// embeddingEditor opts into the working status in its border.
type embeddingEditor struct{ conformanceEditor }

func (*embeddingEditor) EmbedWorkingStatus() bool { return true }

func (e *conformanceEditor) Render(width int) ([]string, error) {
	rows, err := e.Editor.Render(width)
	return append([]string{"[custom editor]"}, rows...), err
}

// conformanceBashOperations is the BashOperations every SDK fixture returns for the user_bash command "operations"
// (TestConformance_UserBashOperationsRunInTheExtension). Its exec is driven by the command it receives.
func conformanceBashOperations() sdk.BashOperations {
	return sdk.BashOperations{Exec: func(command, cwd string, options sdk.BashExecOptions) (sdk.BashExecResult, error) {
		code := func(n int) (sdk.BashExecResult, error) { return sdk.BashExecResult{ExitCode: &n}, nil }
		switch command {
		case "echo":
			options.OnData([]byte("cmd:" + command + "\n"))
			options.OnData([]byte("cwd:" + cwd + "\n"))
			if options.Env != nil && len(options.Env) == 0 {
				options.OnData([]byte("env-empty\n"))
			}
			for _, name := range slices.Sorted(maps.Keys(options.Env)) {
				options.OnData([]byte("env:" + name + "=" + options.Env[name] + "\n"))
			}
			if options.Timeout != nil {
				options.OnData([]byte("timeout:" + strconv.FormatFloat(*options.Timeout, 'f', -1, 64) + "\n"))
			}
			return code(3)
		case "chunks":
			for _, chunk := range []string{"a", "b", "c"} {
				options.OnData([]byte(chunk))
			}
			return sdk.BashExecResult{}, nil
		case "binary":
			options.OnData([]byte{0xff, 0x00, 0x80})
			return code(0)
		case "wait":
			options.OnData([]byte("waiting"))
			<-options.Signal.Done()
			options.OnData([]byte("stopped"))
			return sdk.BashExecResult{}, errors.New("aborted")
		default:
			return sdk.BashExecResult{}, errors.New("exec failed: " + command)
		}
	}}
}

type handleProbeComponent struct{}

func (*handleProbeComponent) Render(int) []string { return []string{"overlay"} }
func (*handleProbeComponent) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	return sdk.RemoteComponentResult{Done: data == "q", Value: "closed"}, nil
}

// focusProbeComponent renders the focus its overlay handle reports. The first frame renders before the handle exists.
type focusProbeComponent struct {
	handle atomic.Pointer[sdk.OverlayHandle]
}

func (c *focusProbeComponent) Render(int) []string {
	handle := c.handle.Load()
	if handle == nil {
		return []string{"focused=pending"}
	}
	return []string{fmt.Sprintf("focused=%t", handle.IsFocused())}
}

func (*focusProbeComponent) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	return sdk.RemoteComponentResult{Done: data == "q", Value: "closed"}, nil
}

// objectKeyLists is the depth-first list of every object's keys, joined with commas, in the order the JSON gives them: what Object.keys returns for each
// object of the value, which a Go map would lose.
func objectKeyLists(raw json.RawMessage) ([]string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	out := []string{}
	var walk func() error
	walk = func() error {
		token, err := dec.Token()
		if err != nil {
			return err
		}
		delim, isDelim := token.(json.Delim)
		if !isDelim {
			return nil
		}
		if delim == '[' {
			for dec.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			_, err := dec.Token()
			return err
		}
		slot := len(out)
		out = append(out, "")
		var keys []string
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return err
			}
			keys = append(keys, key.(string))
			if err := walk(); err != nil {
				return err
			}
		}
		out[slot] = strings.Join(keys, ",")
		_, err = dec.Token()
		return err
	}
	return out, walk()
}

// entrySession is a session over decoded entries, as the host's reads answer them: getBranch is the path from the root to an entry, empty for an unknown one.
type entrySession []map[string]any

func (s entrySession) GetEntry(id string) (map[string]any, error) {
	for _, entry := range s {
		if entry["id"] == id {
			return entry, nil
		}
	}
	return nil, nil
}

func (s entrySession) GetBranch(fromID *string) ([]map[string]any, error) {
	path := []map[string]any{}
	if fromID == nil {
		return path, nil
	}
	for id := *fromID; id != ""; {
		entry, _ := s.GetEntry(id)
		if entry == nil {
			break
		}
		path = append([]map[string]any{entry}, path...)
		id, _ = entry["parentId"].(string)
	}
	return path, nil
}

func nonNil(values []any) []any {
	if values == nil {
		return []any{}
	}
	return values
}

func nonNilMaps(values []map[string]any) []map[string]any {
	if values == nil {
		return []map[string]any{}
	}
	return values
}

// registerSignalAbort contributes the signal-abort row every SDK fixture carries. Pi's ctx.abort() aborts the run's signal before it returns (agent.ts:340-342), so a
// handler that holds the signal and reads it again right after the abort sees it aborted, in the same turn.
func registerSignalAbort(ext *sdk.Extension) {
	state := func(signal interface{ Err() error }) string {
		switch {
		case signal == nil:
			return "none"
		case signal.Err() != nil:
			return "aborted"
		}
		return "live"
	}
	ext.Command("signal-abort", "Abort the run and report ctx.signal", func(ctx sdk.Context, _ string) error {
		held := ctx.Signal()
		ctx.Abort()
		ctx.Notify("abort:"+state(held)+":"+state(ctx.Signal()), "info")
		return nil
	})
}

// dialogText is a dialog's string answer: a select, input or editor result is whatever JSON the client sent (Pi's RPC mode does not check its type), and undefined is nil.
func dialogText(value any) string {
	text, _ := value.(string)
	return text
}
