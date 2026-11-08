// Package tui is pi's footer (coding-agent interactive/components/footer.ts) on p2 data, registered as the runtime's `footer` slot. Provenance (p2-tui-port.md
// section 1.16): token and cost stats are the viewed conversation's (`pi.usage`), the cache hit rate is its latest committed response's, context tokens are its
// active-context estimate, and the context window comes from the owner's model catalog, else from the conversation's guest-safe model metadata (`Chat.model`,
// P3); without either, tokens show without a percentage. The session total (children and tools included, `SessionMetrics`) is labeled "session". `/session`
// lists the same numbers with their scopes.
//
// Ports apps/p2/plugins/status/tui.ts.
package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	modelscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/models/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/status/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	p2tui "github.com/MichaelKinsy/PiG/apps/p2/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/components"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	chordtui "github.com/MichaelKinsy/PiG/chord/next/tui"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	pitui "github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// FormatTokens is formatTokens.
func FormatTokens(count int) string { return render.FormatTokens(count) }

// Totals are a conversation's token and cost totals.
type Totals struct {
	Input, Output, CacheRead, CacheWrite, Cost float64
}

// ConversationUsage is the conversation totals of `pi.usage` (DTUI `totalUsage`): its models' and tools' usage. view is the Chat view's JSON.
func ConversationUsage(view any) Totals {
	state := p2tui.JField(p2tui.JField(view, "docs"), "pi.usage")
	var total Totals
	for _, group := range []string{"models", "tools"} {
		object, _ := p2tui.JField(state, group).(interface{ Keys() []string })
		if object == nil {
			continue
		}
		for _, key := range object.Keys() {
			usage := p2tui.JField(p2tui.JField(state, group), key)
			total.Input += p2tui.JNum(p2tui.JField(usage, "input"))
			total.Output += p2tui.JNum(p2tui.JField(usage, "output"))
			total.CacheRead += p2tui.JNum(p2tui.JField(usage, "cacheRead"))
			total.CacheWrite += p2tui.JNum(p2tui.JField(usage, "cacheWrite"))
			total.Cost += p2tui.JNum(p2tui.JField(p2tui.JField(usage, "cost"), "total"))
		}
	}
	return total
}

type activeStats struct {
	contextTokens float64
	latest        *ai.Usage
}

func statsOf(value any) *activeStats {
	if p2tui.JObj(value) == nil {
		return nil
	}
	stats := &activeStats{contextTokens: p2tui.JNum(p2tui.JField(value, "contextTokens"))}
	if usage := p2tui.JField(value, "latestAssistantUsage"); usage != nil {
		stats.latest = &ai.Usage{Input: int(p2tui.JNum(p2tui.JField(usage, "input"))), CacheRead: int(p2tui.JNum(p2tui.JField(usage, "cacheRead"))), CacheWrite: int(p2tui.JNum(p2tui.JField(usage, "cacheWrite")))}
	}
	return stats
}

func (s *activeStats) hit() *float64 {
	if s == nil || s.latest == nil {
		return nil
	}
	return p2runtime.CacheHitRate(*s.latest)
}

func fixed(n float64, digits int) string { return jsstring.ToFixed(n, digits) }

// FooterSurface is the footer slot. Its fields are the owner loop's.
type FooterSurface struct {
	runtime  *runtime.TuiRuntime
	scope    *chordtui.TuiScope
	active   *chordtui.TuiScope
	models   *chordtui.TuiScope
	identity string
	hasID    bool
	session  string

	sessionsRead func() (delta.JsonValue, bool)
	catalogRead  func() (delta.JsonValue, bool)
	viewRead     func() (delta.JsonValue, bool)
	statsRead    func() (delta.JsonValue, bool)
	modelRead    func() (delta.JsonValue, bool)
	usageRead    func() (delta.JsonValue, bool)
	clientsRead  func() (delta.JsonValue, bool)
	workerError  string

	children     map[string][]runtime.SessionChild
	stopSources  []func()
	sourcesRead  func() (any, bool)
	sourcesTable any
	remove       func()
	onChange     func()
}

// NewFooterSurface is the footer of rt.
func NewFooterSurface(rt *runtime.TuiRuntime) *FooterSurface {
	f := &FooterSurface{runtime: rt}
	f.onChange = func() { rt.Post(func() { f.syncSources(); rt.Ctx.RequestRender() }) }
	f.scope = chordtui.NewTuiScope(context.Background(), rt.Host, f.onChange)
	// Subagent names for the crumb, from the subagents plugin when it is installed.
	f.sourcesRead = chordtui.Registry(f.scope, runtime.TuiSessionSources.Registry)
	if sessions, err := f.scope.Hold(sessionscontract.Sessions, delta.JsonObjectOf()); err == nil {
		f.sessionsRead = chordtui.WatchMember(f.scope, sessions.Member("sessions"))
	}
	f.remove = rt.AddListener(f.changed)
	f.changed()
	return f
}

func (f *FooterSurface) syncSources() {
	value, ok := f.sourcesRead()
	if !ok || value == nil || value == f.sourcesTable {
		return
	}
	table, ok := value.(*p2runtime.KeyedTable[runtime.TuiSessionSource])
	if !ok {
		return
	}
	f.sourcesTable = value
	for _, stop := range f.stopSources {
		stop()
	}
	f.stopSources = nil
	for _, key := range table.Keys() {
		source, _ := table.Get(key)
		if source.Children != nil {
			f.stopSources = append(f.stopSources, source.Children(f.runtime, func(children map[string][]runtime.SessionChild) {
				f.children = children
				f.runtime.Ctx.RequestRender()
			}))
		}
	}
}

func (f *FooterSurface) changed() {
	rt := f.runtime
	// The model catalog is owner-only (plugins/models/server.ts): guests never request it.
	if rt.Role == runtime.RoleOwner && f.models == nil {
		f.models = chordtui.NewTuiScope(context.Background(), rt.Host, f.onChange)
		if models, err := f.models.Hold(modelscontract.Models, delta.JsonObjectOf()); err == nil {
			f.catalogRead = chordtui.WatchMember(f.models, models.Member("models"))
		}
		// A failed session worker (the web's "Session unavailable" with Retry), owner only.
		if status, err := f.models.Hold(contract.SessionStatus, delta.JsonObjectOf()); err == nil {
			if member := status.Member("statuses"); member != nil {
				p2tui.FollowMember(member, func(value delta.JsonValue) {
					rt.Post(func() {
						message := ""
						if f.session != "" {
							message = p2tui.JStr(p2tui.JField(p2tui.JField(value, f.session), "error"))
						}
						if message != "" && message != f.workerError {
							rt.Notify("Session worker failed: "+render.LineText(message)+" · /retry-worker", runtime.NoticeError)
						}
						f.workerError = message
					})
				})
			}
		}
	}
	if f.hasID && f.identity == rt.Identity() {
		return
	}
	if f.active != nil {
		f.active.Dispose()
		f.active = nil
	}
	f.identity, f.hasID = rt.Identity(), true
	f.session = ""
	f.viewRead, f.statsRead, f.modelRead, f.usageRead, f.clientsRead = nil, nil, nil, nil, nil
	if !rt.HasSession {
		return
	}
	f.session = rt.Session
	f.active = chordtui.NewTuiScope(context.Background(), rt.Host, f.onChange)
	if chat, err := f.active.Hold(chatcontract.Chat, delta.JsonObjectOf("session", rt.Session, "conversation", float64(rt.Conversation))); err == nil {
		f.viewRead = chordtui.WatchMember(f.active, chat.Member("view"))
		f.statsRead = chordtui.WatchMember(f.active, chat.Member("stats"))
		f.modelRead = chordtui.WatchMember(f.active, chat.Member("model"))
	}
	if metrics, err := f.active.Hold(contract.SessionMetrics, delta.JsonObjectOf("session", rt.Session, "viewer", rt.Name)); err == nil {
		f.usageRead = chordtui.WatchMember(f.active, metrics.Member("usage"))
		f.clientsRead = chordtui.WatchMember(f.active, metrics.Member("clients"))
	}
}

func read(reader func() (delta.JsonValue, bool)) any {
	if reader == nil {
		return nil
	}
	value, _ := reader()
	return value
}

func (f *FooterSurface) sessionRow() *sessionscontract.Session {
	for _, each := range p2tui.JList(read(f.sessionsRead)) {
		if row := sessionscontract.SessionFromJSON(each); row.ID == f.session {
			return &row
		}
	}
	return nil
}

func (f *FooterSurface) contextWindow(provider, modelID string) float64 {
	for _, each := range p2tui.JList(read(f.catalogRead)) {
		if p2tui.JStr(p2tui.JField(each, "provider")) == provider && p2tui.JStr(p2tui.JField(each, "modelId")) == modelID {
			return p2tui.JNum(p2tui.JField(each, "contextWindow"))
		}
	}
	return p2tui.JNum(p2tui.JField(read(f.modelRead), "contextWindow"))
}

// agentModel is the viewed conversation's model and thinking level (`pi.agent`), "" when the view has none.
func (f *FooterSurface) agent() (provider, modelID, thinking string, hasModel bool) {
	agent := p2tui.JField(p2tui.JField(read(f.viewRead), "docs"), "pi.agent")
	model := p2tui.JField(agent, "model")
	return p2tui.JStr(p2tui.JField(model, "provider")), p2tui.JStr(p2tui.JField(model, "modelId")), p2tui.JStr(p2tui.JField(agent, "thinkingLevel")), model != nil
}

func where(session *sessionscontract.Session) string {
	if session == nil {
		return ""
	}
	if session.Env != nil && *session.Env != "" {
		return *session.Env + ":" + session.Cwd
	}
	return session.Cwd
}

// Render draws the footer.
func (f *FooterSurface) Render(width int) []string {
	rt := f.runtime
	t := rt.Theme().Current()
	session := f.sessionRow()
	provider, modelID, agentThinking, hasAgentModel := f.agent()
	stats := statsOf(read(f.statsRead))
	// Line 1: where (env:cwd), the session title, and a subagent's conversation.
	here := where(session)
	// pi's `cwd (branch)`: the git branch from a plugin's `footer:` slot (the workspace's), when one is mounted.
	if session != nil {
		for _, surface := range rt.Slots("footer:") {
			if lines := surface.Render(width); len(lines) > 0 {
				if lines[0] != "" {
					here += " (" + lines[0] + ")"
				}
				break
			}
		}
	}
	if session != nil && session.Title != "" {
		if here != "" {
			here += " • "
		}
		here += session.Title
	}
	// The subagent crumb (the web's top-bar crumb): the session › the shown subagent conversation.
	main := 1
	if session != nil {
		main = session.Main
	}
	if rt.Conversation != main {
		name := fmt.Sprintf("conversation %d", rt.Conversation)
		for _, row := range f.children[f.session] {
			if row.ConversationID == rt.Conversation {
				name = row.Name
				break
			}
		}
		here += " › " + name
	}
	if f.session == "" {
		here = "No session"
	}
	pwdLine := widthx.TruncateToWidth(t.Fg("dim", render.LineText(here)), width, t.Fg("dim", "..."), false)
	// Line 2: conversation stats, context, session total; model and thinking on the right.
	usage := ConversationUsage(read(f.viewRead))
	var parts []string
	if usage.Input != 0 {
		parts = append(parts, "↑"+FormatTokens(int(usage.Input)))
	}
	if usage.Output != 0 {
		parts = append(parts, "↓"+FormatTokens(int(usage.Output)))
	}
	if usage.CacheRead != 0 {
		parts = append(parts, "R"+FormatTokens(int(usage.CacheRead)))
	}
	if usage.CacheWrite != 0 {
		parts = append(parts, "W"+FormatTokens(int(usage.CacheWrite)))
	}
	hit := stats.hit()
	if (usage.CacheRead != 0 || usage.CacheWrite != 0) && hit != nil {
		parts = append(parts, "CH"+fixed(*hit*100, 1)+"%")
	}
	if usage.Cost != 0 {
		parts = append(parts, "$"+fixed(usage.Cost, 3))
	}
	if !hasAgentModel && session != nil {
		provider, modelID, hasAgentModel = session.Model.Provider, session.Model.ModelId, true
	}
	context := ""
	if stats != nil {
		window := f.contextWindow(provider, modelID)
		if window > 0 {
			percent := stats.contextTokens / window * 100
			text := fixed(percent, 1) + "%/" + FormatTokens(int(window))
			switch {
			case percent > 90:
				context = t.Fg("error", text)
			case percent > 70:
				context = t.Fg("warning", text)
			default:
				context = text
			}
		} else {
			context = FormatTokens(int(stats.contextTokens)) + " ctx"
		}
	}
	if context != "" {
		parts = append(parts, context)
	}
	if cost := p2tui.JNum(p2tui.JField(p2tui.JField(read(f.usageRead), "cost"), "total")); read(f.usageRead) != nil && cost > usage.Cost+0.0005 {
		parts = append(parts, "session $"+fixed(cost, 3))
	}
	thinking := agentThinking
	if thinking == "" && session != nil {
		thinking = session.ThinkingLevel
	}
	modelName := "no-model"
	if hasAgentModel {
		modelName = render.LineText(modelID)
	}
	right := modelName
	if hasAgentModel && thinking != "" {
		if thinking == "off" {
			right += " • thinking off"
		} else {
			right += " • " + render.LineText(thinking)
		}
	}
	withProvider := right
	if hasAgentModel {
		withProvider = "(" + render.LineText(provider) + ") " + right
	}
	left := strings.Join(parts, " ")
	line := components.Spread(left, withProvider, width, 2)
	if !strings.Contains(line, withProvider) {
		line = components.Spread(left, right, width, 2)
	}
	// Dim every part separately: colored context text ends with a reset that would clear an outer dim.
	separator := context
	if separator == "" {
		separator = "\x00"
	}
	pieces := strings.Split(line, separator)
	for i := range pieces {
		pieces[i] = t.Fg("dim", pieces[i])
	}
	return []string{pwdLine, strings.Join(pieces, context)}
}

// Invalidate is a no-op.
func (f *FooterSurface) Invalidate() {}

// SessionInfo is `/session`: the footer's numbers with their scopes (section 1.16), and the session's facts.
func (f *FooterSurface) SessionInfo() []string {
	rt := f.runtime
	t := rt.Theme().Current()
	session := f.sessionRow()
	if session == nil {
		return []string{t.Fg("muted", "No session")}
	}
	provider, modelID, agentThinking, hasAgentModel := f.agent()
	if !hasAgentModel {
		provider, modelID = session.Model.Provider, session.Model.ModelId
	}
	stats := statsOf(read(f.statsRead))
	usage := ConversationUsage(read(f.viewRead))
	window := f.contextWindow(provider, modelID)
	row := func(label, value string) string {
		return "  " + t.Fg("dim", padEnd(label, 26)) + " " + render.LineText(value)
	}
	hit := stats.hit()
	tokens := func(input, output, cacheRead, cacheWrite float64) string {
		return "↑" + FormatTokens(int(input)) + " ↓" + FormatTokens(int(output)) + " R" + FormatTokens(int(cacheRead)) + " W" + FormatTokens(int(cacheWrite))
	}
	title := session.Title
	if title == "" {
		title = "Untitled"
	}
	thinking := agentThinking
	if thinking == "" {
		thinking = session.ThinkingLevel
	}
	viewers := "?"
	if clients := read(f.clientsRead); clients != nil {
		viewers = jsvalueString(clients)
	}
	cacheHit := "-"
	if hit != nil && *hit != 0 {
		cacheHit = fixed(*hit*100, 1) + "%"
	}
	contextText := "-"
	if stats != nil {
		contextText = FormatTokens(int(stats.contextTokens))
		if window != 0 {
			contextText += fmt.Sprintf(" of %s (%s%%)", FormatTokens(int(window)), fixed(stats.contextTokens/window*100, 1))
		}
	}
	sessionTokens, sessionCost := "-", "-"
	if total := read(f.usageRead); total != nil {
		sessionTokens = tokens(p2tui.JNum(p2tui.JField(total, "input")), p2tui.JNum(p2tui.JField(total, "output")), p2tui.JNum(p2tui.JField(total, "cacheRead")), p2tui.JNum(p2tui.JField(total, "cacheWrite")))
		sessionCost = "$" + fixed(p2tui.JNum(p2tui.JField(p2tui.JField(total, "cost"), "total")), 4)
	}
	return []string{
		t.Bold("Session"),
		row("Title", title),
		row("ID", session.ID),
		row("Directory", where(session)),
		row("Conversation", fmt.Sprint(rt.Conversation)),
		row("Model", provider+"/"+modelID),
		row("Thinking", thinking),
		row("Viewers", viewers),
		"",
		t.Bold("This conversation"),
		row("Tokens", tokens(usage.Input, usage.Output, usage.CacheRead, usage.CacheWrite)),
		row("Cost", "$"+fixed(usage.Cost, 4)),
		row("Cache hit (latest answer)", cacheHit),
		row("Context (estimate)", contextText),
		"",
		t.Bold("Whole session (children and tools)"),
		row("Tokens", sessionTokens),
		row("Cost", sessionCost),
	}
}

func jsvalueString(value any) string {
	if n, ok := value.(float64); ok {
		return fixed(n, 0)
	}
	return fmt.Sprint(value)
}

// padEnd pads with spaces to n UTF-16 units, as String.padEnd does.
func padEnd(text string, n int) string {
	if units := widthx.VisibleWidth(text); units < n {
		return text + strings.Repeat(" ", n-units)
	}
	return text
}

// Dispose releases everything the footer follows.
func (f *FooterSurface) Dispose() {
	f.remove()
	for _, stop := range f.stopSources {
		stop()
	}
	f.scope.Dispose()
	if f.models != nil {
		f.models.Dispose()
	}
	if f.active != nil {
		f.active.Dispose()
	}
}

var _ pitui.Component = (*FooterSurface)(nil)

var commands = p2runtime.Keyed[runtime.TuiCommand]{
	p2runtime.Entry("p2.worker.retry", runtime.TuiCommand{
		Title: "Retry the session worker", Category: "Owner", When: "owner && !overlay", Slash: &runtime.TuiCommandSlash{Name: "retry-worker"},
		// Long work: the pending line shows at once, the outcome when the worker started (or failed again).
		Run: func(ctx runtime.TuiContext, _ string) error {
			session := ctx.Identity().Session
			if session == "" {
				return nil
			}
			ctx.Notify("Retrying the session worker…", runtime.NoticeInfo)
			go func() {
				err := func() error {
					control, err := ctx.Host().Get(context.Background(), contract.WorkerControl, delta.JsonObjectOf("session", session), host.GetOptions{})
					if err != nil {
						return err
					}
					defer control.Release()
					if variant, _, _ := control.Variant(); variant != "owner" {
						return nil
					}
					if _, err := control.Call("restart", delta.JsonObjectOf(), context.Background()); err != nil {
						return err
					}
					ctx.Post(func() { ctx.Notify("Session worker started", runtime.NoticeInfo) })
					return nil
				}()
				if err != nil {
					ctx.Post(func() { ctx.Fail(err) })
				}
			}()
			return nil
		},
	}),
	p2runtime.Entry("p2.dev.reload", runtime.TuiCommand{
		Title: "Reload changed plugins (dev server)", Category: "Owner", When: "owner && !overlay", Slash: &runtime.TuiCommandSlash{Name: "dev-reload"},
		// The dev channel is an owner HTTP route (`/__p2_dev`) that the launcher reaches with its own sign-in: a launcher action (section 3.2), not a request from
		// this worker. The launcher's consent screen confirms; the server answers with what it reloaded (or why it did not).
		Run: func(ctx runtime.TuiContext, _ string) error {
			go func() {
				result := ctx.Grant("devCommand", map[string]any{"action": "reload"})
				ctx.Post(func() {
					text := ""
					if result.OK {
						if message, ok := resultMessage(result.Value); ok {
							text = render.LineText(message)
						}
					}
					if units := []rune(text); len(units) > 200 {
						text = string(units[:200])
					}
					if text == "" {
						text = "Plugin reload requested"
					}
					p2tui.GrantNotice(ctx, text, result)
				})
			}()
			return nil
		},
	}),
	p2runtime.Entry("p2.session.info", runtime.TuiCommand{
		Title: "Session information", Category: "Session", When: "!overlay", Slash: &runtime.TuiCommandSlash{Name: "session"},
		Run: func(ctx runtime.TuiContext, _ string) error {
			footer, _ := ctx.Surface("footer").(*FooterSurface)
			if footer == nil {
				return nil
			}
			var handle runtime.TuiOverlayHandle
			handle = ctx.Overlay("session", p2tui.NewTextOverlay(ctx, "Session", footer.SessionInfo, func() { handle.Close() }),
				pitui.OverlaySpec{Width: p2tui.Percent(90), Anchor: "top-center", OffsetY: 1})
			return nil
		},
	}),
}

// resultMessage is a `message` string member of a launcher result's value.
func resultMessage(value any) (string, bool) {
	switch typed := value.(type) {
	case map[string]any:
		message, ok := typed["message"].(string)
		return message, ok
	case interface{ Value(string) any }:
		message, ok := typed.Value("message").(string)
		return message, ok
	}
	return "", false
}

// Facet is the plugin's TUI facet.
var Facet = runtime.TuiFacet("footer", func(rt *runtime.TuiRuntime) runtime.TuiSurface { return NewFooterSurface(rt) }, commands, nil)
