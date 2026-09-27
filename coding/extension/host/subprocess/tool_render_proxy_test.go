package subprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

const toolRendererExtension = `export default function (pi) {
  const line = (text) => ({ render: (width) => [text + " w=" + width] });
  pi.registerTool({
    name: "card_tool",
    description: "renders its own card",
    parameters: { type: "object", properties: {} },
    renderShell: "self",
    async execute() { return { content: [{ type: "text", text: "ok" }] }; },
    renderCall(args, _theme, context) {
      context.state.calls = (context.state.calls ?? 0) + 1;
      if (context.state.calls === 1) setTimeout(() => context.invalidate(), 5);
      return line("call " + args.topic + " partial=" + context.isPartial + " last=" + (context.lastComponent ? "yes" : "no") + " calls=" + context.state.calls);
    },
    renderResult(result, options, _theme, context) {
      const text = result.content.map((block) => block.text).join("|");
      return line("result " + text + " " + result.details.k + " expanded=" + options.expanded + " calls=" + context.state.calls);
    },
  });
  pi.registerTool({
    name: "throwing_tool",
    description: "renderers throw",
    parameters: { type: "object", properties: {} },
    async execute() { return { content: [] }; },
    renderCall() { throw new Error("renderCall failed"); },
  });
}
`

// Upstream ToolExecutionComponent runs a registered tool's renderCall and
// renderResult with one state per card and each renderer's last component,
// renders the returned component at the card width, and reruns the renderers
// on context.invalidate. A Node extension's renderers run in its process: the
// host proxies them, renders only the last component on a resize, and draws
// the card's fallback when a renderer throws.
func TestNodeToolRenderersRunInTheExtensionProcess(t *testing.T) {
	nodeCellRequireNode(t)
	entry := filepath.Join(t.TempDir(), "renderers.mjs")
	if err := os.WriteFile(entry, []byte(toolRendererExtension), 0o644); err != nil {
		t.Fatal(err)
	}
	host := NewHost(t.TempDir())
	t.Cleanup(func() { host.Shutdown("test done") })
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	loaded, errs := host.LoadAll(ctx, []ExtConfig{{Name: "renderers", Source: entry, Enabled: true}})
	if len(errs) != 0 || len(loaded) != 1 {
		t.Fatalf("load: %v", errs)
	}
	card := loaded[0].Tools["card_tool"].Definition
	if card.RenderShell != extension.ToolRenderShellSelf || card.RenderCall == nil || card.RenderResult == nil {
		t.Fatalf("card_tool definition = %+v, want self shell with both renderers", card)
	}
	if throwing := loaded[0].Tools["throwing_tool"].Definition; throwing.RenderCall == nil || throwing.RenderResult != nil {
		t.Fatalf("throwing_tool definition = %+v, want renderCall only", throwing)
	}

	invalidated := make(chan struct{}, 4)
	args := json.RawMessage(`{"topic":"alpha"}`)
	renderContext := extension.ToolRenderContext{
		Args: args, ToolCallID: "call-1", Card: "card-under-test", IsPartial: true,
		Invalidate: func() { invalidated <- struct{}{} },
	}
	call := card.RenderCall(args, nil, renderContext).(*toolRenderProxy)
	waitForLines(t, call, 40, "call alpha partial=true last=no calls=1 w=40")
	select {
	case <-invalidated:
	case <-time.After(5 * time.Second):
		t.Fatal("context.invalidate did not reach the card")
	}

	renderContext.IsPartial = false
	renderContext.LastComponent = call
	if again := card.RenderCall(args, nil, renderContext); again != call {
		t.Fatal("renderCall with the last component returned a new proxy")
	}
	waitForLines(t, call, 40, "call alpha partial=false last=yes calls=2 w=40")
	// A resize renders the last component again without running renderCall.
	waitForLines(t, call, 30, "call alpha partial=false last=yes calls=2 w=30")

	renderContext.LastComponent = nil
	result := card.RenderResult(agent.AgentToolResult{Content: "out", Details: map[string]any{"k": "v"}}, extension.ToolRenderResultOptions{Expanded: true}, nil, renderContext).(*toolRenderProxy)
	waitForLines(t, result, 40, "result out v expanded=true calls=2 w=40")
	if result.session != call.session {
		t.Fatal("the call and result renderers of one card did not share a session")
	}

	throwing := loaded[0].Tools["throwing_tool"].Definition
	failed := throwing.RenderCall(args, nil, extension.ToolRenderContext{Args: args, Card: "throwing-card"}).(*toolRenderProxy)
	failed.SetRendererFallback(func(width int) []string { return []string{"fallback"} })
	waitForLines(t, failed, 40, "fallback")

	// Releasing a card drops its state: a new session starts from scratch.
	me := host.exts["renderers"]
	me.toolRenders.release(toolRenderRelease{card: "card-under-test", conn: me.conn})
	fresh := card.RenderCall(args, nil, extension.ToolRenderContext{Args: args, Card: "card-under-test", Invalidate: func() {}}).(*toolRenderProxy)
	waitForLines(t, fresh, 40, "call alpha partial=false last=no calls=1 w=40")
}

func waitForLines(t *testing.T, proxy *toolRenderProxy, width int, want ...string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var got []string
	for time.Now().Before(deadline) {
		if got = proxy.Render(width); slices.Equal(got, want) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("rendered %q, want %q", got, want)
}

// Every SDK exposes upstream's renderShell, renderCall and renderResult with
// one renderer state per tool card shared by both renderers, a context
// invalidate, and an error that draws the card's fallback.
func TestToolRenderersAcrossSDKs(t *testing.T) {
	root := findModuleRoot(t)
	t.Setenv("PIG_SDK_GO_ROOT", filepath.Join(root, "extensions", "sdk"))
	t.Setenv("PIG_SDK_PY_ROOT", filepath.Join(root, "extensions", "sdk-py"))
	t.Setenv("PIG_SDK_RS_ROOT", filepath.Join(root, "extensions", "sdk-rs"))
	for _, language := range []string{"go", "python", "rust"} {
		t.Run(language, func(t *testing.T) {
			config := toolRendererFactory(t, language, "renderers_"+language)
			host := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
			defer host.Shutdown("test done")
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
			defer cancel()
			loaded, errs := host.LoadAll(ctx, []ExtConfig{config})
			if len(errs) != 0 || len(loaded) != 1 {
				t.Fatalf("load: %v", errs)
			}
			card := loaded[0].Tools["card_tool"].Definition
			if card.RenderShell != extension.ToolRenderShellSelf || card.RenderCall == nil || card.RenderResult == nil {
				t.Fatalf("card_tool definition = %+v, want self shell with both renderers", card)
			}
			invalidated := make(chan struct{}, 4)
			args := json.RawMessage(`{"topic":"alpha"}`)
			renderContext := extension.ToolRenderContext{
				Args: args, ToolCallID: "call-1", Card: "sdk-card", IsPartial: true,
				Invalidate: func() { invalidated <- struct{}{} },
			}
			call := card.RenderCall(args, nil, renderContext).(*toolRenderProxy)
			waitForLines(t, call, 40, "call alpha partial=true calls=1 w=40")
			select {
			case <-invalidated:
			case <-time.After(10 * time.Second):
				t.Fatal("context invalidate did not reach the card")
			}
			result := card.RenderResult(agent.AgentToolResult{Content: "out", Details: map[string]any{"k": "v"}}, extension.ToolRenderResultOptions{Expanded: true}, nil, renderContext).(*toolRenderProxy)
			waitForLines(t, result, 40, "result out v expanded=true calls=1 w=40")

			throwing := loaded[0].Tools["throwing_tool"].Definition
			if throwing.RenderCall == nil || throwing.RenderResult != nil {
				t.Fatalf("throwing_tool definition = %+v, want a call renderer only", throwing)
			}
			failed := throwing.RenderCall(args, nil, extension.ToolRenderContext{Args: args, Card: "sdk-throwing"}).(*toolRenderProxy)
			failed.SetRendererFallback(func(int) []string { return []string{"fallback"} })
			waitForLines(t, failed, 40, "fallback")
		})
	}
}

func toolRendererFactory(t *testing.T, language, name string) ExtConfig {
	t.Helper()
	dir := t.TempDir()
	write := func(path, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, path), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	switch language {
	case "go":
		module := "example.com/" + name
		sdkMod, err := os.ReadFile(filepath.Join(findModuleRoot(t), "extensions", "sdk", "go.mod"))
		if err != nil {
			t.Fatal(err)
		}
		_, directive, ok := strings.Cut(string(sdkMod), "\ngo ")
		if !ok {
			t.Fatal("SDK go.mod has no language floor")
		}
		floor, _, _ := strings.Cut(directive, "\n")
		write("go.mod", "module "+module+"\n\ngo "+floor+"\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n")
		write("ext.go", fmt.Sprintf(`package ext
import (
    "errors"
    "fmt"
    "github.com/MichaelKinsy/PiG/extensions/sdk"
)
func Extension() *sdk.Extension {
    ext := sdk.New(%q)
    ok := func(sdk.Context, map[string]any) (any, error) { return "ok", nil }
    ext.Tool("card_tool", "renders its own card", sdk.Schema{"type": "object"}, ok)
    ext.SetToolRenderers("card_tool", sdk.ToolRenderers{
        Shell: sdk.ToolRenderShellSelf,
        Call: func(_ sdk.Context, args map[string]any, render sdk.ToolRenderContext, width int) ([]string, error) {
            calls, _ := render.State["calls"].(int)
            calls++
            render.State["calls"] = calls
            if calls == 1 {
                render.Invalidate()
            }
            return []string{fmt.Sprintf("call %%v partial=%%t calls=%%d w=%%d", args["topic"], render.IsPartial, calls, width)}, nil
        },
        Result: func(_ sdk.Context, result sdk.ToolRenderResult, options sdk.ToolRenderResultOptions, render sdk.ToolRenderContext, width int) ([]string, error) {
            details, _ := result.Details.(map[string]any)
            return []string{fmt.Sprintf("result %%v %%v expanded=%%t calls=%%v w=%%d", result.Content[0]["text"], details["k"], options.Expanded, render.State["calls"], width)}, nil
        },
    })
    ext.Tool("throwing_tool", "renderer fails", sdk.Schema{"type": "object"}, ok)
    ext.SetToolRenderers("throwing_tool", sdk.ToolRenderers{
        Call: func(sdk.Context, map[string]any, sdk.ToolRenderContext, int) ([]string, error) {
            return nil, errors.New("renderCall failed")
        },
    })
    return ext
}
`, name))
		return packedFactoryConfig(name, dir, module, name)
	case "python":
		write(name+".py", fmt.Sprintf(`import pig_sdk

def new_extension():
    ext = pig_sdk.Extension(%q)
    ext.tool("card_tool", "renders its own card", {"type": "object"}, lambda _ctx, _params: "ok")

    def render_call(_ctx, args, render, width):
        calls = render.state.get("calls", 0) + 1
        render.state["calls"] = calls
        if calls == 1:
            render.invalidate()
        return [f"call {args['topic']} partial={str(render.is_partial).lower()} calls={calls} w={width}"]

    def render_result(_ctx, result, options, render, width):
        text = result["content"][0]["text"]
        expanded = str(bool(options.get("expanded"))).lower()
        return [f"result {text} {result['details']['k']} expanded={expanded} calls={render.state['calls']} w={width}"]

    ext.tool_renderers("card_tool", render_call=render_call, render_result=render_result, render_shell="self")
    ext.tool("throwing_tool", "renderer fails", {"type": "object"}, lambda _ctx, _params: "ok")

    def failing(_ctx, _args, _render, _width):
        raise RuntimeError("renderCall failed")

    ext.tool_renderers("throwing_tool", render_call=failing)
    return ext
`, name))
		return packedPythonFactoryConfig(name, dir, name, name)
	case "rust":
		write("Cargo.toml", fmt.Sprintf("[package]\nname = %q\nversion = \"0.0.0\"\nedition = \"2024\"\n[dependencies]\npig-sdk = { path = %q }\nserde_json = \"1\"\n", name, filepath.Join(findModuleRoot(t), "extensions", "sdk-rs")))
		if err := os.Mkdir(filepath.Join(dir, "src"), 0o700); err != nil {
			t.Fatal(err)
		}
		write("src/lib.rs", fmt.Sprintf(`use pig_sdk::{Extension, ToolRenderShell, ToolResult};
use serde_json::json;
pub fn new_extension() -> Extension {
    let mut ext = Extension::new(%q);
    ext.tool("card_tool", "renders its own card", json!({"type": "object"}), |_ctx, _params| ToolResult::text("ok"));
    ext.tool_render_shell("card_tool", ToolRenderShell::SelfShell);
    ext.render_tool_call("card_tool", |_ctx, args, render, width| {
        let calls = render.state.get("calls").and_then(|v| v.as_u64()).unwrap_or(0) + 1;
        render.state.insert("calls".to_string(), json!(calls));
        if calls == 1 {
            render.invalidate();
        }
        Ok(vec![format!("call {} partial={} calls={} w={}", args["topic"].as_str().unwrap_or(""), render.is_partial, calls, width)])
    });
    ext.render_tool_result("card_tool", |_ctx, result, options, render, width| {
        let text = result.content[0]["text"].as_str().unwrap_or("").to_string();
        let calls = render.state.get("calls").cloned().unwrap_or(json!(0));
        Ok(vec![format!("result {} {} expanded={} calls={} w={}", text, result.details["k"].as_str().unwrap_or(""), options.expanded, calls, width)])
    });
    ext.tool("throwing_tool", "renderer fails", json!({"type": "object"}), |_ctx, _params| ToolResult::text("ok"));
    ext.render_tool_call("throwing_tool", |_ctx, _args, _render, _width| Err("renderCall failed".to_string()));
    ext
}
`, name))
		return packedRustFactoryConfig(name, dir, name, name)
	default:
		t.Fatalf("unknown language: %s", language)
		return ExtConfig{}
	}
}
