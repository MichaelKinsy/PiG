// Headless JSONL command and event loop.
//
// The command set and wire shapes follow the pinned Pi rpc-mode.ts and
// rpc-types.ts. Extension UI dialogs use extension_ui_request and
// extension_ui_response records on the same stream.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	"github.com/MichaelKinsy/PiG/coding/pigletbuild/binarypiglet"
	"github.com/MichaelKinsy/PiG/coding/rpcclient"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	codingexport "github.com/MichaelKinsy/PiG/internal/codingagent/export"
	"github.com/MichaelKinsy/PiG/internal/codingagent/llama"
	"github.com/MichaelKinsy/PiG/internal/codingagent/prompts"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

type rpcTaskGroup struct {
	mu     sync.Mutex
	wg     sync.WaitGroup
	closed bool
}

func (g *rpcTaskGroup) Go(task func()) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return false
	}
	g.wg.Go(task)
	return true
}

func (g *rpcTaskGroup) CloseAndWait() {
	g.mu.Lock()
	g.closed = true
	g.mu.Unlock()
	g.wg.Wait()
}

type rpcModeResources struct {
	StartupExtensions *startupExtensionSet
	// PreTrustExtensionDiagnostics are the pre-trust load errors, reported
	// with the final load errors as upstream loadFinalExtensionSet does.
	PreTrustExtensionDiagnostics []codingagent.AgentSessionRuntimeDiagnostic
	Services                     *coding.Services
	PromptTemplates              []codingagent.PromptTemplate
	Skills                       []*codingagent.SkillDef
	SourceInfo                   map[string]codingagent.ResourceSourceInfo
	ExtensionConfigs             []subprocess.ExtConfig
	EmbeddedCells                []subprocess.EmbeddedCell
	ProjectTrusted               bool
	ResumePath                   string
}

type headlessCommandRunner interface {
	Commands() []extension.ResolvedCommand
	Command(string) (extension.ResolvedCommand, bool)
	ExecuteCommand(context.Context, string, string) bool
}

// headlessCommandCatalog routes the prompts of print, JSON and RPC mode as
// upstream AgentSession.prompt does: an extension command runs instead of
// prompting, and other text has skill commands and prompt templates expanded.
// It also lists the commands upstream getCommands and get_commands report.
type headlessCommandCatalog struct {
	runner headlessCommandRunner
	// mode is the command context's mode: "print", "json" or "rpc".
	mode string
	// llama is the built-in llama.cpp extension's /llama command; notify
	// carries its ctx.ui.notify calls to the client.
	llama           *llama.Host
	notify          func(message, kind string)
	promptTemplates []codingagent.PromptTemplate
	skills          []*codingagent.SkillDef
	cwd             string
	agentDir        string
	sourceInfo      map[string]codingagent.ResourceSourceInfo
}

// commands lists the catalog as upstream get_commands does.
func (c headlessCommandCatalog) commands() []RPCSlashCommand {
	return c.slashCatalog().Commands()
}

// slashCatalog is the shared getCommands catalog over this command set.
func (c headlessCommandCatalog) slashCatalog() codingagent.SlashCommandCatalog {
	catalog := codingagent.SlashCommandCatalog{
		PromptTemplates: c.promptTemplates, Skills: c.skills,
		CWD: c.cwd, AgentDir: c.agentDir, SourceInfo: c.sourceInfo,
	}
	if c.runner != nil {
		catalog.Runner = c.runner
	}
	if c.llama != nil {
		catalog.Inline = []codingagent.PiSlashCommand{codingagent.LlamaSlashCommand()}
	}
	return catalog
}

func (c headlessCommandCatalog) extensionCommand(message string) (string, string, bool) {
	if !strings.HasPrefix(message, "/") {
		return "", "", false
	}
	requestedNameAndArgs := message[1:]
	requestedName, args, found := strings.Cut(requestedNameAndArgs, " ")
	if !found {
		args = ""
	}
	if c.llama != nil && requestedName == llama.CommandName {
		return llama.CommandName, args, true
	}
	if c.runner == nil {
		return "", "", false
	}
	for _, command := range c.runner.Commands() {
		if strings.TrimPrefix(command.InvocationName, "/") == requestedName {
			return command.InvocationName, args, true
		}
	}
	return "", "", false
}

func (c headlessCommandCatalog) expandPrompt(message string) string {
	if expanded, ok := codingagent.ExpandSkillCommand(message, c.skills); ok {
		message = expanded
	}
	if expanded, ok := codingagent.ExpandPromptTemplate(message, c.promptTemplates); ok {
		message = expanded
	}
	return message
}

func (c headlessCommandCatalog) routePrompt(ctx context.Context, message string) (string, bool) {
	if name, args, ok := c.extensionCommand(message); ok {
		return "", c.executeCommand(ctx, name, args)
	}
	return c.expandPrompt(message), false
}

// executeCommand runs a command extensionCommand resolved; /llama runs the
// built-in llama.cpp command with this mode's command context.
func (c headlessCommandCatalog) executeCommand(ctx context.Context, name, args string) bool {
	if c.llama != nil && name == llama.CommandName {
		_ = c.llama.HandleCommand(llama.CommandContext{Ctx: ctx, Mode: c.mode, Notify: c.notify})
		return true
	}
	return c.runner.ExecuteCommand(ctx, name, args)
}

func (c headlessCommandCatalog) sourceInfoForPath(path, kind string) RPCSourceInfo {
	return c.slashCatalog().SourceInfoForPath(path, kind)
}

func rpcGetCommandsResponse(id string, catalog headlessCommandCatalog) RPCResponse {
	return rpcSuccess(id, "get_commands", RPCGetCommandsData{Commands: catalog.commands()})
}

func rpcExtensionConfigs(configs []subprocess.ExtConfig, cwd, agentDir string, sourceInfo map[string]codingagent.ResourceSourceInfo) []subprocess.ExtConfig {
	out := append([]subprocess.ExtConfig(nil), configs...)
	catalog := headlessCommandCatalog{cwd: cwd, agentDir: agentDir, sourceInfo: sourceInfo}
	for i := range out {
		if out[i].SourceInfo != nil {
			// Already stamped where it was collected (a -e extension).
			continue
		}
		path := out[i].Source
		if path == "" {
			path = out[i].Path
		}
		if path == "" {
			path = "builtin:" + out[i].Name
		}
		info := catalog.sourceInfoForPath(path, "extensions")
		if strings.HasPrefix(path, "builtin:") {
			info.Source = "builtin"
			info.BaseDir = ""
		}
		out[i].SourceInfo = info
	}
	return out
}

func rpcResolvedSkills(skills []*codingagent.SkillDef, activePiglet *piglet.Piglet) []*codingagent.SkillDef {
	out := make([]*codingagent.SkillDef, len(skills))
	for i, skill := range skills {
		copy := *skill
		if copy.Path == "" && activePiglet != nil {
			copy.Path = activePiglet.SourcePath()
			if copy.Path == "" {
				copy.Path = "builtin:piglet"
			}
			copy.Dir = filepath.Dir(copy.Path)
		}
		out[i] = &copy
	}
	return out
}

// runRPCMode is the entrypoint for `pig --rpc`.
// It initialises a coding.Session, starts the event forwarder goroutine,
// then reads commands from stdin until EOF.
// Returns 0 on clean shutdown, 1 on fatal error.
func runRPCMode(ctx context.Context, flags CLIFlags, activePiglet *piglet.Piglet, resources rpcModeResources) int {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var writeMu sync.Mutex
	writeRPC := func(v any) {
		writeMu.Lock()
		defer writeMu.Unlock()
		writeJSONLine(os.Stdout, v)
	}
	rpcUI := newRPCUIContext(writeRPC)
	defer rpcUI.Close()

	// ── Services & model ───────────────────────────────────────────────────

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pig --mode rpc: getwd: %v\n", err)
		return 1
	}
	agentDir := os.Getenv("PIG_CODING_AGENT_DIR")
	if agentDir == "" {
		agentDir = codingagent.DefaultAgentDir()
	}

	services := resources.Services
	if services == nil {
		services, err = coding.NewServices(coding.ServicesOptions{
			CWD:            cwd,
			AgentDir:       agentDir,
			ProjectTrusted: new(resources.ProjectTrusted),
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "pig --rpc: services: %v\n", err)
			return 1
		}
	}
	llamaHost := startBuiltInLlama(ctx, services)
	refreshCatalogsInBackground(ctx, llamaHost)
	settings := services.Settings()

	registry := services.Registry()
	// RPC refreshes dynamic model catalogs (Radius) in the background, as
	// upstream main.ts does for RPC mode unless offline (15 s timeout).
	if codingagent.ModelNetworkEnabled() {
		go func() {
			refreshCtx, cancelRefresh := context.WithTimeout(ctx, 15*time.Second)
			defer cancelRefresh()
			registry.RefreshCatalogs(refreshCtx, codingagent.CatalogRefreshOptions{AllowNetwork: true})
		}()
	}
	inlinePigletSystemPrompt(activePiglet)
	applyPigletPreStart(activePiglet, &flags)
	modelFlag := flags.Model
	if modelFlag == "" {
		modelFlag = pigletModelSpec(activePiglet)
	}
	scopePatterns := flags.Models
	if len(scopePatterns) == 0 {
		scopePatterns = settings.EnabledModels
	}
	selected, err := selectStartupModel(ctx, startupModelOptions{
		CLIProvider:   flags.Provider,
		CLIModel:      modelFlag,
		CLIThinking:   flags.Thinking,
		ScopePatterns: scopePatterns,
		Continuing:    resources.ResumePath != "",
		APIKey:        flags.APIKey,
	}, settings, registry.ModelRegistry)
	for _, warning := range selected.Warnings {
		printModelDiagnostic(warning)
	}
	model := selected.Model
	if flags.Thinking == "" {
		flags.Thinking = selected.Thinking
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "pig --rpc: model: %v\n", err)
		return 1
	}

	// ── Extensions ─────────────────────────────────────────────────────────
	// Load subprocess extensions from CLI -e paths. In RPC mode,
	// --no-extensions suppresses auto-discovery but -e paths are
	// still honoured (same semantics as interactive mode).
	var subprocExts []extension.Extension
	var subprocHost *subprocess.Host

	var extraExtConfigs = append([]subprocess.ExtConfig(nil), resources.ExtensionConfigs...)
	var embeddedCells = append([]subprocess.EmbeddedCell(nil), resources.EmbeddedCells...)
	if activePiglet != nil && binarypiglet.IsPigletBinary() && len(activePiglet.Extensions) > 0 {
		flags.NoExtensions = true
	}
	var subprocBridge *subprocess.UIBridge
	var loadErrs []error
	if !flags.NoExtensions || len(extraExtConfigs) > 0 || len(embeddedCells) > 0 {
		subprocExts, subprocHost, subprocBridge, loadErrs = loadFinalSubprocessExtensions(
			ctx, cwd, extension.ModeRPC, registry.ModelRegistry, extraExtConfigs,
			embeddedCells, nil, resources.StartupExtensions,
		)
	}
	// Mirrors upstream main.ts: every extension load error, pre-trust and
	// final, including tool and flag conflicts, is reported once and ends RPC
	// startup.
	if diagnostics := slices.Concat(resources.PreTrustExtensionDiagnostics, extensionLoadDiagnostics(loadErrs), extensionConflictDiagnostics(codingagent.DetectExtensionConflicts(subprocExts))); len(diagnostics) > 0 {
		if subprocHost != nil {
			subprocHost.Shutdown("extension load failure")
		}
		reportExtensionLoadFailures(diagnostics)
		return 1
	}
	if subprocHost != nil {
		defer subprocHost.Shutdown("rpc-exit")
	}
	if subprocBridge != nil {
		subprocBridge.SetUIContext(rpcUI)
		subprocBridge.SetWidgetRequestFunc(func(_ string, key string, lines []string, opts extension.ExtensionWidgetOptions) {
			rpcUI.SetWidget(key, lines, opts)
		})
		var widgetMu sync.Mutex
		previousWidgets := make(map[string][]string)
		subprocBridge.SetWidgetSyncFunc(func(widgets map[string]*subprocess.PushProxy) {
			widgetMu.Lock()
			defer widgetMu.Unlock()
			keys := make([]string, 0, len(widgets))
			for key := range widgets {
				keys = append(keys, key)
			}
			slices.Sort(keys)
			for _, qualified := range keys {
				lines := widgets[qualified].Lines()
				previous, exists := previousWidgets[qualified]
				if exists && slices.Equal(previous, lines) {
					continue
				}
				key := qualified
				if _, suffix, ok := strings.Cut(qualified, ":"); ok {
					key = suffix
				}
				rpcUI.SetWidget(key, lines, nil)
				previousWidgets[qualified] = append([]string(nil), lines...)
			}
			for qualified := range previousWidgets {
				if _, exists := widgets[qualified]; exists {
					continue
				}
				key := qualified
				if _, suffix, ok := strings.Cut(qualified, ":"); ok {
					key = suffix
				}
				rpcUI.SetWidget(key, nil, nil)
				delete(previousWidgets, qualified)
			}
		})
	}

	// Log loaded extension tools for diagnostics.
	if len(subprocExts) > 0 {
		totalTools := 0
		for _, ext := range subprocExts {
			totalTools += len(ext.Tools)
			if len(ext.Tools) > 0 {
				var names []string
				for name := range ext.Tools {
					names = append(names, name)
				}
				fmt.Fprintf(os.Stderr, "pig --rpc: extension %q: %d tools: %v\n", ext.Name, len(ext.Tools), names)
			}
		}
		fmt.Fprintf(os.Stderr, "pig --rpc: loaded %d extensions, %d total tools\n", len(subprocExts), totalTools)
	}

	// An active Piglet uses the generic in-process extension runner for runtime
	// scoping and read-only inspection. Bare Stock Pig registers no extra command.
	if activePiglet != nil {
		subprocExts = append(subprocExts, piglet.BuildExtensionWithPiglet(activePiglet))
	}

	// ── Session ────────────────────────────────────────────────────────────
	// Keep the full Session tool registry available to extension/piglet scoping,
	// while the startup agent loadout follows the coding-agent defaults.
	defaultToolNames := []string{"read", "bash", "edit", "write"}
	if settings.DefaultTools != nil {
		defaultToolNames = settings.DefaultTools
	}
	var agentToolNames []string
	var allowed map[string]struct{}
	if !flags.NoBuiltinTools {
		switch {
		case flags.NoTools:
			allowed = map[string]struct{}{} // empty = block all
		case len(flags.Tools) > 0:
			allowed = make(map[string]struct{}, len(flags.Tools))
			for _, t := range flags.Tools {
				allowed[t] = struct{}{}
			}
			for _, n := range tools.BuiltinToolNames() {
				if _, ok := allowed[n]; ok {
					agentToolNames = append(agentToolNames, n)
				}
			}
		default:
			agentToolNames = append([]string(nil), defaultToolNames...)
		}
	}
	// Add extension-contributed tool names so the system prompt
	// mentions them and the agent loop offers them to the LLM.
	for _, ext := range subprocExts {
		for name := range ext.Tools {
			agentToolNames = append(agentToolNames, name)
		}
	}

	promptSkills := promptSkillsFor(resources.Skills)
	contextFiles := loadContextFiles(cwd, agentDir, flags.NoContextFiles)
	resolvedPrompts := resolvePromptInputs(cwd, agentDir, flags, resources.ProjectTrusted)
	promptOptions := prompts.Options{
		Cwd:                cwd,
		Tools:              agentToolNames,
		ToolHints:          prompts.DefaultToolSnippets(),
		ToolGuidelines:     tools.DefaultToolGuidelines(),
		Skills:             promptSkills,
		PigDocsPath:        filepath.Join(codingagent.ConfigRoot(), "docs"),
		ContextFiles:       toPromptContextFiles(contextFiles),
		CustomPrompt:       resolvedPrompts.custom,
		AppendSystemPrompt: resolvedPrompts.append,
	}
	if resolvedPrompts.custom != "" {
		promptOptions.AppendMode = "replace"
	}
	systemPromptSections := prompts.BuildSystemPromptSections(promptOptions)
	systemPrompt := prompts.BuildDefaultPrompt(promptOptions)

	rt, err := coding.NewRuntime(coding.RuntimeOptions{
		Services:      services,
		NewExtensions: subprocExts,
		AbortContext:  ctx,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "pig --rpc: runtime: %v\n", err)
		return 1
	}
	defer func() { _ = rt.Close() }()

	// sessPtr is set after session creation so runtime callbacks can access it.
	var sessPtr *coding.Session
	var extensionEvents rpcTaskGroup

	// Wire in-process extension context actions (GetAllTools, SetActiveTools)
	// so the piglet extension can scope tools during before_agent_start.
	// Mirrors interactive mode's wireInprocContextActions().
	runner := rt.NewExtensionRunner()
	if runner != nil {
		runner.AddErrorListener(func(err *extension.ExtensionError) {
			writeRPC(rpcExtensionErrorEvent(err))
		})
		runner.SetUIContext(rpcUI)
		// Subprocess dialogs reach rpcUI through the bridge; report their
		// ui_prompt_start/ui_prompt_end through this runner.
		if subprocBridge != nil {
			subprocBridge.SetUIPromptScope(runner)
		}
	}
	commandCatalog := headlessCommandCatalog{
		runner: runner, mode: "rpc", promptTemplates: resources.PromptTemplates, skills: resources.Skills,
		cwd: cwd, agentDir: agentDir, sourceInfo: resources.SourceInfo,
		llama: llamaHost, notify: rpcUI.Notify,
	}
	// Extension host calls read the published copy of the catalog; this
	// goroutine alone changes commandCatalog.
	var publishedCatalog atomic.Pointer[headlessCommandCatalog]
	publishedCatalog.Store(new(commandCatalog))
	// Session-backed actions (sendUserMessage, isIdle, abort,
	// hasPendingMessages, waitForIdle) for in-process and subprocess
	// extensions, as upstream rpc-mode binds the session in bindExtensions.
	if runner != nil || subprocBridge != nil {
		bindSessionExtensionActions(runner, subprocBridge, func() *coding.Session { return sessPtr },
			extension.ContextActions{
				GetAllTools: func() []extension.ToolInfo {
					tools := runner.Tools()
					result := make([]extension.ToolInfo, len(tools))
					for i, t := range tools {
						source := "builtin"
						if s, ok := t.SourceInfo.(string); ok && s != "" {
							source = s
						}
						result[i] = extension.ToolInfo{
							Name:        t.Definition.Name,
							Description: t.Definition.Description,
							SourceInfo:  source,
						}
					}
					return result
				},
				GetActiveTools: func() []string {
					if sessPtr != nil {
						agentTools := sessPtr.Agent().Tools()
						names := make([]string, len(agentTools))
						for i, t := range agentTools {
							names[i] = t.Name()
						}
						return names
					}
					tools := runner.Tools()
					names := make([]string, len(tools))
					for i, t := range tools {
						names[i] = t.Definition.Name
					}
					return names
				},
				SetActiveTools: func(names []string) {
					if sessPtr == nil {
						return // session not created yet; scoping deferred to before_agent_start
					}
					allowed := make(map[string]struct{}, len(names))
					for _, n := range names {
						allowed[n] = struct{}{}
					}
					allTools := sessPtr.Tools()
					var filtered []agent.AgentTool
					for _, t := range allTools {
						if _, ok := allowed[t.Name()]; ok {
							filtered = append(filtered, t)
						}
					}
					sessPtr.Agent().SetTools(filtered)
				},
				GetFlagValue: func(name string) any {
					return nil
				},
				IsProjectTrusted: func() bool {
					return resources.ProjectTrusted
				},
				// RPC mode. Mirrors upstream rpc-mode.ts:320 (`mode: "rpc"`).
				Mode:          extension.ModeRPC,
				ModelRegistry: services.Registry(),
			})
	}

	sessionDir, err := resolveSessionDir(flags.SessionDir, services.SettingsManager())
	if err != nil {
		fmt.Fprintf(os.Stderr, "pig --rpc: session: %v\n", err)
		return 1
	}
	sess, err := rt.New(coding.SessionStartOptions{
		Model:                model,
		SystemPrompt:         systemPrompt,
		SystemPromptSections: systemPromptSections,
		AllowedTools:         allowed,
		SkipBuiltinTools:     flags.NoBuiltinTools,
		SessionDir:           sessionDir,
		SessionID:            flags.SessionID,
		NoSession:            flags.NoSession,
		ResumePath:           resources.ResumePath,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "pig --rpc: session: %v\n", err)
		return 1
	}
	defer func() { _ = sess.Close() }()
	sessPtr = sess // enable piglet scoping callbacks
	rpcSetInitialActiveTools(sess, agentToolNames)

	toolSource := make(map[string]string)
	for _, ext := range subprocExts {
		for toolName := range ext.Tools {
			toolSource[toolName] = ext.Name
		}
	}
	if activePiglet != nil {
		allTools := sess.Tools()
		infos := make([]piglet.ToolInfo, len(allTools))
		for i, t := range allTools {
			source := toolSource[t.Name()]
			if source == "" {
				source = "builtin"
			}
			infos[i] = piglet.ToolInfo{Name: t.Name(), Source: source}
		}
		allowedNames := piglet.ScopeTools(activePiglet, infos)
		allowedSet := make(map[string]struct{}, len(allowedNames))
		for _, name := range allowedNames {
			allowedSet[name] = struct{}{}
		}
		var filtered []agent.AgentTool
		for _, t := range allTools {
			if _, ok := allowedSet[t.Name()]; ok {
				filtered = append(filtered, t)
			}
		}
		sess.Agent().SetTools(filtered)
	}

	// Wire extension host callbacks for RPC mode.
	// In interactive mode these are wired in interactive.go:4249+.
	// Subprocess extensions (like piglet) call getAllTools/setActiveTools
	// via JSON-RPC through the UIBridge: without these, those calls return
	// empty/no-op. Mirrors upstream agent-session.ts:2197-2199.
	if subprocBridge != nil {
		detachModelRegistry := wireSubprocessModelRegistry(subprocBridge, sess, services)
		defer detachModelRegistry()
		registryAllowed, registryExcluded := toolRegistryFilters(flags)
		subprocBridge.SetHostAction("getAllTools", func() []subprocess.ToolInfo {
			return codingagent.ExtensionToolInfos(runner, registryAllowed, registryExcluded)
		})
		subprocBridge.SetHostAction("getCommands", func() []subprocess.CommandInfo {
			return publishedCatalog.Load().slashCatalog().SubprocessCommands()
		})
		subprocBridge.SetHostAction("getSessionName", func() string { return sess.SessionName() })
		subprocBridge.SetHostAction("getSessionID", func() string { return sess.ID() })
		subprocBridge.SetHostAction("getSessionFile", func() string { return sess.Path() })
		subprocBridge.SetHostAction("getLeafID", func() string {
			if leaf := sess.LeafID(); leaf != nil {
				return *leaf
			}
			return ""
		})
		subprocBridge.SetHostAction("getEntriesPage", func(cursor, maxBytes int) ([]json.RawMessage, int, bool, string) {
			entries := sess.Inner().Entries()
			if cursor < 0 || cursor > len(entries) {
				cursor = 0
			}
			page := make([]json.RawMessage, 0)
			bytes := 0
			next := cursor
			for next < len(entries) {
				raw := entries[next].Raw()
				entryBytes := len(raw) + 1
				if len(page) > 0 && bytes+entryBytes > maxBytes {
					break
				}
				if len(raw) > 0 {
					page = append(page, json.RawMessage(raw))
					bytes += entryBytes
				}
				next++
			}
			leafID := ""
			if leaf := sess.LeafID(); leaf != nil {
				leafID = *leaf
			}
			return page, next, next < len(entries), leafID
		})
		// getActiveTools and setActiveTools come from bindSessionExtensionActions
		// (upstream getActiveToolNames and setActiveToolsByName).
		subprocBridge.SetHostAction("refreshTools", func() {
			// No-op in RPC mode: tools don't change dynamically.
		})
		subprocBridge.SetHostAction("appendEntry", func(customType string, data any) error {
			id, err := codingagent.GenerateEntryID()
			if err != nil {
				return err
			}
			entry := codingagent.CustomEntry{
				SessionEntryBase: codingagent.SessionEntryBase{
					Type:      "custom",
					ID:        id,
					ParentID:  sess.LeafID(),
					Timestamp: codingagent.RFC3339NowNano(),
				},
				CustomType: customType,
				Data:       data,
			}
			if err := sess.Inner().AppendEntry(entry); err != nil {
				return err
			}
			writeRPC(RPCEntryAppendedEvent{
				Type: "entry_appended",
				Entry: RPCEntryAppendedEntry{
					Type: entry.Type, CustomType: entry.CustomType, Data: entry.Data,
					ID: entry.ID, ParentID: entry.ParentID, Timestamp: entry.Timestamp,
				},
			})
			return nil
		})
		subprocBridge.SetHostAction("setSessionName", func(name string) error {
			if err := sess.SetSessionName(name); err != nil {
				return err
			}
			effectiveName := sess.SessionName()
			writeRPC(rpcSessionInfoChanged(effectiveName))
			if runner != nil {
				extensionEvents.Go(func() {
					_, _ = runner.Emit(ctx, extension.SessionInfoChangedEvent{
						Type: codingagent.EventSessionInfoChanged,
						Name: effectiveName,
					})
				})
			}
			return nil
		})
	}

	// Diagnostic: log actual tool count from the agent.
	fmt.Fprintf(os.Stderr, "pig --rpc: agent has %d tools (builtins_skipped=%v)\n",
		len(sess.Agent().Tools()), flags.NoBuiltinTools)

	// Pi's unknown model supports only off. A selected model uses the CLI
	// preference, which the normal model-resolution path already validates.
	if model == nil {
		sess.Agent().SetThinkingLevel(ai.ThinkingOff)
	} else if flags.Thinking != "" {
		sess.Agent().SetThinkingLevel(ai.ThinkingLevel(flags.Thinking))
	}

	// Drive the extension session lifecycle in RPC mode. Upstream fires
	// session_start via session.bindExtensions (rpc-mode.ts:318) after wiring
	// the mode's host-action bindings, and session_shutdown on dispose; pig
	// previously fired these only in interactive mode, so extensions were
	// silently skipped under the production `--mode rpc` path. Emitted inline
	// after binding, with shutdown deferred so it only runs when start did.
	sess.EmitSessionStart("startup")
	defer sess.EmitSessionShutdown("quit")
	if commandCatalog.extendFromExtensions(ctx, runner, "startup") {
		promptOptions.Skills = promptSkillsFor(commandCatalog.skills)
		sess.SetSystemPromptSections(prompts.BuildSystemPromptSections(promptOptions))
	}
	publishedCatalog.Store(new(commandCatalog))

	// ── Event forwarder ────────────────────────────────────────────────────
	// Forward upstream-compatible AgentSessionEvent JSON objects. Upstream pi
	// RPC mode simply subscribes to session events and writes each event as a
	// JSONL object (rpc-mode.ts:346); rpcAgentEvent adapts pig's internal
	// event structs to that wire shape.

	// The Session owns streaming state and abort, as upstream reads
	// session.isStreaming and calls session.abort(), so a run an extension
	// started is streaming and abortable too. promptPending covers the gap
	// between accepting an RPC prompt and its run starting.
	var promptPending atomic.Bool
	streaming := func() bool { return sess.IsStreaming() || promptPending.Load() }
	var promptWG sync.WaitGroup
	var bashWG sync.WaitGroup
	var commandWG sync.WaitGroup
	cycleComplete := make(chan struct{})
	close(cycleComplete)

	handleCycleModel := func(id string, currentSession *coding.Session) {
		models, err := rpcAvailableModels(services, currentSession.Model())
		if err != nil {
			writeRPC(rpcError(id, "cycle_model", err.Error()))
			return
		}
		scoped := rpcScopedModels(models, flags.Models)
		if len(scoped) <= 1 {
			writeRPC(rpcSuccessNull(id, "cycle_model"))
			return
		}
		currentIndex := -1
		for i, candidate := range scoped {
			if ai.ModelsAreEqual(candidate, currentSession.Model()) {
				currentIndex = i
				break
			}
		}
		if currentIndex < 0 {
			currentIndex = 0
		}
		next := scoped[(currentIndex+1)%len(scoped)]
		if err := currentSession.CycleToModel(next); err != nil {
			writeRPC(rpcError(id, "cycle_model", err.Error()))
			return
		}
		writeRPC(rpcSuccess(id, "cycle_model", RPCModelCycleResult{
			Model: rpcModelValue(next), ThinkingLevel: currentSession.ThinkingLevel(), IsScoped: len(flags.Models) > 0,
		}))
	}

	// settlePrompt is upstream `await session.abort()`: the active run stops,
	// whoever started it, and its agent_settled is on the wire before the
	// caller answers.
	settlePrompt := func() {
		sess.RequestAbort()
		promptWG.Wait()
		_ = sess.WaitForIdle(context.Background())
		_ = sess.FlushEvents(ctx)
	}
	settleSessionWork := func() {
		commandWG.Wait()
		settlePrompt()
		sess.AbortBash()
		bashWG.Wait()
	}

	// eventsDone is closed when the event forwarder exits.
	eventsDone := make(chan struct{})
	eventConversionErr := make(chan error, 1)

	go func() {
		defer close(eventsDone)
		for ev := range sess.Events() {
			if coding.AcknowledgeEvent(ev) {
				continue
			}
			converted, err := rpcAgentEvent(ev)
			if err != nil {
				eventConversionErr <- err
				writeRPC(RPCErrorEvent{Type: "error", Message: "event conversion error: " + err.Error()})
				cancel()
				return
			}
			for _, outEvent := range converted {
				writeRPC(outEvent)
			}
		}
	}()

	// ── Command loop ───────────────────────────────────────────────────────

	inputLines := make(chan []byte, 64)
	inputDone := make(chan error, 1)
	go func() {
		defer close(inputLines)
		err := rpcclient.ReadJSONLLines(os.Stdin, func(line []byte) bool {
			select {
			case inputLines <- line:
				return true
			case <-ctx.Done():
				return false
			}
		})
		if ctx.Err() != nil {
			inputDone <- nil
			return
		}
		rpcUI.Close()
		inputDone <- err
	}()

	for line := range inputLines {

		env, parseErr := parseRPCCommand(line)
		if parseErr != nil {
			// Upstream uses rpcError(undefined, "parse", message): mirrors
			// rpc-mode.ts:handleInputLine catch block. Translate Go json
			// error wording to upstream's JS SyntaxError wording so the wire
			// shape matches byte-for-byte across binaries.
			writeRPC(rpcError("", "parse",
				fmt.Sprintf("Failed to parse command: %s", jsifyJSONErrorMessage(parseErr))))
			continue
		}

		if env.Type == "extension_ui_response" {
			rpcUI.HandleResponse(env.Raw)
			continue
		}

		switch env.Type {
		// ── prompt ──────────────────────────────────────────────────────
		case "prompt":
			var cmd RPCPromptCommand
			if err := json.Unmarshal(env.Raw, &cmd); err != nil {
				writeRPC(rpcError(env.ID, "prompt", err.Error()))
				continue
			}
			if name, args, ok := commandCatalog.extensionCommand(cmd.Message); ok {
				go func(id, commandName, commandArgs string) {
					commandCatalog.executeCommand(ctx, commandName, commandArgs)
					writeRPC(rpcSuccess(id, "prompt", nil))
				}(env.ID, name, args)
				continue
			}
			if sess.IsCompacting() {
				writeRPC(rpcError(env.ID, "prompt", "Cannot submit a prompt while compaction is in progress. Wait for compaction to finish and retry."))
				continue
			}
			message, images, handled, err := sess.RunInputHandlers(ctx, cmd.Message, rpcImages(cmd.Images), extension.InputSourceRPC, cmd.StreamingBehavior)
			if err != nil {
				writeRPC(rpcError(env.ID, "prompt", err.Error()))
				continue
			}
			if handled {
				writeRPC(rpcSuccess(env.ID, "prompt", nil))
				continue
			}
			cmd.Message = commandCatalog.expandPrompt(message)
			if streaming() {
				switch cmd.StreamingBehavior {
				case "steer":
					sess.Steer(cmd.Message, images)
				case "followUp":
					sess.FollowUp(cmd.Message, images)
				case "":
					writeRPC(rpcError(env.ID, "prompt", "Agent is already processing. Specify streamingBehavior ('steer' or 'followUp') to queue the message."))
					continue
				default:
					writeRPC(rpcError(env.ID, "prompt", fmt.Sprintf("Invalid streamingBehavior: %s", cmd.StreamingBehavior)))
					continue
				}
				if err := sess.FlushEvents(ctx); err != nil {
					writeRPC(rpcError(env.ID, "prompt", err.Error()))
					continue
				}
				writeRPC(rpcSuccess(env.ID, "prompt", nil))
				continue
			}

			if model := sess.Model(); model == nil {
				writeRPC(rpcError(env.ID, "prompt", codingagent.FormatNoAPIKeyFoundMessage("unknown")))
				continue
			} else {
				providerID := model.ProviderMeta.ProviderID
				if providerID == "" && model.Provider != nil {
					providerID = model.Provider.ID()
				}
				if providerID != "test-faux" && !registry.HasConfiguredAuth(providerID) {
					writeRPC(rpcError(env.ID, "prompt", codingagent.FormatNoAPIKeyFoundMessage(providerID)))
					continue
				}
			}

			// Run Send in a goroutine so we don't block the command reader.
			// Mirrors upstream: the response is written once preflight
			// succeeds, a failure before that is the response, and a failure
			// after it is swallowed (the run's events already report it).
			promptPending.Store(true)
			promptWG.Go(func() {
				accepted := false
				_, sendErr := sess.SendContentWithPreflight(ctx, coding.BuildUserContent(cmd.Message, images), func() {
					accepted = true
					promptPending.Store(false)
					writeRPC(rpcSuccess(env.ID, "prompt", nil))
				})
				if accepted {
					return
				}
				promptPending.Store(false)
				if sendErr != nil {
					writeRPC(rpcError(env.ID, "prompt", sendErr.Error()))
				} else {
					writeRPC(rpcSuccess(env.ID, "prompt", nil))
				}
			})

		// ── abort ────────────────────────────────────────────────────────
		case "abort":
			settlePrompt()
			writeRPC(rpcSuccess(env.ID, "abort", nil))

		case "clear_queue":
			steering, followUp := sess.ClearQueue()
			// Flush the queue_update this emits through the wire before the
			// response, as Pi's clearQueue() emits it synchronously before
			// rpc-mode's success() call runs (steer/follow_up flush the same
			// way for the same reason).
			if err := sess.FlushEvents(ctx); err != nil {
				writeRPC(rpcError(env.ID, "clear_queue", err.Error()))
				continue
			}
			writeRPC(rpcSuccess(env.ID, "clear_queue", RPCClearQueueData{Steering: steering, FollowUp: followUp}))

		case "new_session":
			var cmd RPCNewSessionCommand
			if err := json.Unmarshal(env.Raw, &cmd); err != nil {
				writeRPC(rpcError(env.ID, "new_session", err.Error()))
				continue
			}
			cancelled, err := rpcBeforeSessionSwitch(ctx, runner, "new", "")
			if err != nil {
				writeRPC(rpcError(env.ID, "new_session", err.Error()))
				continue
			}
			if cancelled {
				writeRPC(rpcSuccess(env.ID, "new_session", RPCCancelledResult{Cancelled: true}))
				continue
			}
			sessionID, err := codingagent.GenerateSessionID()
			if err != nil {
				writeRPC(rpcError(env.ID, "new_session", err.Error()))
				continue
			}
			var next *codingagent.Session
			if flags.NoSession {
				next = codingagent.NewSession(sessionID, cwd)
			} else {
				next, err = newSessionManagerWithDir(cwd, sessionDir).Create(sessionID, cmd.ParentSession)
				if err != nil {
					writeRPC(rpcError(env.ID, "new_session", err.Error()))
					continue
				}
			}
			if model := sess.Model(); model != nil {
				if err := next.AppendModelSwitch(model.ProviderMeta.ProviderID, model.ID, model.DisplayName); err != nil {
					writeRPC(rpcError(env.ID, "new_session", err.Error()))
					continue
				}
			}
			if err := next.AppendThinkingLevelChange(string(sess.ThinkingLevel())); err != nil {
				writeRPC(rpcError(env.ID, "new_session", err.Error()))
				continue
			}
			settleSessionWork()
			previous := sess.Path()
			sess.EmitSessionShutdownTransition("new", next.Path())
			sess.ReplaceInner(next)
			sess.EmitSessionStartTransition("new", previous)
			writeRPC(rpcSuccess(env.ID, "new_session", RPCCancelledResult{Cancelled: false}))

		// ── get_commands ───────────────────────────────────────────────
		case "get_commands":
			writeRPC(rpcGetCommandsResponse(env.ID, commandCatalog))

		// ── get_state ────────────────────────────────────────────────────
		case "get_state":
			compactionEnabled := services.SettingsManager().GetCompactionSettings().Enabled
			state := RPCSessionState{
				Model:                 rpcModelValue(sess.Model()),
				ThinkingLevel:         string(sess.ThinkingLevel()),
				IsStreaming:           streaming(),
				IsCompacting:          sess.IsCompacting(),
				SteeringMode:          string(sess.Agent().SteeringMode()),
				FollowUpMode:          string(sess.Agent().FollowUpMode()),
				SessionFile:           sess.Path(),
				SessionID:             sess.ID(),
				SessionName:           sess.SessionName(),
				AutoCompactionEnabled: compactionEnabled,
				MessageCount:          len(sess.Messages()),
				PendingMessageCount:   sess.PendingMessageCount(),
			}
			writeRPC(rpcSuccess(env.ID, "get_state", state))

		// ── set_model ────────────────────────────────────────────────────
		case "set_model":
			var cmd RPCSetModelCommand
			if err := json.Unmarshal(env.Raw, &cmd); err != nil {
				writeRPC(rpcError(env.ID, "set_model", err.Error()))
				continue
			}
			models, err := rpcAvailableModels(services, sess.Model())
			if err != nil {
				writeRPC(rpcError(env.ID, "set_model", err.Error()))
				continue
			}
			var newModel *ai.Model
			for _, candidate := range models {
				if candidate.ProviderMeta.ProviderID == cmd.Provider && candidate.ID == cmd.ModelID {
					newModel = candidate
					break
				}
			}
			if newModel == nil {
				writeRPC(rpcError(env.ID, "set_model", fmt.Sprintf("Model not found: %s/%s", cmd.Provider, cmd.ModelID)))
				continue
			}
			if err := sess.SetModel(newModel); err != nil {
				writeRPC(rpcError(env.ID, "set_model", err.Error()))
				continue
			}
			writeRPC(rpcSuccess(env.ID, "set_model", rpcModelValue(newModel)))

		case "cycle_model":
			commandID := env.ID
			currentSession := sess
			previousCycle := cycleComplete
			cycleComplete = make(chan struct{})
			currentCycle := cycleComplete
			commandWG.Go(func() {
				defer close(currentCycle)
				select {
				case <-ctx.Done():
					return
				case <-previousCycle:
				}
				handleCycleModel(commandID, currentSession)
			})

		// ── compact ─────────────────────────────────────────────────────
		case "compact":
			var cmd RPCCompactCommand
			if err := json.Unmarshal(env.Raw, &cmd); err != nil {
				writeRPC(rpcError(env.ID, "compact", err.Error()))
				continue
			}
			commandID := env.ID
			customInstructions := cmd.CustomInstructions
			currentSession := sess
			commandWG.Go(func() {
				settlePrompt()
				result, err := currentSession.CompactResult(ctx, customInstructions)
				if err != nil {
					writeRPC(rpcError(commandID, "compact", err.Error()))
					return
				}
				writeRPC(rpcSuccess(commandID, "compact", rpcCompactionResult(result)))
			})

		// ── set_auto_compaction ──────────────────────────────────────────
		case "set_auto_compaction":
			var cmd RPCSetAutoCompactionCommand
			if err := json.Unmarshal(env.Raw, &cmd); err != nil {
				writeRPC(rpcError(env.ID, "set_auto_compaction", err.Error()))
				continue
			}
			if settingsMgr := services.SettingsManager(); settingsMgr != nil {
				if err := settingsMgr.UpdateGlobal(func(s *codingagent.Settings) {
					if s.Compaction == nil {
						s.Compaction = &codingagent.CompactionSettingsJSON{}
					}
					s.Compaction.Enabled = &cmd.Enabled
				}); err != nil {
					writeRPC(rpcError(env.ID, "set_auto_compaction", err.Error()))
					continue
				}
			}
			writeRPC(rpcSuccess(env.ID, "set_auto_compaction", nil))

		// ── get_available_models ─────────────────────────────────────────
		case "get_available_models":
			models, err := rpcAvailableModels(services, sess.Model())
			if err != nil {
				writeRPC(rpcError(env.ID, "get_available_models", err.Error()))
				continue
			}
			writeRPC(rpcSuccess(env.ID, "get_available_models", map[string]any{
				"models": rpcModelList(models),
			}))

		// ── set_thinking_level ─────────────────────────────────────────
		case "set_thinking_level":
			var cmd RPCSetThinkingLevelCommand
			if err := json.Unmarshal(env.Raw, &cmd); err != nil {
				writeRPC(rpcError(env.ID, "set_thinking_level", err.Error()))
				continue
			}
			if err := sess.SetThinkingLevel(ai.ThinkingLevel(cmd.Level)); err != nil {
				writeRPC(rpcError(env.ID, "set_thinking_level", err.Error()))
				continue
			}
			writeRPC(rpcSuccess(env.ID, "set_thinking_level", nil))

		// ── cycle_thinking_level ─────────────────────────────────────────────
		case "cycle_thinking_level":
			levels := sess.AvailableThinkingLevels()
			if len(levels) <= 1 {
				writeRPC(rpcSuccessNull(env.ID, "cycle_thinking_level"))
				continue
			}
			current := sess.ThinkingLevel()
			currentIndex := max(slices.Index(levels, current), 0)
			nextLevel := levels[(currentIndex+1)%len(levels)]
			if err := sess.SetThinkingLevel(nextLevel); err != nil {
				writeRPC(rpcError(env.ID, "cycle_thinking_level", err.Error()))
				continue
			}
			writeRPC(rpcSuccess(env.ID, "cycle_thinking_level", map[string]string{
				"level": string(nextLevel),
			}))

		case "get_available_thinking_levels":
			levels := sess.AvailableThinkingLevels()
			writeRPC(rpcSuccess(env.ID, "get_available_thinking_levels", map[string]any{"levels": levels}))

		// ── set_auto_retry ────────────────────────────────────────────────
		case "set_auto_retry":
			var cmd RPCSetAutoRetryCommand
			if err := json.Unmarshal(env.Raw, &cmd); err != nil {
				writeRPC(rpcError(env.ID, "set_auto_retry", err.Error()))
				continue
			}
			if err := sess.SetAutoRetryEnabled(cmd.Enabled); err != nil {
				writeRPC(rpcError(env.ID, "set_auto_retry", err.Error()))
				continue
			}
			writeRPC(rpcSuccess(env.ID, "set_auto_retry", nil))

		case "abort_retry":
			sess.AbortRetry()
			writeRPC(rpcSuccess(env.ID, "abort_retry", nil))

		// ── steer ────────────────────────────────────────────────────────────
		case "steer":
			var cmd RPCSteerCommand
			if err := json.Unmarshal(env.Raw, &cmd); err != nil {
				writeRPC(rpcError(env.ID, "steer", err.Error()))
				continue
			}
			if name, _, ok := commandCatalog.extensionCommand(cmd.Message); ok {
				writeRPC(rpcError(env.ID, "steer", fmt.Sprintf("Extension command %q cannot be queued. Use prompt() or execute the command when not streaming.", name)))
				continue
			}
			// Upstream session.steer: input handlers first
			// (_queueUserInput), then expansion and queueing.
			message, images, handled, err := sess.RunInputHandlers(ctx, cmd.Message, rpcImages(cmd.Images), extension.InputSourceRPC, "steer")
			if err != nil {
				writeRPC(rpcError(env.ID, "steer", err.Error()))
				continue
			}
			if !handled {
				sess.Steer(commandCatalog.expandPrompt(message), images)
			}
			if err := sess.FlushEvents(ctx); err != nil {
				writeRPC(rpcError(env.ID, "steer", err.Error()))
				continue
			}
			writeRPC(rpcSuccess(env.ID, "steer", nil))

		// ── follow_up ─────────────────────────────────────────────────────────
		case "follow_up":
			var cmd RPCFollowUpCommand
			if err := json.Unmarshal(env.Raw, &cmd); err != nil {
				writeRPC(rpcError(env.ID, "follow_up", err.Error()))
				continue
			}
			if name, _, ok := commandCatalog.extensionCommand(cmd.Message); ok {
				writeRPC(rpcError(env.ID, "follow_up", fmt.Sprintf("Extension command %q cannot be queued. Use prompt() or execute the command when not streaming.", name)))
				continue
			}
			// Upstream session.followUp: input handlers first
			// (_queueUserInput), then expansion and queueing.
			message, images, handled, err := sess.RunInputHandlers(ctx, cmd.Message, rpcImages(cmd.Images), extension.InputSourceRPC, "followUp")
			if err != nil {
				writeRPC(rpcError(env.ID, "follow_up", err.Error()))
				continue
			}
			if !handled {
				sess.FollowUp(commandCatalog.expandPrompt(message), images)
			}
			if err := sess.FlushEvents(ctx); err != nil {
				writeRPC(rpcError(env.ID, "follow_up", err.Error()))
				continue
			}
			writeRPC(rpcSuccess(env.ID, "follow_up", nil))

		// ── bash ────────────────────────────────────────────────────────────────
		case "bash":
			var cmd RPCBashCommand
			if err := json.Unmarshal(env.Raw, &cmd); err != nil {
				writeRPC(rpcError(env.ID, "bash", err.Error()))
				continue
			}
			// Run bash in a goroutine (may block for a long time).
			bashWG.Add(1)
			go func(id, command string, excludeFromContext bool) {
				defer bashWG.Done()
				// Upstream rpc-mode awaits emitUserBash first: a result override is
				// recorded and returned without running the command, and a failed
				// handler fails the request (#9068).
				override, operations, err := rpcUserBashOverride(ctx, runner, command, excludeFromContext, sess.CWD())
				if err != nil {
					writeRPC(rpcError(id, "bash", err.Error()))
					return
				}
				if override != nil {
					sess.RecordBashResult(command, *override, excludeFromContext)
					writeRPC(rpcSuccess(id, "bash", RPCBashResult(*override)))
					return
				}
				result, err := sess.ExecuteBashWithOperations(ctx, command, excludeFromContext, func(delta string) {
					writeRPC(RPCBashExecutionUpdate{Type: "bash_execution_update", ID: id, Delta: delta})
				}, operations)
				if err != nil {
					writeRPC(rpcError(id, "bash", err.Error()))
					return
				}
				writeRPC(rpcSuccess(id, "bash", RPCBashResult{
					Output:         result.Output,
					ExitCode:       result.ExitCode,
					Cancelled:      result.Cancelled,
					Truncated:      result.Truncated,
					FullOutputPath: result.FullOutputPath,
				}))
			}(env.ID, cmd.Command, cmd.ExcludeFromContext)

		// ── abort_bash ────────────────────────────────────────────────────────
		case "abort_bash":
			sess.AbortBash()
			writeRPC(rpcSuccess(env.ID, "abort_bash", nil))

		// ── set_session_name ─────────────────────────────────────────────────
		case "set_session_name":
			var cmd RPCSetSessionNameCommand
			if err := json.Unmarshal(env.Raw, &cmd); err != nil {
				writeRPC(rpcError(env.ID, "set_session_name", err.Error()))
				continue
			}
			name := strings.TrimSpace(cmd.Name)
			if name == "" {
				writeRPC(rpcError(env.ID, "set_session_name", "Session name cannot be empty"))
				continue
			}
			if err := sess.SetSessionName(name); err != nil {
				writeRPC(rpcError(env.ID, "set_session_name", err.Error()))
				continue
			}
			name = sess.SessionName()
			writeRPC(rpcSessionInfoChanged(name))
			if runner != nil {
				extensionEvents.Go(func() {
					_, _ = runner.Emit(ctx, extension.SessionInfoChangedEvent{
						Type: codingagent.EventSessionInfoChanged,
						Name: name,
					})
				})
			}
			writeRPC(rpcSuccess(env.ID, "set_session_name", nil))

		// ── get_session_stats ────────────────────────────────────────────────
		case "get_session_stats":
			writeRPC(rpcSuccess(env.ID, "get_session_stats", sess.GetSessionStats()))

		case "export_html":
			var cmd RPCExportHTMLCommand
			if err := json.Unmarshal(env.Raw, &cmd); err != nil {
				writeRPC(rpcError(env.ID, "export_html", err.Error()))
				continue
			}
			if sess.Path() == "" {
				writeRPC(rpcError(env.ID, "export_html", "Cannot export an in-memory session"))
				continue
			}
			outputPath, err := codingexport.ExportFromFile(sess.Path(), cmd.OutputPath)
			if err != nil {
				writeRPC(rpcError(env.ID, "export_html", err.Error()))
				continue
			}
			writeRPC(rpcSuccess(env.ID, "export_html", map[string]string{"path": outputPath}))

		case "switch_session":
			var cmd RPCSwitchSessionCommand
			if err := json.Unmarshal(env.Raw, &cmd); err != nil {
				writeRPC(rpcError(env.ID, "switch_session", err.Error()))
				continue
			}
			if cmd.SessionPath == "" {
				writeRPC(rpcError(env.ID, "switch_session", "EISDIR: illegal operation on a directory, read"))
				continue
			}
			cancelled, err := rpcBeforeSessionSwitch(ctx, runner, "resume", cmd.SessionPath)
			if err != nil {
				writeRPC(rpcError(env.ID, "switch_session", err.Error()))
				continue
			}
			if cancelled {
				writeRPC(rpcSuccess(env.ID, "switch_session", RPCCancelledResult{Cancelled: true}))
				continue
			}
			next, err := newSessionManagerWithDir(cwd, sessionDir).Load(cmd.SessionPath)
			if err != nil {
				writeRPC(rpcError(env.ID, "switch_session", err.Error()))
				continue
			}
			settleSessionWork()
			previous := sess.Path()
			sess.EmitSessionShutdownTransition("resume", cmd.SessionPath)
			sess.ReplaceInner(next)
			sess.EmitSessionStartTransition("resume", previous)
			writeRPC(rpcSuccess(env.ID, "switch_session", RPCCancelledResult{Cancelled: false}))

		case "fork":
			var cmd RPCForkCommand
			if err := json.Unmarshal(env.Raw, &cmd); err != nil {
				writeRPC(rpcError(env.ID, "fork", err.Error()))
				continue
			}
			if cmd.EntryID == "" {
				writeRPC(rpcError(env.ID, "fork", "Invalid entry ID for forking"))
				continue
			}
			cancelled, err := rpcBeforeSessionFork(ctx, runner, cmd.EntryID, "before")
			if err != nil {
				writeRPC(rpcError(env.ID, "fork", err.Error()))
				continue
			}
			if cancelled {
				writeRPC(rpcSuccess(env.ID, "fork", RPCForkResult{Cancelled: true}))
				continue
			}
			forked, text, err := newSessionManagerWithDir(cwd, sessionDir).ForkToNewSession(sess.Inner(), cmd.EntryID)
			if err != nil {
				writeRPC(rpcError(env.ID, "fork", err.Error()))
				continue
			}
			settleSessionWork()
			previous := sess.Path()
			sess.EmitSessionShutdownTransition("fork", forked.Path())
			sess.ReplaceInner(forked)
			sess.EmitSessionStartTransition("fork", previous)
			writeRPC(rpcSuccess(env.ID, "fork", RPCForkResult{Text: text, Cancelled: false}))

		case "clone":
			leaf := sess.LeafID()
			if leaf == nil {
				writeRPC(rpcError(env.ID, "clone", "Cannot clone session: no current entry selected"))
				continue
			}
			cancelled, err := rpcBeforeSessionFork(ctx, runner, *leaf, "at")
			if err != nil {
				writeRPC(rpcError(env.ID, "clone", err.Error()))
				continue
			}
			if cancelled {
				writeRPC(rpcSuccess(env.ID, "clone", RPCCancelledResult{Cancelled: true}))
				continue
			}
			cloned, err := newSessionManagerWithDir(cwd, sessionDir).Clone(sess.Inner(), *leaf)
			if err != nil {
				writeRPC(rpcError(env.ID, "clone", err.Error()))
				continue
			}
			settleSessionWork()
			previous := sess.Path()
			sess.EmitSessionShutdownTransition("fork", cloned.Path())
			sess.ReplaceInner(cloned)
			sess.EmitSessionStartTransition("fork", previous)
			writeRPC(rpcSuccess(env.ID, "clone", RPCCancelledResult{Cancelled: false}))

		// ── get_messages ────────────────────────────────────────────────────────
		case "get_messages":
			writeRPC(rpcSuccess(env.ID, "get_messages", map[string]any{
				"messages": sess.Messages(),
			}))

		// ── get_last_assistant_text ───────────────────────────────────────────
		case "get_last_assistant_text":
			// Upstream: success(id, "get_last_assistant_text", { text })
			// When getLastAssistantText() returns undefined, JS serializes
			// {text: undefined} as {} (keys with undefined values are omitted).
			// Match by omitting the key when nil.
			if text := sess.LastAssistantText(); text != nil {
				writeRPC(rpcSuccess(env.ID, "get_last_assistant_text", map[string]any{
					"text": *text,
				}))
			} else {
				writeRPC(rpcSuccess(env.ID, "get_last_assistant_text", map[string]any{}))
			}

		// ── get_fork_messages ─────────────────────────────────────────────────
		case "get_fork_messages":
			writeRPC(rpcSuccess(env.ID, "get_fork_messages", map[string]any{
				"messages": sess.UserMessagesForForking(),
			}))

		// ── get_entries ───────────────────────────────────────────────────────
		case "get_entries":
			var cmd RPCGetEntriesCommand
			if err := json.Unmarshal(env.Raw, &cmd); err != nil {
				writeRPC(rpcError(env.ID, "get_entries", err.Error()))
				continue
			}
			entries, err := rpcEntriesSince(sess.Entries(), cmd.Since)
			if err != nil {
				writeRPC(rpcError(env.ID, "get_entries", err.Error()))
				continue
			}
			writeRPC(rpcSuccess(env.ID, "get_entries", rpcEntriesData{
				Entries: entries,
				LeafID:  sess.LeafID(),
			}))

		// ── get_tree ──────────────────────────────────────────────────────────
		case "get_tree":
			writeRPC(rpcSuccess(env.ID, "get_tree", rpcTreeData{
				Tree:   rpcTree(sess.Tree()),
				LeafID: sess.LeafID(),
			}))

		// ── set_steering_mode / set_follow_up_mode ─────────────────────────
		// Persist to settings so the value survives sessions.
		case "set_steering_mode":
			var cmd RPCSetSteeringModeCommand
			if err := json.Unmarshal(env.Raw, &cmd); err != nil {
				writeRPC(rpcError(env.ID, "set_steering_mode", err.Error()))
				continue
			}
			if err := sess.SetSteeringMode(agent.QueueMode(cmd.Mode)); err != nil {
				writeRPC(rpcError(env.ID, "set_steering_mode", err.Error()))
				continue
			}
			writeRPC(rpcSuccess(env.ID, "set_steering_mode", nil))

		case "set_follow_up_mode":
			var cmd RPCSetFollowUpModeCommand
			if err := json.Unmarshal(env.Raw, &cmd); err != nil {
				writeRPC(rpcError(env.ID, "set_follow_up_mode", err.Error()))
				continue
			}
			if err := sess.SetFollowUpMode(agent.QueueMode(cmd.Mode)); err != nil {
				writeRPC(rpcError(env.ID, "set_follow_up_mode", err.Error()))
				continue
			}
			writeRPC(rpcSuccess(env.ID, "set_follow_up_mode", nil))

		default:
			// Upstream (0.79.10): error(id, unknownCommand.type, `Unknown command: ${unknownCommand.type}`)
			// 0.79.x changed this from error(undefined, ...) to echo the
			// request id so clients can correlate the error response.
			writeRPC(rpcError(env.ID, env.Type,
				fmt.Sprintf("Unknown command: %s", env.Type)))
		} // end switch
	} // end for scanner.Scan()

	if scanErr := <-inputDone; scanErr != nil && !errors.Is(scanErr, io.EOF) {
		writeRPC(RPCErrorEvent{
			Type:    "error",
			Message: fmt.Sprintf("stdin read error: %v", scanErr),
		})
	}

	// Stop admitting or blocking on extension work before joining commands.
	// A model-select handler can be waiting for an RPC dialog while its command
	// is in commandWG, so waiting first would deadlock on EOF.
	cancel()
	rpcUI.Close()
	settleSessionWork()
	extensionEvents.CloseAndWait()

	// Session Close drains and closes the events channel.
	_ = sess.Close()
	<-eventsDone
	select {
	case <-eventConversionErr:
		return 1
	default:
	}

	return 0
}

type rpcEntriesData struct {
	Entries []codingagent.SessionEntry `json:"entries"`
	LeafID  *string                    `json:"leafId"`
}

type rpcTreeData struct {
	Tree   []rpcSessionTreeNode `json:"tree"`
	LeafID *string              `json:"leafId"`
}

// rpcSessionTreeNode is the JSON shape returned by upstream get_tree.
// Pig's internal tree uses exported Go fields and a synthetic root; RPC returns
// lower-case keys and only real root entries.
type rpcSessionTreeNode struct {
	Entry          codingagent.SessionEntry `json:"entry"`
	Children       []rpcSessionTreeNode     `json:"children"`
	Label          string                   `json:"label,omitempty"`
	LabelTimestamp string                   `json:"labelTimestamp,omitempty"`
}

type rpcEventRunner interface {
	Emit(context.Context, any) (any, error)
}

func rpcBeforeSessionSwitch(ctx context.Context, runner rpcEventRunner, reason, target string) (bool, error) {
	if runner == nil {
		return false, nil
	}
	result, err := runner.Emit(ctx, extension.SessionBeforeSwitchEvent{
		Type:              codingagent.EventSessionBeforeSwitch,
		Reason:            reason,
		TargetSessionFile: target,
	})
	if err != nil {
		return false, err
	}
	return rpcResultCancelled(result), nil
}

func rpcBeforeSessionFork(ctx context.Context, runner rpcEventRunner, entryID, position string) (bool, error) {
	if runner == nil {
		return false, nil
	}
	result, err := runner.Emit(ctx, extension.SessionBeforeForkEvent{
		Type:     codingagent.EventSessionBeforeFork,
		EntryID:  entryID,
		Position: position,
	})
	if err != nil {
		return false, err
	}
	return rpcResultCancelled(result), nil
}

func rpcResultCancelled(result any) bool {
	switch value := result.(type) {
	case extension.SessionBeforeSwitchResult:
		return value.Cancel
	case *extension.SessionBeforeSwitchResult:
		return value != nil && value.Cancel
	case extension.SessionBeforeForkResult:
		return value.Cancel
	case *extension.SessionBeforeForkResult:
		return value != nil && value.Cancel
	}
	data, err := json.Marshal(result)
	if err != nil {
		return false
	}
	var value struct {
		Cancel bool `json:"cancel"`
	}
	return json.Unmarshal(data, &value) == nil && value.Cancel
}

func rpcAvailableModels(services *coding.Services, current *ai.Model) ([]*ai.Model, error) {
	registry := services.Registry()
	entries := registry.GetAvailable()
	seen := make(map[string]struct{}, len(entries))
	models := make([]*ai.Model, 0, len(entries))
	for _, entry := range entries {
		model, err := coding.BuildModel(entry.ProviderID+"/"+entry.ModelID, services)
		if err != nil {
			return nil, err
		}
		key := entry.ProviderID + "\x00" + entry.ModelID
		seen[key] = struct{}{}
		models = append(models, model)
	}

	authed := codingagent.AuthenticatedProviders(services.AgentDir())
	for _, generated := range ai.ListModels("") {
		if !authed[generated.Provider] {
			continue
		}
		key := generated.Provider + "\x00" + generated.ID
		if _, exists := seen[key]; exists {
			continue
		}
		model, err := coding.BuildModel(generated.Provider+"/"+generated.ID, services)
		if err != nil {
			return nil, err
		}
		seen[key] = struct{}{}
		models = append(models, model)
	}
	if current != nil {
		provider := current.ProviderMeta.ProviderID
		if provider == "" && current.Provider != nil {
			provider = current.Provider.ID()
		}
		key := provider + "\x00" + current.ID
		if _, exists := seen[key]; !exists {
			models = append(models, current)
		}
	}
	return models, nil
}

func rpcScopedModels(models []*ai.Model, patterns []string) []*ai.Model {
	if len(patterns) == 0 {
		return models
	}
	var scoped []*ai.Model
	seen := make(map[string]struct{})
	for _, pattern := range patterns {
		pattern = rpcModelPatternWithoutThinking(pattern)
		for _, model := range models {
			provider := model.ProviderMeta.ProviderID
			if provider == "" && model.Provider != nil {
				provider = model.Provider.ID()
			}
			fullID := provider + "/" + model.ID
			matched := strings.EqualFold(pattern, fullID) || strings.EqualFold(pattern, model.ID)
			if !matched && strings.ContainsAny(pattern, "*?[") {
				fullMatch, fullErr := path.Match(strings.ToLower(pattern), strings.ToLower(fullID))
				idMatch, idErr := path.Match(strings.ToLower(pattern), strings.ToLower(model.ID))
				matched = (fullErr == nil && fullMatch) || (idErr == nil && idMatch)
			}
			if !matched {
				continue
			}
			key := provider + "\x00" + model.ID
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			scoped = append(scoped, model)
		}
	}
	return scoped
}

func rpcModelPatternWithoutThinking(pattern string) string {
	index := strings.LastIndex(pattern, ":")
	if index < 0 {
		return pattern
	}
	switch pattern[index+1:] {
	case "off", "minimal", "low", "medium", "high", "xhigh", "max":
		return pattern[:index]
	default:
		return pattern
	}
}

func rpcModelList(models []*ai.Model) []*RPCModel {
	out := make([]*RPCModel, len(models))
	for i, model := range models {
		out[i] = rpcModelValue(model)
	}
	return out
}

func rpcEntriesSince(entries []codingagent.SessionEntry, since string) ([]codingagent.SessionEntry, error) {
	if since == "" {
		return entries, nil
	}
	for i, entry := range entries {
		if entry.Base.ID == since {
			return entries[i+1:], nil
		}
	}
	return nil, fmt.Errorf("Entry not found: %s", since)
}

func rpcTree(root *codingagent.SessionTreeNode) []rpcSessionTreeNode {
	if root == nil {
		return nil
	}
	out := make([]rpcSessionTreeNode, len(root.Children))
	for i, child := range root.Children {
		out[i] = rpcTreeNode(child)
	}
	return out
}

func rpcTreeNode(node *codingagent.SessionTreeNode) rpcSessionTreeNode {
	out := rpcSessionTreeNode{
		Entry:          node.Entry,
		Children:       make([]rpcSessionTreeNode, len(node.Children)),
		Label:          node.Label,
		LabelTimestamp: node.LabelTimestamp,
	}
	for i, child := range node.Children {
		out.Children[i] = rpcTreeNode(child)
	}
	return out
}

// jsifyJSONErrorMessage rewrites Go encoding/json error wording so it matches
// the JavaScript SyntaxError wording emitted by upstream pi's V8-based parser.
// Only the surface texts that flow through the RPC `parse` error path are
// translated: every other error path passes upstream's own messages through.
//
// Locked by parity scenarios rpc/03-rpc-state-setters.toml and
// rpc/04-rpc-jsonl-framing.toml. See `Failed to parse command: ...`.
func jsifyJSONErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if msg == "unexpected end of JSON input" {
		return "Unexpected end of JSON input"
	}
	return msg
}

// rpcUserBashOverride dispatches user_bash for an RPC bash command. It returns
// the extension's replacement result, or the operations to run the command
// through (nil for local execution), or the handler failure that must fail
// the request.
func rpcUserBashOverride(ctx context.Context, runner *inproc.Runner, command string, excludeFromContext bool, cwd string) (*coding.BashResult, extension.BashOperations, error) {
	if runner == nil || !runner.HasHandlers("user_bash") {
		return nil, nil, nil
	}
	eventResult, err := runner.EmitUserBash(ctx, extension.UserBashEvent{
		Type: "user_bash", Command: command, ExcludeFromContext: excludeFromContext, Cwd: cwd,
	})
	if err != nil || eventResult == nil {
		return nil, nil, err
	}
	if eventResult.Result == nil {
		return nil, eventResult.Operations, nil
	}
	encoded, err := json.Marshal(eventResult.Result)
	if err != nil {
		return nil, nil, err
	}
	var result coding.BashResult
	if err := json.Unmarshal(encoded, &result); err != nil {
		return nil, nil, err
	}
	return &result, nil, nil
}
