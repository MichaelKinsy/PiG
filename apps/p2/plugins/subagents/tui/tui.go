// Package tui is the subagent tool's card in the TUI, after the web card: action and name, the message, the child's configuration, and the reply (an answer as
// Markdown). A running spawn says where the subagent works (DTUI `apply`). Also the session selector's subagent conversations (threaded sort, p2-tui-port.md
// section 1.17).
//
// Ports apps/p2/plugins/subagents/tui.ts.
package tui

import (
	"context"
	"sync"
	"fmt"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	p2tui "github.com/MichaelKinsy/PiG/apps/p2/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/components"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/jsvalue"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tools"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	chordtui "github.com/MichaelKinsy/PiG/chord/next/tui"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

const previewLines = 10

func record(value any) *jsvalue.Object {
	object, _ := value.(*jsvalue.Object)
	return object
}

// Renderers is the subagent tool's card.
var Renderers = &tools.ToolRenderers{
	RenderCall: func(args tools.ToolArgs, t *theme.Theme, context *tools.ToolRenderContext) pitui.Component {
		component, ok := context.LastComponent.(*pitui.Container)
		if !ok {
			component = pitui.NewContainer()
		}
		component.Clear()
		action, _ := args.Lookup("action").(string)
		title := "subagent"
		if action != "" {
			title = "subagent " + render.LineText(action)
		}
		title = t.Fg("toolTitle", t.Bold(title))
		if name, _ := args.Lookup("name").(string); name != "" {
			title += " " + t.Fg("accent", render.LineText(name))
		}
		component.AddChild(pitui.NewPaddedText(title, 0, 0, nil))
		if message, _ := args.Lookup("message").(string); message != "" {
			lines := strings.Split(render.SafeText(message), "\n")
			shown := lines
			if !context.Expanded && len(lines) > previewLines {
				shown = lines[:previewLines]
			}
			text := t.Fg("muted", strings.Join(shown, "\n"))
			if len(shown) < len(lines) {
				text += "\n" + tools.MoreLinesHint(context.UI, t, len(lines)-len(shown), "")
			}
			component.AddChild(pitui.NewPaddedText(text, 0, 0, nil))
		}
		return component
	},
	RenderResult: func(result tools.ToolResultView, options tools.ToolRenderResultOptions, t *theme.Theme, context *tools.ToolRenderContext) pitui.Component {
		component, ok := context.LastComponent.(*pitui.Container)
		if !ok {
			component = pitui.NewContainer()
		}
		component.Clear()
		details := record(result.Details)
		var config []string
		if model, ok := details.Lookup("model").(string); ok && model != "" {
			config = append(config, render.LineText(model))
		}
		if level, ok := details.Lookup("thinkingLevel").(string); ok && level != "" {
			config = append(config, "thinking "+render.LineText(level))
		}
		if list, ok := details.Lookup("tools").([]any); ok {
			var names []string
			for _, tool := range list {
				names = append(names, render.LineText(jsvalue.String(tool)))
			}
			joined := strings.Join(names, ", ")
			if joined == "" {
				joined = "none"
			}
			config = append(config, "tools "+joined)
		}
		if cwd, ok := details.Lookup("cwd").(string); ok {
			shown := ""
			if env, ok := details.Lookup("env").(string); ok {
				shown = render.LineText(env + ":" + cwd)
			} else {
				shown = render.LineText(tools.ShortenPath(cwd, context.Cwd))
				if tools.ShortenPath(cwd, context.Cwd) == "" {
					shown = render.LineText(".")
				}
			}
			config = append(config, "cwd "+shown)
		}
		if len(config) > 0 {
			component.AddChild(pitui.NewPaddedText(t.Fg("dim", strings.Join(config, " · ")), 0, 0, nil))
		}
		output := strings.TrimSpace(tools.GetTextOutput(&result))
		conversation, hasConversation := details.Lookup("conversationId").(float64)
		if output == "" && options.IsPartial && hasConversation {
			component.AddChild(pitui.NewSpacer(1))
			component.AddChild(pitui.NewPaddedText(t.Fg("muted", fmt.Sprintf("Subagent %s is working.", jsvalue.String(conversation))), 0, 0, nil))
		} else if output != "" {
			component.AddChild(pitui.NewSpacer(1))
			action, _ := context.Args.Lookup("action").(string)
			if action == "answer" && !context.IsError {
				markdown := context.UI.Theme().Markdown
				component.AddChild(pitui.NewMarkdownWithOptions(output, 0, 0, &markdown, nil, nil))
			} else {
				color := "toolOutput"
				if context.IsError {
					color = "error"
				}
				component.AddChild(pitui.NewPaddedText(t.Fg(color, output), 0, 0, nil))
			}
		}
		return component
	},
}

// child is a subagent as the sidebar lists it.
type child struct {
	name, kind, model, thinkingLevel string
	archived                         bool
	conversationID                   int
	tools                            []string
	location                         *p2runtime.Location
}

func childOf(value any) child {
	c := child{
		name: p2tui.JStr(p2tui.JField(value, "name")), kind: p2tui.JStr(p2tui.JField(value, "kind")), model: p2tui.JStr(p2tui.JField(value, "model")),
		thinkingLevel: p2tui.JStr(p2tui.JField(value, "thinkingLevel")), archived: p2tui.JBool(p2tui.JField(value, "archived")),
		conversationID: int(p2tui.JNum(p2tui.JField(value, "conversationId"))),
	}
	for _, name := range p2tui.JList(p2tui.JField(value, "tools")) {
		c.tools = append(c.tools, p2tui.JStr(name))
	}
	if location := p2tui.JField(value, "location"); location != nil {
		l := p2runtime.Location{Cwd: p2tui.JStr(p2tui.JField(location, "cwd"))}
		if env, ok := p2tui.JField(location, "env").(string); ok {
			l.Env = &env
		}
		c.location = &l
	}
	return c
}

// sessionSource is the named subagents by session (one-shot agents and archived ones are not listed), as the sidebar shows them.
var sessionSource = runtime.TuiSessionSource{
	Children: func(rt *runtime.TuiRuntime, onChange func(map[string][]runtime.SessionChild)) func() {
		ref, err := rt.Host.Hold(context.Background(), contract.Subagents, delta.JsonObjectOf(), host.GetOptions{})
		if err != nil {
			return func() {}
		}
		var mu sync.Mutex
		var stop func()
		stopped := false
		go func() {
			if err := p2runtime.WhenLive(context.Background(), ref); err != nil {
				return
			}
			if variant, _, _ := ref.Variant(); variant != "reader" {
				return
			}
			follow := p2tui.FollowMember(ref.Member("children"), func(value delta.JsonValue) {
				named := map[string][]runtime.SessionChild{}
				if object, ok := value.(interface{ Keys() []string }); ok {
					for _, session := range object.Keys() {
						rows := []runtime.SessionChild{}
						for _, each := range p2tui.JList(p2tui.JField(value, session)) {
							c := childOf(each)
							if c.kind != "agent" && !c.archived {
								rows = append(rows, runtime.SessionChild{Name: c.name, ConversationID: c.conversationID, Model: c.model})
							}
						}
						named[session] = rows
					}
				}
				rt.Post(func() { onChange(named) })
			})
			// A stop that came first leaves nothing to follow.
			mu.Lock()
			if stopped {
				mu.Unlock()
				follow()
				return
			}
			stop = follow
			mu.Unlock()
		}()
		return func() {
			mu.Lock()
			stopped = true
			current := stop
			mu.Unlock()
			if current != nil {
				current()
			}
			ref.Release()
		}
	},
}

type archiveChange struct {
	conversation int
	archived     bool
}

// openAgents is `/agents` (pi's DurableTui `selectConversation`, the web sidebar's subagent rows): the session's main conversation, its subagents and one-shot
// agents. Enter opens one, keeping each conversation's draft and reading position (P0c); participants archive or restore a subagent (one-shot agents are
// read-only), the archived ones shown on request.
func openAgents(ctx runtime.TuiContext) error {
	session := ctx.Identity().Session
	if session == "" {
		ctx.Notify("Open a session first", runtime.NoticeInfo)
		return nil
	}
	scope := ctx.Scope()
	ref, err := scope.Hold(contract.Subagents, delta.JsonObjectOf())
	if err != nil {
		scope.Dispose()
		return err
	}
	childrenRead := chordtui.WatchMember(scope, ref.Member("children"))
	control, err := scope.Hold(contract.SubagentControl, delta.JsonObjectOf("session", session))
	if err != nil {
		scope.Dispose()
		return err
	}
	// Archive and restore show at once; a refused one flips back with the error.
	shown, archiving, _ := chordtui.Optimistic(scope, func() []child {
		value, _ := childrenRead()
		var out []child
		for _, each := range p2tui.JList(p2tui.JField(value, session)) {
			out = append(out, childOf(each))
		}
		return out
	}, func(children []child, change archiveChange) []child {
		out := append([]child(nil), children...)
		for i := range out {
			if out[i].conversationID == change.conversation {
				out[i].archived = change.archived
			}
		}
		return out
	})
	showArchived := false
	var overlay runtime.TuiOverlayHandle
	closeAll := func() {
		scope.Dispose()
		overlay.Close()
	}
	rows := func() []components.ListRow {
		identity := ctx.Identity()
		detail := fmt.Sprintf("conversation %d", identity.Main)
		if identity.Conversation == identity.Main {
			detail = "✓ shown"
		}
		out := []components.ListRow{{Key: strconv.Itoa(identity.Main), Label: "main", Detail: detail}}
		for _, c := range shown() {
			if !showArchived && c.archived {
				continue
			}
			label := c.name
			if c.kind == "agent" {
				label = "agent " + label
			}
			if c.archived {
				label += " (archived)"
			}
			mark := ""
			if identity.Conversation == c.conversationID {
				mark = "✓ shown · "
			}
			description := "no tools"
			if len(c.tools) > 0 {
				description = "tools: " + strings.Join(c.tools, ", ")
			}
			if c.location != nil {
				description += " · in " + render.LineText(p2runtime.LocationText(*c.location))
			} else {
				description += " · follows the session"
			}
			row := components.ListRow{Key: strconv.Itoa(c.conversationID), Label: label, Detail: fmt.Sprintf("%s%s · %s", mark, c.model, c.thinkingLevel), Description: description}
			if c.archived {
				row.Color = "muted"
			}
			out = append(out, row)
		}
		return out
	}
	selected := strconv.Itoa(ctx.Identity().Conversation)
	overlay = ctx.Overlay("agents", components.NewListOverlay(runtime.Selectors(ctx), components.ListOverlayOptions{
		Title: func() string {
			if showArchived {
				return "Conversations · with archived"
			}
			return "Conversations · active"
		},
		Rows:     rows,
		Selected: &selected,
		OnSelect: func(row components.ListRow) {
			closeAll()
			if conversation, err := strconv.Atoi(row.Key); err == nil {
				ctx.Navigate("chat", session, &conversation)
			}
		},
		Actions: []components.ListAction{
			{ID: "app.session.toggleArchived", Label: "archived", Run: func(*components.ListRow) { showArchived = !showArchived }},
			{ID: "app.session.delete", Label: "archive/restore", Run: func(row *components.ListRow) {
				if row == nil {
					return
				}
				var target *child
				for _, c := range shown() {
					if strconv.Itoa(c.conversationID) == row.Key {
						c := c
						target = &c
					}
				}
				if target == nil || target.kind == "agent" {
					return
				}
				if variant, _, _ := control.Variant(); variant != "participant" || p2runtime.StatusOf(control) != host.RefStateLive {
					ctx.Notify("Only participants archive subagents", runtime.NoticeWarning)
					return
				}
				change := archiveChange{conversation: target.conversationID, archived: !target.archived}
				name := target.name
				go func() {
					err := archiving(change, func() error {
						_, err := control.Call("archive", delta.JsonObjectOf("conversation", float64(change.conversation), "archived", change.archived), context.Background())
						return err
					})
					ctx.Post(func() {
						if err != nil {
							ctx.Fail(err)
							return
						}
						verb := "Restored"
						if change.archived {
							verb = "Archived"
						}
						ctx.Notify(verb+" "+name, runtime.NoticeInfo)
					})
				}()
			}},
		},
		OnClose: closeAll,
	}), pitui.OverlaySpec{Width: p2tui.Percent(100), Anchor: "bottom-center"})
	return nil
}

var commands = p2runtime.Keyed[runtime.TuiCommand]{
	p2runtime.Entry("p2.agents", runtime.TuiCommand{
		Title: "Conversations of this session (subagents)", Category: "Session", When: "chat && !overlay", Slash: &runtime.TuiCommandSlash{Name: "agents"},
		Run: func(ctx runtime.TuiContext, _ string) error { return openAgents(ctx) },
	}),
	p2runtime.Entry("p2.agents.main", runtime.TuiCommand{
		Title: "Back to the main conversation", Category: "Session", When: "chat && !overlay", Slash: &runtime.TuiCommandSlash{Name: "main"},
		Run: func(ctx runtime.TuiContext, _ string) error {
			identity := ctx.Identity()
			ctx.Navigate("chat", identity.Session, &identity.Main)
			return nil
		},
	}),
}

// Facet is the plugin's TUI facet.
var Facet = host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	return p2runtime.Contribute(facet, "p2.tui-subagents", func(self host.StoreSelf) error {
		p2runtime.ContributeKeyed(self, runtime.TuiToolRenderers, p2runtime.Entry("subagent", Renderers))
		p2runtime.ContributeKeyed(self, runtime.TuiSessionSources, p2runtime.Entry("subagents", sessionSource))
		p2runtime.ContributeKeyed(self, runtime.TuiCommands, commands...)
		return nil
	})
})
