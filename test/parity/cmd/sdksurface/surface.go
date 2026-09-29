package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// Row kinds decide how a row's SDK cells are detected.
const (
	kindMember  = "member"  // a callable or property on pi, ctx, ctx.ui, theme, sessionManager or modelRegistry
	kindEvent   = "event"   // an event name accepted by pi.on
	kindPayload = "payload" // a field of an event payload
	kindResult  = "result"  // a field of an event handler's return value
	kindField   = "field"   // a field of an argument, option bag, definition or return value
)

// surfaceRow is one extension-facing surface of Pi's API.
type surfaceRow struct {
	Group string
	Key   string // stable identifier shown in the matrix and used by the map
	Decl  string // upstream declaration the row comes from
	Kind  string
	Root  string // pi, ctx, ui, theme, sessionManager, modelRegistry, events, event, ...
	Name  string // member or field name
	Event string // event name for event, payload and result rows
	// WireType is the upstream type whose fields travel as JSON for payload
	// and result rows; the host's Go port of it must carry the field.
	WireType string
	Callback bool // the field holds a function (a callback into the extension)
}

// upstreamFiles are the Pi sources the surface reads: types.ts and the files
// whose declarations it re-exports or exposes through its members.
var upstreamFiles = []string{
	"packages/coding-agent/src/core/extensions/types.ts",
	"packages/coding-agent/src/core/event-bus.ts",
	"packages/coding-agent/src/core/exec.ts",
	"packages/coding-agent/src/core/cache-warmer.ts",
	"packages/coding-agent/src/core/session-manager.ts",
	"packages/coding-agent/src/core/model-registry.ts",
	"packages/coding-agent/src/core/messages.ts",
	"packages/coding-agent/src/core/slash-commands.ts",
	"packages/coding-agent/src/core/source-info.ts",
	"packages/coding-agent/src/modes/interactive/theme/theme.ts",
	"packages/agent/src/types.ts",
}

func loadUpstream(mirror string) (*tsModule, error) {
	m := newTSModule()
	for _, f := range upstreamFiles {
		if err := m.load(filepath.Join(mirror, f), f); err != nil {
			return nil, fmt.Errorf("read upstream %s (run `make upstream-mirror`): %w", f, err)
		}
	}
	return m, nil
}

// fieldExpandable names the option and return types whose fields become rows.
var fieldExpandable = map[string]bool{
	"ExecOptions": true, "ExecResult": true, "CompactOptions": true, "ContextUsage": true,
	"ExtensionUIDialogOptions": true, "ExtensionWidgetOptions": true, "WorkingIndicatorOptions": true,
	"ToolInfo": true, "SlashCommandInfo": true,
}

var promiseRE = regexp.MustCompile(`^Promise<(.*)>$`)

// buildSurface enumerates every extension-facing surface of Pi's API from
// the parsed upstream declarations.
func buildSurface(m *tsModule) ([]surfaceRow, error) {
	var rows []surfaceRow
	add := func(r surfaceRow) { rows = append(rows, r) }

	api, err := m.decl("ExtensionAPI")
	if err != nil {
		return nil, err
	}

	// pi.* members and their argument/option/return fields.
	seen := map[string]bool{}
	for _, mem := range api.Members {
		if mem.Name == "on" || seen[mem.Name] {
			continue
		}
		seen[mem.Name] = true
		if mem.Name == "events" {
			bus, err := m.fields("EventBus")
			if err != nil {
				return nil, err
			}
			for _, b := range bus {
				add(surfaceRow{Group: "pi.events (EventBus)", Key: "pi.events." + b.Name, Decl: "EventBus." + b.Name, Kind: kindMember, Root: "events", Name: b.Name})
			}
			continue
		}
		add(surfaceRow{Group: "pi (ExtensionAPI)", Key: "pi." + mem.Name, Decl: "ExtensionAPI." + mem.Name, Kind: kindMember, Root: "pi", Name: mem.Name})
		if err := addCallFields(m, &rows, "pi (ExtensionAPI) arguments", "pi."+mem.Name, "ExtensionAPI."+mem.Name, "pi", mem, api); err != nil {
			return nil, err
		}
	}

	// Events: every pi.on overload, its payload fields and its result fields.
	for _, mem := range api.Members {
		if mem.Name != "on" {
			continue
		}
		params := methodParams(mem.Type)
		if len(params) != 2 {
			return nil, fmt.Errorf("unexpected pi.on overload %q", mem.Type)
		}
		event := strings.Trim(params[0].Type, `"`)
		payloadType, resultType, err := handlerTypes(m, params[1].Type)
		if err != nil {
			return nil, fmt.Errorf("pi.on(%q): %w", event, err)
		}
		add(surfaceRow{Group: "Events", Key: fmt.Sprintf("pi.on(%q)", event), Decl: "ExtensionAPI.on", Kind: kindEvent, Root: "event", Name: event, Event: event, WireType: payloadType})
		payload, err := m.fields(payloadType)
		if err != nil {
			return nil, err
		}
		for _, f := range payload {
			if f.Name == "type" {
				continue
			}
			add(surfaceRow{Group: "Event payloads", Key: fmt.Sprintf("%s event.%s", event, f.Name), Decl: payloadType + "." + f.Name, Kind: kindPayload, Root: "event", Name: f.Name, Event: event, WireType: payloadType})
		}
		if resultType == "" {
			continue
		}
		result, err := m.fields(resultType)
		if err != nil {
			return nil, err
		}
		if len(result) == 0 {
			add(surfaceRow{Group: "Event results", Key: fmt.Sprintf("%s return value", event), Decl: resultType, Kind: kindResult, Root: "event", Name: "", Event: event, WireType: resultType})
		}
		for _, f := range result {
			add(surfaceRow{Group: "Event results", Key: fmt.Sprintf("%s return.%s", event, f.Name), Decl: resultType + "." + f.Name, Kind: kindResult, Root: "event", Name: f.Name, Event: event, WireType: resultType})
		}
	}

	// ctx.*: ExtensionContext, then ExtensionCommandContext and the
	// replacement-session context passed to withSession callbacks.
	type ctxLayer struct{ decl, group string }
	ctxSeen := map[string]bool{}
	for _, layer := range []ctxLayer{
		{"ExtensionContext", "ctx (ExtensionContext)"},
		{"ExtensionCommandContext", "ctx (ExtensionCommandContext, command handlers)"},
		{"ReplacedSessionContext", "ctx (ReplacedSessionContext, withSession callbacks)"},
	} {
		d, err := m.decl(layer.decl)
		if err != nil {
			return nil, err
		}
		for _, mem := range d.Members {
			if ctxSeen[mem.Name] {
				continue
			}
			ctxSeen[mem.Name] = true
			switch mem.Name {
			case "ui":
				continue // expanded below
			case "sessionManager":
				add(surfaceRow{Group: layer.group, Key: "ctx.sessionManager", Decl: layer.decl + ".sessionManager", Kind: kindMember, Root: "ctx", Name: mem.Name})
				sm, err := m.fields("ReadonlySessionManager")
				if err != nil {
					return nil, err
				}
				for _, s := range sm {
					add(surfaceRow{Group: "ctx.sessionManager (ReadonlySessionManager)", Key: "ctx.sessionManager." + s.Name, Decl: "ReadonlySessionManager." + s.Name, Kind: kindMember, Root: "sessionManager", Name: s.Name})
				}
				continue
			case "modelRegistry":
				add(surfaceRow{Group: layer.group, Key: "ctx.modelRegistry", Decl: layer.decl + ".modelRegistry", Kind: kindMember, Root: "ctx", Name: mem.Name})
				mr, err := m.fields("ModelRegistry")
				if err != nil {
					return nil, err
				}
				for _, s := range mr {
					add(surfaceRow{Group: "ctx.modelRegistry (ModelRegistry)", Key: "ctx.modelRegistry." + s.Name, Decl: "ModelRegistry." + s.Name, Kind: kindMember, Root: "modelRegistry", Name: s.Name})
				}
				continue
			}
			key := "ctx." + mem.Name
			root := "ctx"
			if layer.decl == "ReplacedSessionContext" {
				key = "withSession ctx." + mem.Name
				root = "replacedCtx"
			}
			add(surfaceRow{Group: layer.group, Key: key, Decl: layer.decl + "." + mem.Name, Kind: kindMember, Root: root, Name: mem.Name})
			if err := addCallFields(m, &rows, layer.group+" arguments", key, layer.decl+"."+mem.Name, root, mem, d); err != nil {
				return nil, err
			}
		}
	}

	// ctx.ui.* and ctx.ui.theme.*.
	ui, err := m.decl("ExtensionUIContext")
	if err != nil {
		return nil, err
	}
	uiSeen := map[string]bool{}
	for _, mem := range ui.Members {
		if uiSeen[mem.Name] {
			continue
		}
		uiSeen[mem.Name] = true
		if mem.Name == "theme" {
			add(surfaceRow{Group: "ctx.ui (ExtensionUIContext)", Key: "ctx.ui.theme", Decl: "ExtensionUIContext.theme", Kind: kindMember, Root: "ui", Name: "theme"})
			theme, err := m.fields("Theme")
			if err != nil {
				return nil, err
			}
			for _, t := range theme {
				add(surfaceRow{Group: "ctx.ui.theme (Theme)", Key: "ctx.ui.theme." + t.Name, Decl: "Theme." + t.Name, Kind: kindMember, Root: "theme", Name: t.Name})
			}
			continue
		}
		add(surfaceRow{Group: "ctx.ui (ExtensionUIContext)", Key: "ctx.ui." + mem.Name, Decl: "ExtensionUIContext." + mem.Name, Kind: kindMember, Root: "ui", Name: mem.Name})
		if err := addCallFields(m, &rows, "ctx.ui arguments", "ctx.ui."+mem.Name, "ExtensionUIContext."+mem.Name, "ui", mem, ui); err != nil {
			return nil, err
		}
	}

	// Definition shapes the extension hands to Pi, and the shapes Pi hands
	// back to its callbacks.
	type shape struct{ group, prefix, typ, root string }
	for _, s := range []shape{
		{"Tool definition (registerTool)", "tool", "ToolDefinition", "tool"},
		{"Tool definition (registerTool)", "tool execute result", "AgentToolResult", "toolResult"},
		{"Tool definition (registerTool)", "tool render context", "ToolRenderContext", "toolRender"},
		{"Tool definition (registerTool)", "tool renderResult options", "ToolRenderResultOptions", "toolRenderOptions"},
		{"Command definition (registerCommand)", "command", `Omit<RegisteredCommand, "name" | "sourceInfo">`, "command"},
		{"Provider definition (registerProvider)", "provider", "ProviderConfig", "provider"},
		{"Provider definition (registerProvider)", "provider model", "ProviderModelConfig", "providerModel"},
		{"Renderers", "message renderer options", "MessageRenderOptions", "messageRenderOptions"},
		{"Renderers", "entry renderer options", "EntryRenderOptions", "entryRenderOptions"},
		{"Renderers", "markdown transform context", "MarkdownTransformContext", "markdownContext"},
	} {
		fs, err := m.fields(s.typ)
		if err != nil {
			return nil, err
		}
		if len(fs) == 0 {
			return nil, fmt.Errorf("upstream %s has no fields", s.typ)
		}
		for _, f := range fs {
			add(surfaceRow{Group: s.group, Key: s.prefix + "." + f.Name, Decl: declName(s.typ) + "." + f.Name, Kind: kindField, Root: s.root, Name: f.Name, Callback: isCallback(f)})
			if s.typ == "ProviderConfig" && f.Name == "oauth" {
				oauth, err := m.fields(f.Type)
				if err != nil {
					return nil, err
				}
				for _, o := range oauth {
					add(surfaceRow{Group: s.group, Key: "provider.oauth." + o.Name, Decl: "ProviderConfig.oauth." + o.Name, Kind: kindField, Root: "providerOAuth", Name: o.Name, Callback: isCallback(o)})
				}
			}
		}
	}
	return rows, nil
}

// addCallFields adds rows for the fields of a member's object-shaped
// parameters and its object-shaped return value.
func addCallFields(m *tsModule, rows *[]surfaceRow, group, key, decl, root string, mem tsMember, owner *tsDecl) error {
	if !mem.Method && !strings.Contains(mem.Type, "=>") {
		return nil
	}
	// Union overloads: collect params across every overload of this member.
	var sigs []string
	for _, o := range owner.Members {
		if o.Name == mem.Name && o.Method {
			sigs = append(sigs, o.Type)
		}
	}
	seen := map[string]bool{}
	for _, sig := range sigs {
		for _, p := range methodParams(sig) {
			if !expandableParam(p.Type) {
				continue
			}
			fs, err := m.fields(p.Type)
			if err != nil {
				return err
			}
			for _, f := range fs {
				k := fmt.Sprintf("%s(%s.%s)", key, p.Name, f.Name)
				if seen[k] {
					continue
				}
				seen[k] = true
				*rows = append(*rows, surfaceRow{Group: group, Key: k, Decl: fmt.Sprintf("%s(%s.%s)", decl, p.Name, f.Name), Kind: kindField, Root: root + "Arg", Name: mem.Name + "." + p.Name + "." + f.Name, Callback: isCallback(f)})
			}
		}
		ret := returnType(sig)
		if ret == "" || !expandableReturn(ret) {
			continue
		}
		fs, err := m.fields(strings.TrimSuffix(ret, "[]"))
		if err != nil {
			return err
		}
		for _, f := range fs {
			k := fmt.Sprintf("%s → %s", key, f.Name)
			if seen[k] {
				continue
			}
			seen[k] = true
			*rows = append(*rows, surfaceRow{Group: group, Key: k, Decl: fmt.Sprintf("%s → %s", decl, f.Name), Kind: kindField, Root: root + "Return", Name: mem.Name + "." + f.Name})
		}
	}
	return nil
}

func expandableParam(t string) bool {
	t = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(t), "| undefined"))
	if strings.HasPrefix(t, "{") || strings.HasPrefix(t, "Pick<") {
		return true
	}
	if strings.Contains(t, "=>") {
		return false // a function type, or a union with one
	}
	if strings.HasPrefix(t, "|") {
		return true
	}
	name, _ := leadingIdent(t)
	return fieldExpandable[name]
}

func expandableReturn(t string) bool {
	if strings.HasPrefix(t, "{") {
		return true
	}
	name, _ := leadingIdent(strings.TrimSuffix(t, "[]"))
	return fieldExpandable[name]
}

// returnType extracts a method signature's return type, unwrapping Promise
// and dropping "| undefined" and "| null".
func returnType(sig string) string {
	sig = strings.TrimSpace(skipTypeParams(sig))
	depth := 0
	for i := 0; i < len(sig); i++ {
		switch sig[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				rest := strings.TrimSpace(sig[i+1:])
				rest = strings.TrimSpace(strings.TrimPrefix(rest, ":"))
				rest = strings.TrimPrefix(rest, "=>")
				rest = strings.TrimSpace(rest)
				if sm := promiseRE.FindStringSubmatch(rest); sm != nil {
					rest = sm[1]
				}
				for _, drop := range []string{"| undefined", "| null"} {
					rest = strings.TrimSpace(strings.ReplaceAll(rest, drop, ""))
				}
				return rest
			}
		}
	}
	return ""
}

// handlerTypes returns the payload and result types of a pi.on handler type:
// ExtensionHandler<E, R>, or a named handler alias such as ProjectTrustHandler.
func handlerTypes(m *tsModule, handler string) (string, string, error) {
	handler = strings.TrimSpace(handler)
	if strings.HasPrefix(handler, "ExtensionHandler<") {
		args := typeArgs(handler)
		if len(args) == 1 {
			return args[0], "", nil
		}
		return args[0], args[1], nil
	}
	d, err := m.decl(handler)
	if err != nil {
		return "", "", err
	}
	params := methodParams(d.Alias)
	if len(params) == 0 {
		return "", "", fmt.Errorf("handler %s has no parameters", handler)
	}
	ret := returnType(d.Alias)
	if parts := splitTopLevel(ret, '|'); len(parts) > 0 {
		ret = strings.TrimSpace(parts[len(parts)-1])
	}
	return params[0].Type, ret, nil
}

func declName(t string) string {
	if after, ok := strings.CutPrefix(t, "Omit<"); ok {
		name, _ := leadingIdent(after)
		return name
	}
	return t
}

func isCallback(f tsMember) bool {
	if f.Method {
		return true
	}
	t := strings.TrimSpace(f.Type)
	return strings.HasPrefix(t, "(") && strings.Contains(t, "=>")
}
