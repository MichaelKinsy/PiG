package sdkscenarios

// hostEvents are the frames the host broadcasts to every extension process and
// the observable effect each has on an SDK (wiring-checks F5, F6, F7).
func hostEvents() []Scenario {
	const group = "host-events"
	provider := func(name string) Provider {
		return Provider{ID: "conf-shared", Name: name, APIKey: &APIKey{Name: name + " key"}}
	}
	probe := Command{Name: "probe", Description: "Report the registered provider", Op: CommandRegisteredProvider, Arg: "conf-shared"}
	return []Scenario{
		{
			ID: "provider-superseded-drops-the-old-registration", Group: group,
			Pi:     "model-runtime.ts:884-888 registerNativeProvider keeps one effective registration per provider (nativeExtensionProviders.set) and :523-525 getRegisteredNativeProvider returns it, so after a second extension registers the same id the first extension's modelRegistry.getRegisteredNativeProvider returns the latest provider, not its own",
			Covers: []string{"notify provider_superseded", "call registerProvider"},
			Spec:   Spec{Providers: []Provider{provider("First")}, Commands: []Command{probe}},
			Peer:   &Spec{Providers: []Provider{provider("Second")}},
			Steps: []Step{
				{Op: "settle"},
				{Op: "command", Name: "probe"},
				{Op: "load-peer"},
				{Op: "settle"},
				{Op: "host-provider", Provider: "conf-shared"},
				{Op: "command", Name: "probe"},
			},
			Want: []string{
				`command probe First`,
				`peer loaded`,
				`host holds conf-shared Second`,
				`command probe Second`,
			},
		},
		{
			ID: "reload-makes-the-captured-context-stale", Group: group,
			Pi:     "agent-session.ts:3660-3664 reload invalidates the old runner before the new extensions load, and runner.ts:894-896 guards ctx.cwd (like every ctx member) with assertActive, which throws the stale message (runner.ts:721-735); so after `await ctx.reload()` the command's own captured ctx throws",
			Covers: []string{"notify invalidate", "call reload", "request command"},
			Spec:   Spec{Commands: []Command{{Name: "rl", Description: "Reload and probe", Op: CommandReloadStale}}},
			Steps:  []Step{{Op: "settle"}, {Op: "command", Name: "rl"}},
			Want: []string{
				`command rl cwd threw: This extension ctx is stale after session replacement or reload. Do not use a captured pi or command ctx after ctx.newSession(), ctx.fork(), ctx.switchSession(), or ctx.reload(). For newSession, fork, and switchSession, move post-replacement work into withSession and use the ctx passed to withSession. For reload, do not use the old ctx after await ctx.reload().`,
			},
		},
		{
			ID: "reload-keeps-the-process-and-calls-each-event-handler-once", Group: group,
			Pi:     "/reload re-invokes the factories inside the process that holds the module (additive-features.md, runtime cells) and replaces the handlers: session_shutdown (reason reload) reaches the old handlers and finishes, session_start (reason reload, previousSessionFile) reaches the new ones, and an event never reaches a handler twice (the counter continues because the process stays, and every event shows one line)",
			Covers: []string{"request event"}, // the reload step is Host.Reload, which no wire request carries: the runtime sees the old generation's session_shutdown event and the new generation's registration
			Spec:   Spec{Events: []Event{{Name: "session_start", Op: EventRecordCounted, Tag: "gen"}, {Name: "session_shutdown", Op: EventRecordCounted, Tag: "gen"}}},
			Steps: []Step{
				{Op: "emit-event", Name: "session_start", Args: `{"reason":"startup"}`},
				{Op: "emit-event", Name: "session_shutdown", Args: `{"reason":"reload"}`},
				{Op: "reload"},
				{Op: "emit-event", Name: "session_start", Args: `{"reason":"reload","previousSessionFile":"/s/old.jsonl"}`},
				{Op: "emit-event", Name: "session_shutdown", Args: `{"reason":"quit"}`},
			},
			Want: []string{
				`emit session_start handler gen@1 saw {"reason":"startup","type":"session_start"}`,
				`emit session_shutdown handler gen@2 saw {"reason":"reload","type":"session_shutdown"}`,
				`reload loaded=1`,
				`emit session_start handler gen@3 saw {"previousSessionFile":"/s/old.jsonl","reason":"reload","type":"session_start"}`,
				`emit session_shutdown handler gen@4 saw {"reason":"quit","type":"session_shutdown"}`,
			},
		},
		{
			ID: "reload-replaces-the-registrations", Group: group,
			Pi: "runner.ts reload: a reload discards the old generation's registrations and loads the new factory's; the extension's tools and commands are those of the new generation",
			// reload_started is not covered: it only releases a Node runtime waiting in place in print or JSON mode, which this row never enters, and the row passes with the notification removed from Host.Reload.
			Covers: []string{"request command"},
			Spec: Spec{
				Tools:    []Tool{{Name: "alpha", Description: "Alpha tool", Behavior: ToolEcho}},
				Commands: []Command{{Name: "ping", Description: "Ping", Op: CommandPing}},
			},
			Steps: []Step{
				{Op: "registrations"},
				{Op: "command", Name: "ping"},
				{Op: "reload", Spec: &Spec{
					Tools:    []Tool{{Name: "beta", Description: "Beta tool", Behavior: ToolEcho}},
					Commands: []Command{{Name: "pong", Description: "Pong", Op: CommandPing}},
				}},
				{Op: "registrations"},
				{Op: "command", Name: "pong"},
			},
			Want: []string{
				`tool alpha "Alpha tool" params={"properties":{"text":{"type":"string"}},"type":"object"}`,
				`command ping "Ping"`,
				`command ping pong`,
				`reload loaded=1`,
				`tool beta "Beta tool" params={"properties":{"text":{"type":"string"}},"type":"object"}`,
				`command pong "Pong"`,
				`command pong pong`,
			},
		},
		{
			ID: "input-end-counts-the-hung-command", Group: group,
			Pi:     "rpc-mode.ts:802-805: at stdin's end a command that has not answered is left unanswered, so the host counts it for every runtime (Node reports it after runtime_input_end, a native runtime has no microtask continuation, so the host counts it at once)",
			Covers: []string{"notify runtime_input_end", "request command"},
			Spec:   Spec{Commands: []Command{{Name: "stuck", Description: "Never finishes", Op: CommandHang}}},
			Steps:  []Step{{Op: "settle"}, {Op: "end-input", Name: "stuck"}},
			Want:   []string{`input ended: unanswered commands=1`},
		},
	}
}
