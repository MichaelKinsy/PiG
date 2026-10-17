package sdkscenarios

// contextScenarios is the extension context an extension reads and acts through: the
// agent state getters answer from the host (the rig serves values no SDK could
// default to), the session manager reads a fixed session, and abort, shutdown
// and compact reach the host.
func contextScenarios() []Scenario {
	const group = "context"
	query := func(name, arg string) Command {
		return Command{Name: name, Description: "Query " + arg, Op: CommandQuery, Arg: arg}
	}
	action := func(name, arg string) Command {
		return Command{Name: name, Description: "Action " + arg, Op: CommandAction, Arg: arg}
	}
	spec := Spec{Commands: []Command{
		query("q-idle", "idle"),
		query("q-pending", "pending"),
		query("q-prompt", "system-prompt"),
		query("q-thinking", "thinking"),
		query("q-tools", "all-tools"),
		query("q-header", "session-header"),
		query("q-leaf", "leaf-entry"),
		query("q-manager-id", "session-manager-id"),
		action("a-abort", "abort"),
		action("a-shutdown", "shutdown"),
		action("a-compact", "compact"),
		{Name: "r-auth-yes", Description: "Registry has-auth", Op: CommandRegistry, Arg: "has-auth:configured"},
		{Name: "r-auth-no", Description: "Registry has-auth", Op: CommandRegistry, Arg: "has-auth:plain"},
		{Name: "r-auth-unknown", Description: "Registry has-auth", Op: CommandRegistry, Arg: "has-auth:nobody"},
		{Name: "r-oauth-yes", Description: "Registry using-oauth", Op: CommandRegistry, Arg: "using-oauth:configured"},
		{Name: "r-oauth-no", Description: "Registry using-oauth", Op: CommandRegistry, Arg: "using-oauth:plain"},
		{Name: "r-auth-key", Description: "Registry has-auth", Op: CommandRegistry, Arg: "has-auth:keyonly"},
		{Name: "r-oauth-key", Description: "Registry using-oauth", Op: CommandRegistry, Arg: "using-oauth:keyonly"},
		{Name: "r-provider", Description: "Registry provider-name", Op: CommandRegistry, Arg: "provider-name:owned"},
		{Name: "r-provider-none", Description: "Registry provider-name", Op: CommandRegistry, Arg: "provider-name:nobody"},
	}, Providers: []Provider{{ID: "owned", Name: "Owned Provider"}}}
	return []Scenario{
		{
			ID: "state-getters-answer-from-the-host", Group: group,
			Pi:     "runner.ts createContext and agent-session.ts _bindExtensionCore: ctx.isIdle(), ctx.hasPendingMessages(), ctx.getSystemPrompt() and pi.getThinkingLevel()/getAllTools() return what the session answers, not a default",
			Covers: []string{"call isIdle", "call hasPendingMessages", "call getSystemPrompt", "call getThinkingLevel", "call getAllTools"},
			Spec:   spec,
			Steps: []Step{
				{Op: "command", Name: "q-idle", JSON: true},
				{Op: "command", Name: "q-pending", JSON: true},
				{Op: "command", Name: "q-prompt", JSON: true},
				{Op: "command", Name: "q-thinking", JSON: true},
				{Op: "command", Name: "q-tools", JSON: true},
			},
			Want: []string{
				`command q-idle false`,
				`command q-pending true`,
				`command q-prompt "scenario system prompt"`,
				`command q-thinking "high"`,
				`command q-tools [{"description":"Read files","name":"read"},{"description":"Search files","name":"grep"}]`,
			},
		},
		{
			ID: "session-manager-reads-the-session", Group: group,
			Pi:     "runner.ts createContext sessionManager: ctx.sessionManager.getHeader(), getLeafEntry() and getSessionId() read the session the host holds",
			Covers: []string{"call sessionRead"},
			Spec:   spec,
			Steps: []Step{
				{Op: "command", Name: "q-header", JSON: true},
				{Op: "command", Name: "q-leaf", JSON: true},
				{Op: "command", Name: "q-manager-id", JSON: true},
			},
			Want: []string{
				`command q-header {"cwd":"/conformance/registry","id":"registry-session","type":"session"}`,
				`command q-leaf {"id":"two","type":"label"}`,
				`command q-manager-id "registry-session"`,
			},
		},
		{
			ID: "model-registry-answers-from-the-host-state", Group: group,
			Pi:     "model-registry.ts:85-86,206-207 hasConfiguredAuth and isUsingOAuth ask the runtime about the model's provider; getProvider returns the Provider object registered under the id, or undefined",
			Covers: []string{"call getModelRegistryState"},
			Spec:   spec,
			Steps: []Step{
				{Op: "command", Name: "r-auth-yes", JSON: true},
				{Op: "command", Name: "r-auth-no", JSON: true},
				{Op: "command", Name: "r-auth-unknown", JSON: true},
				{Op: "command", Name: "r-oauth-yes", JSON: true},
				{Op: "command", Name: "r-oauth-no", JSON: true},
				{Op: "command", Name: "r-auth-key", JSON: true},
				{Op: "command", Name: "r-oauth-key", JSON: true},
				{Op: "command", Name: "r-provider", JSON: true},
				{Op: "command", Name: "r-provider-none", JSON: true},
			},
			Want: []string{
				`command r-auth-yes true`,
				`command r-auth-no false`,
				`command r-auth-unknown false`,
				`command r-oauth-yes true`,
				`command r-oauth-no false`,
				`command r-auth-key true`,
				`command r-oauth-key false`,
				`command r-provider "Owned Provider"`,
				`command r-provider-none null`,
			},
		},
		{
			ID: "ctx-signal-follows-the-hosts-run", Group: group,
			Pi:     "runner.ts:917-920 ctx.signal is a getter over agent.signal: undefined while no run is active, the run's AbortSignal during one, aborted once the run is aborted, undefined again after it ends",
			Covers: []string{"notify run_signal", "request command"},
			Spec:   Spec{Commands: []Command{{Name: "sig", Description: "Reports ctx.signal", Op: CommandSignal}}},
			Steps: []Step{
				{Op: "command", Name: "sig"},
				{Op: "run-signal", Name: "begin"},
				{Op: "command", Name: "sig"},
				{Op: "run-signal", Name: "abort"},
				{Op: "command", Name: "sig"},
				{Op: "run-signal", Name: "end"},
				{Op: "command", Name: "sig"},
			},
			Want: []string{`command sig none`, `command sig live`, `command sig aborted`, `command sig none`},
		},
		{
			ID: "aborting-the-run-reaches-a-command-already-waiting-on-ctx-signal", Group: group,
			Pi:     "runner.ts:917-920 ctx.signal is the live agent.signal: a handler that took it during the run sees the abort that comes later",
			Covers: []string{"notify run_signal", "request command"},
			Spec:   Spec{Commands: []Command{{Name: "waiter", Description: "Waits for ctx.signal", Op: CommandSignalWait}}},
			Steps:  []Step{{Op: "abort-run-in-command", Name: "waiter"}},
			Want:   []string{`command waiter signal aborted while running`},
		},
		{
			ID: "abort-shutdown-and-compact-reach-the-host", Group: group,
			Pi:     "agent-session.ts _bindExtensionCore: ctx.abort(), ctx.shutdown() and ctx.compact(options) call the session; compact carries customInstructions",
			Covers: []string{"call abort", "call shutdown", "call compact"},
			Spec:   spec,
			Steps: []Step{
				{Op: "command", Name: "a-abort"},
				{Op: "host-calls", Count: 1},
				{Op: "command", Name: "a-shutdown"},
				{Op: "host-calls", Count: 2},
				{Op: "command", Name: "a-compact"},
				{Op: "host-calls", Count: 3},
			},
			Want: []string{
				`command a-abort called`,
				`host call abort`,
				`command a-shutdown called`,
				`host call shutdown`,
				`command a-compact called`,
				`host call compact instructions=be brief`,
			},
		},
	}
}
